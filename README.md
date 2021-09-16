<p align="center">
  <img src="doc/kubedog-logo.svg?sanitize=true" style="max-height:100%;" height="100">
</p>

# kubedog

Kubedog is a library to watch and follow Kubernetes resources in CI/CD deploy pipelines.

This library is used in the [werf CI/CD tool](https://github.com/werf/werf) to track resources during deploy process.

**NOTE:** Kubedog also includes a CLI, however it provides a *minimal* interface to access library functions. CLI was created to check library features and for debug purposes. Currently, we have no plans on further improvement of CLI.

## Table of Contents
- [Install kubedog CLI](#install-kubedog-cli)
   * [With trdl (recommended)](#with-trdl-recommended) 
     - [Linux](#linux-bash)
     - [macOS](#macos-zsh)
     - [Windows](#windows-powershell)
   * [Alternative binary installation](#alternative-binary-installation)
     - [Linux](#linux)
     - [macOS](#macos)
     - [Windows](#windows)
- [Usage](#usage)
- [Community](#community)
- [License](#license)

## Install `kubedog` CLI

### With `trdl` (recommended)

#### Linux (Bash)

Setup `trdl`, which will install and update `kubedog`:
```shell
# Add ~/bin to the PATH.
echo 'export PATH=$HOME/bin:$PATH' >> ~/.bash_profile
export PATH="$HOME/bin:$PATH"

# Install trdl.
curl -L "https://tuf.trdl.dev/targets/releases/0.1.3/linux-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')/bin/trdl" -o /tmp/trdl
mkdir -p ~/bin
install /tmp/trdl ~/bin/trdl
```

Add `kubedog` repo to `trdl`:
```shell
trdl add kubedog https://tuf.kubedog.werf.io 1 2cc56abdc649a9699074097ba60206f1299e43b320d6170c40eab552dcb940d9e813a8abf5893ff391d71f0a84b39111ffa6403a3e038b81634a40d29674a531
```

Install and activate `kubedog`:
```shell
# Activate kubedog binary in the current shell.
source $(trdl use kubedog 0 stable)

# Check whether kubedog is available now.
kubedog version

# Activate kubedog binary automatically during shell initializations.
echo 'source $(trdl use kubedog 0 stable)' >> ~/.bashrc
```

#### macOS (Zsh)

Setup `trdl`, which will install and update `kubedog`:
```shell
# Add ~/bin to the PATH.
echo 'export PATH=$HOME/bin:$PATH' >> ~/.zprofile
export PATH="$HOME/bin:$PATH"

# Install trdl.
curl -L "https://tuf.trdl.dev/targets/releases/0.1.3/darwin-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')/bin/trdl" -o /tmp/trdl
mkdir -p ~/bin
install /tmp/trdl ~/bin/trdl
```

Add `kubedog` repo to `trdl`:
```shell
trdl add kubedog https://tuf.kubedog.werf.io 1 2cc56abdc649a9699074097ba60206f1299e43b320d6170c40eab552dcb940d9e813a8abf5893ff391d71f0a84b39111ffa6403a3e038b81634a40d29674a531
```

Install and activate `kubedog`:
```shell
# Activate kubedog binary in the current shell.
source $(trdl use kubedog 0 stable)

# Check whether kubedog is available now.
kubedog version

# Activate kubedog binary automatically during shell initializations.
echo 'source $(trdl use kubedog 0 stable)' >> ~/.zshrc
```

#### Windows (PowerShell)

Setup `trdl`, which will install and update `kubedog`:
```powershell
# Add %USERPROFILE%\bin to the PATH.
[Environment]::SetEnvironmentVariable("Path", "$env:USERPROFILE\bin" + [Environment]::GetEnvironmentVariable("Path", "User"), "User")
$env:Path = "$env:USERPROFILE\bin;$env:Path"

# Install trdl.
mkdir -Force "$env:USERPROFILE\bin"
Invoke-WebRequest -Uri "https://tuf.trdl.dev/targets/releases/0.1.3/windows-amd64/bin/trdl.exe" -OutFile "$env:USERPROFILE\bin\trdl.exe"
```

Add `kubedog` repo to `trdl`:
```powershell
trdl add kubedog https://tuf.kubedog.werf.io 1 2cc56abdc649a9699074097ba60206f1299e43b320d6170c40eab552dcb940d9e813a8abf5893ff391d71f0a84b39111ffa6403a3e038b81634a40d29674a531
```

Install and activate `kubedog`:
```powershell
# Activate kubedog binary in the current shell.
. $(trdl use kubedog 0 stable)

# Check whether kubedog is available now.
kubedog version
```

To allow automatic activation of `kubedog` binary for new PowerShell instances you'll need to allow execution of scripts created locally. Run in the PowerShell under Administrator:
```powershell
Set-ExecutionPolicy RemoteSigned -Scope CurrentUser

# Activate kubedog binary automatically during PowerSell initializations.
if (!(Test-Path "$profile")) {
  New-Item -Path "$profile" -Force
}
Add-Content -Path "$profile" -Value '. $(trdl use kubedog 0 stable)'
```

### Alternative binary installation

#### Linux

Execute in shell:
```shell
curl -L "https://tuf.kubedog.werf.io/targets/releases/0.6.1/linux-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')/bin/kubedog" -o /tmp/kubedog
install /tmp/kubedog /usr/local/bin/kubedog
```

#### macOS

Execute in shell:
```shell
curl -L "https://tuf.kubedog.werf.io/targets/releases/0.6.1/darwin-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')/bin/kubedog" -o /tmp/kubedog
install /tmp/kubedog /usr/local/bin/kubedog
```

#### Windows

Execute in PowerShell:
```powershell
# Add %USERPROFILE%\bin to the PATH.
[Environment]::SetEnvironmentVariable("Path", "$env:USERPROFILE\bin" + [Environment]::GetEnvironmentVariable("Path", "User"), "User")
$env:Path = "$env:USERPROFILE\bin;$env:Path"

# Install kubedog.
mkdir -Force "$env:USERPROFILE\bin"
Invoke-WebRequest -Uri "https://tuf.kubedog.werf.io/targets/releases/0.6.1/windows-amd64/bin/kubedog.exe" -OutFile "$env:USERPROFILE\bin\kubedog.exe"
```

## Usage

* [CLI usage](doc/usage.md#cli-usage)
* [Library usage: Multitracker](doc/usage.md#Multitracker)

## Community

Please feel free to reach us via [project's Discussions](https://github.com/werf/kubedog/discussions) and [werf's Telegram group](https://t.me/werf_io) (there's [another one in Russian](https://t.me/werf_ru) as well).

You're also welcome to follow [@werf_io](https://twitter.com/werf_io) to stay informed about all important news, articles, etc.

## License

Kubedog is an Open Source project licensed under the [Apache License](https://www.apache.org/licenses/LICENSE-2.0).
