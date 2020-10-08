package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/caarlos0/env/v6"
	"github.com/ghodss/yaml"
	"github.com/google/go-github/v32/github"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/thepwagner/action-update-docker/docker"
	"github.com/thepwagner/action-update-dockerurl/dockerurl"
	"github.com/thepwagner/action-update-go/gomodules"
	"github.com/thepwagner/action-update/actions/updateaction"
	"github.com/thepwagner/action-update/cmd"
	gitrepo "github.com/thepwagner/action-update/repo"
)

const (
	cfgKeep       = "Keep"
	cfgBranchName = "Branch"
)

var updateCmd = &cobra.Command{
	Use:          "update <url>",
	Short:        "Perform dependency updates",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		viper.SetDefault(cfgKeep, false)
		viper.SetDefault(cfgBranchName, "master")

		var target string
		if len(args) > 0 {
			target = args[0]
		} else {
			target = "https://github.com/thepwagner/action-update-go"
		}
		return MockUpdate(context.Background(), target)
	},
}

func MockUpdate(ctx context.Context, target string) error {
	logrus.WithFields(logrus.Fields{
		"target":  target,
		"updater": updaterType,
	}).Info("performing mock update")

	// Setup a tempdir for the clone:
	dir, err := ioutil.TempDir("", "action-update-go-*")
	if err != nil {
		return err
	}
	dirLog := logrus.WithField("temp_dir", dir)
	dirLog.Debug("created tempdir")
	if !viper.GetBool(cfgKeep) {
		defer func() {
			if err := os.RemoveAll(dir); err != nil {
				dirLog.WithError(err).Warn("error cleaning temp dir")
			}
		}()
	}

	parsed, err := parseTargetURL(target)
	if err != nil {
		return err
	}

	ghToken := viper.GetString(cfgGitHubToken)
	gh := gitrepo.NewGitHubClient(ghToken)

	ok, err := cloneAndSetEnv(ctx, parsed, gh, dir)
	if err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("could not detect environment")
	}
	dirLog.Info("cloned to tempdir")

	if err := os.Chdir(dir); err != nil {
		return err
	}

	var environment *updateaction.Environment
	var params updateaction.HandlerParams
	switch updaterType {
	case "docker":
		e := &docker.Environment{}
		environment = &e.Environment
		params = e
	case "dockerurl":
		e := &dockerurl.Environment{}
		environment = &e.Environment
		params = e
	case "go":
		e := &gomodules.Environment{}
		environment = &e.Environment
		params = e
	default:
		return fmt.Errorf("unknown updater: %w", err)
	}

	if err := loadConfiguration(ctx, gh, parsed, params); err != nil {
		return err
	}

	handlers := updateaction.NewHandlers(params)
	return handlers.Handle(ctx, &environment.Environment)
}

func cloneAndSetEnv(ctx context.Context, parsed *parsedTarget, gh *github.Client, dir string) (bool, error) {
	if err := parsed.initRepo(ctx, dir); err != nil {
		return false, err
	}

	// Interpret the path to decide how to clone and what event to simulate:
	if parsed.prNumber > 0 {
		// Pull request - fetch PR head and simulate `pull_request.reopened` to recreate
		return parsed.clonePullRequest(ctx, gh, dir)
	}

	// Fetch default branch and simulate `schedule` to reopen all
	return parsed.cloneEvent(ctx, dir)
}

const workflowDir = ".github/workflows"

func loadConfiguration(ctx context.Context, gh *github.Client, parsed *parsedTarget, environment interface{}) error {
	_, directoryContent, _, err := gh.Repositories.GetContents(ctx, parsed.owner, parsed.repo, workflowDir, &github.RepositoryContentGetOptions{})
	if err != nil {
		return err
	}

	for _, f := range directoryContent {
		file, _, _, err := gh.Repositories.GetContents(ctx, parsed.owner, parsed.repo, path.Join(workflowDir, f.GetName()), &github.RepositoryContentGetOptions{})
		if err != nil {
			return err
		}
		content, err := file.GetContent()
		if err != nil {
			return err
		}

		data := map[string]interface{}{}
		if err := yaml.Unmarshal([]byte(content), &data); err != nil {
			return err
		}

		jobs, ok := data["jobs"].(map[string]interface{})
		if !ok {
			continue
		}
		for jobName, jobDetails := range jobs {
			jd := jobDetails.(map[string]interface{})
			steps, ok := jd["steps"].([]interface{})
			if !ok {
				continue
			}

			for i, stepDetails := range steps {
				step := stepDetails.(map[string]interface{})
				uses, ok := step["uses"].(string)
				if !ok {
					continue
				}
				if !strings.HasPrefix(uses, "thepwagner/action-update") {
					continue
				}
				with, ok := step["with"].(map[string]interface{})
				if !ok {
					continue
				}
				logrus.WithFields(logrus.Fields{
					"filename": f.GetName(),
					"job":      jobName,
					"step":     i,
				}).Info("loaded configuration from workflow")

				for k, v := range with {
					key := fmt.Sprintf("INPUT_%s", strings.ToUpper(k))
					value := fmt.Sprintf("%v", v)
					if strings.Contains(value, "${{") {
						continue
					}
					logrus.WithFields(logrus.Fields{
						"key":   key,
						"value": value,
					}).Debug("setting value from workflow input")
					_ = os.Setenv(key, value)
				}
				return env.Parse(environment)
			}
		}
	}

	logrus.Info("could not load configuration from repo")
	return nil
}

type parsedTarget struct {
	owner, repo string
	prNumber    int
}

func parseTargetURL(target string) (*parsedTarget, error) {
	targetURL, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("parsing target URL: %w", err)
	}
	if targetURL.Host != "github.com" {
		return nil, fmt.Errorf("unsupported host")
	}
	pathParts := strings.Split(targetURL.Path, "/")
	if len(pathParts) < 3 {
		return nil, fmt.Errorf("could not parse repo from path")
	}

	t := &parsedTarget{
		owner: pathParts[1],
		repo:  pathParts[2],
	}
	if len(pathParts) == 5 && pathParts[3] == "pull" {
		t.prNumber, err = strconv.Atoi(pathParts[4])
		if err != nil {
			return nil, fmt.Errorf("parsing PR number: %w", err)
		}
	}
	return t, nil
}

func (p *parsedTarget) initRepo(ctx context.Context, dir string) error {
	if err := cmd.CommandExecute(ctx, dir, "git", "init", "."); err != nil {
		return fmt.Errorf("git init: %w", err)
	}
	remoteURL := fmt.Sprintf("https://github.com/%s/%s", p.owner, p.repo)
	if err := cmd.CommandExecute(ctx, dir, "git", "remote", "add", gitrepo.RemoteName, remoteURL); err != nil {
		return fmt.Errorf("git remote add: %w", err)
	}
	return nil
}

func (p *parsedTarget) clonePullRequest(ctx context.Context, gh *github.Client, dir string) (bool, error) {
	// pull request - fetch the pr HEAD and simulate a "reopened" event
	pr, _, err := gh.PullRequests.Get(ctx, p.owner, p.repo, p.prNumber)
	if err != nil {
		return false, fmt.Errorf("getting PR: %w", err)
	}

	remoteRef := fmt.Sprintf("refs/remotes/pull/%d/merge", p.prNumber)
	refSpec := fmt.Sprintf("+%s:%s", pr.GetMergeCommitSHA(), remoteRef)
	if err := cmd.CommandExecute(ctx, dir, "git", "-c", "protocol.version=2", "fetch", "--no-tags",
		"--prune", "--progress", "--no-recurse-submodules", "--depth=1", gitrepo.RemoteName, refSpec); err != nil {
		return false, fmt.Errorf("git fetch: %w", err)
	}
	if err := cmd.CommandExecute(ctx, dir, "git", "checkout", "--progress", "--force", remoteRef); err != nil {
		return false, fmt.Errorf("git fetch: %w", err)
	}

	tmpEvt, err := tmpEventFile(&github.PullRequestEvent{
		Action:      github.String("reopened"),
		PullRequest: pr,
	})
	if err != nil {
		return false, fmt.Errorf("creating temp event file: %w", err)
	}
	_ = os.Setenv("GITHUB_EVENT_NAME", "pull_request")
	_ = os.Setenv("GITHUB_EVENT_PATH", tmpEvt)
	return true, nil
}

func (p *parsedTarget) cloneEvent(ctx context.Context, dir string) (bool, error) {
	branchName := viper.GetString(cfgBranchName)
	remoteRef := path.Join("refs/remotes/origin", branchName)
	refSpec := fmt.Sprintf("+:%s", remoteRef)
	if err := cmd.CommandExecute(ctx, dir, "git", "-c", "protocol.version=2", "fetch", "--no-tags",
		"--prune", "--progress", "--no-recurse-submodules", "--depth=1", gitrepo.RemoteName, refSpec); err != nil {
		return false, fmt.Errorf("git fetch: %w", err)
	}
	if err := cmd.CommandExecute(ctx, dir, "git", "checkout", "--progress", "--force", "-B", branchName, remoteRef); err != nil {
		return false, fmt.Errorf("git fetch: %w", err)
	}

	_ = os.Setenv("GITHUB_EVENT_NAME", "schedule")
	return true, nil
}

func tmpEventFile(evt interface{}) (string, error) {
	f, err := ioutil.TempFile("", "action-update-go-event-*")
	if err != nil {
		return "", err
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(evt); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
