module github.com/thepwagner/action-update-cli

require (
	github.com/caarlos0/env/v6 v6.6.2
	github.com/ghodss/yaml v1.0.0
	github.com/google/go-github/v36 v36.0.0
	github.com/mitchellh/go-homedir v1.1.0
	github.com/sirupsen/logrus v1.8.1
	github.com/spf13/cobra v1.0.0
	github.com/spf13/viper v1.8.1
	github.com/thepwagner/action-update v0.0.41
	github.com/thepwagner/action-update-docker v0.0.8
	github.com/thepwagner/action-update-dockerurl v0.0.1
	github.com/thepwagner/action-update-go v0.0.1
)

replace (
	github.com/containerd/containerd => github.com/containerd/containerd v1.4.0
	github.com/docker/docker => github.com/moby/moby v17.12.0-ce-rc1.0.20200916142827-bd33bbf0497b+incompatible
	github.com/thepwagner/action-update => ../action-update
	github.com/thepwagner/action-update-docker => ../action-update-docker
	github.com/thepwagner/action-update-dockerurl => ../action-update-dockerurl
	github.com/thepwagner/action-update-go => ../action-update-go
)

go 1.15
