<p align="center">
  <img src="doc/kubedog-logo.svg?sanitize=true" style="max-height:100%;" height="100">
</p>

# kubedog

Kubedog is a library to watch and follow Kubernetes resources in CI/CD deploy pipelines.

This library is used in the [werf CI/CD tool](https://github.com/werf/werf) to track resources during deploy process.

**NOTE:** Kubedog also includes a CLI, however it provides a *minimal* interface to access library functions. CLI was created to check library features and for debug purposes. Currently, we have no plans on further improvement of CLI.

## Table of Contents
- [Install kubedog CLI](#install-kubedog-cli)
   - [Linux/macOS](#linuxmacos)
   - [Windows](#windows-powershell)
   - [Alternative binary installation](#alternative-binary-installation)
- [Usage](#usage)
- [Community](#community)
- [License](#license)

## Install `kubedog` CLI

### Linux/macOS

[Install trdl](https://github.com/werf/trdl/releases/) to `~/bin/trdl`, which will manage `kubedog` installation and updates. Add `~/bin` to your $PATH.

Add `kubedog` repo to `trdl`:
```shell
trdl add kubedog https://tuf.kubedog.werf.io 1 2cc56abdc649a9699074097ba60206f1299e43b320d6170c40eab552dcb940d9e813a8abf5893ff391d71f0a84b39111ffa6403a3e038b81634a40d29674a531
```

To use `kubedog` on a workstation we recommend setting up `kubedog` _automatic activation_. For this the activation command should be executed for each new shell session. Often this is achieved by adding the activation command to `~/.bashrc` (for Bash), `~/.zshrc` (for Zsh) or to the one of the profile files, but this depends on the OS/shell/terminal. Refer to your shell/terminal manuals for more information.

This is the `kubedog` activation command for the current shell-session:
```shell
source "$(trdl use kubedog 0 stable)"
```

To use `kubedog` in CI prefer activating `kubedog` manually instead. For this execute the activation command in the beginning of your CI job, before calling the `kubedog` binary.

### Windows (PowerShell)

Following instructions should be executed in PowerShell.

[Install trdl](https://github.com/werf/trdl/releases/) to `<disk>:\Users\<your username>\bin\trdl`, which will manage `kubedog` installation and updates. Add `<disk>:\Users\<your username>\bin\` to your $PATH environment variable.

Add `kubedog` repo to `trdl`:
```powershell
trdl add kubedog https://tuf.kubedog.werf.io 1 2cc56abdc649a9699074097ba60206f1299e43b320d6170c40eab552dcb940d9e813a8abf5893ff391d71f0a84b39111ffa6403a3e038b81634a40d29674a531
```

To use `kubedog` on a workstation we recommend setting up `kubedog` _automatic activation_. For this the activation command should be executed for each new PowerShell session. For PowerShell this is usually achieved by adding the activation command to [$PROFILE file](https://docs.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_profiles).

This is the `kubedog` activation command for the current PowerShell-session:
```powershell
. $(trdl use kubedog 0 stable)
```

To use `kubedog` in CI prefer activating `kubedog` manually instead. For this execute the activation command in the beginning of your CI job, before calling the `kubedog` binary.

### Alternative binary installation

The recommended way to install `kubedog` is described above. Alternatively, although not recommended, you can download `kubedog` binary straight from the [GitHub Releases page](https://github.com/werf/kubedog/releases/), optionally verifying the binary with the PGP signature.

## Usage

* [CLI usage](doc/usage.md#cli-usage)
* [Library usage: Multitracker](doc/usage.md#Multitracker)

## Community

Please feel free to reach us via [project's Discussions](https://github.com/werf/kubedog/discussions) and [werf's Telegram group](https://t.me/werf_io) (there's [another one in Russian](https://t.me/werf_ru) as well).

You're also welcome to follow [@werf_io](https://twitter.com/werf_io) to stay informed about all important news, articles, etc.

## License

Kubedog is an Open Source project licensed under the [Apache License](https://www.apache.org/licenses/LICENSE-2.0).
