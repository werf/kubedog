<p align="center">
  <img src="doc/kubedog-logo.svg?sanitize=true" style="max-height:100%;" height="100">
</p>

# kubedog

Kubedog is a library to watch and follow Kubernetes resources in CI/CD deploy pipelines.

This library is used in the [werf CI/CD tool](https://github.com/werf/werf) to track resources during deploy process.

**NOTE:** Kubedog also includes a CLI, however it provides a *minimal* interface to access library functions. CLI was created to check library features and for debug purposes. Currently, we have no plans on further improvement of CLI.

# Installation

## Install library

```
go get github.com/werf/kubedog
```

## Install CLI

The latest release can be downloaded from [this page](https://bintray.com/flant/kubedog/kubedog/_latestVersion).

### MacOS

```bash
curl -L https://dl.bintray.com/flant/kubedog/v0.3.4/kubedog-darwin-amd64-v0.3.4 -o /tmp/kubedog
chmod +x /tmp/kubedog
sudo mv /tmp/kubedog /usr/local/bin/kubedog
```

### Linux

```bash
curl -L https://dl.bintray.com/flant/kubedog/v0.3.4/kubedog-linux-amd64-v0.3.4 -o /tmp/kubedog
chmod +x /tmp/kubedog
sudo mv /tmp/kubedog /usr/local/bin/kubedog
```

### Windows

Download [kubedog.exe](https://dl.bintray.com/flant/kubedog/v0.3.4/kubedog-windows-amd64-v0.3.4.exe).

# Using kubedog

* [CLI usage](doc/usage.md#cli-usage)
* [Library usage: Trackers](doc/usage.md#library-usage-trackers)
* [Library usage: Custom trackers](doc/usage.md#library-usage-custom-trackers)

# Support

You can ask for support in [werf CNCF Slack channel](https://cloud-native.slack.com/messages/CHY2THYUU), [werf chat in Telegram](https://t.me/werf_ru) or simply create an issue.

# License

Kubedog is an Open Source project licensed under the [Apache License](https://www.apache.org/licenses/LICENSE-2.0).
