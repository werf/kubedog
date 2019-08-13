# kubedog

Kubedog is a library that allows watching and following kubernetes resources in CI/CD deploy pipelines.

This library is used in the [werf CI/CD tool](https://github.com/flant/werf) to track resources during deploy process.

**NOTE** Cli utility is intent to be used to observe library abilities and for debug purposes. Further improvement of cli is not planned. Cli is only a minimal viable interface to access library functions.

# Installation

## Install library

```
go get github.com/flant/kubedog
```

## Install CLI

The latest release can be downloaded from [this page](https://bintray.com/flant/kubedog/kubedog/_latestVersion).

### MacOS

```bash
curl -L https://dl.bintray.com/flant/kubedog/v0.3.2/kubedog-darwin-amd64-v0.3.2 -o /tmp/kubedog
chmod +x /tmp/kubedog
sudo mv /tmp/kubedog /usr/local/bin/kubedog
```

### Linux

```bash
curl -L https://dl.bintray.com/flant/kubedog/v0.3.2/kubedog-linux-amd64-v0.3.2 -o /tmp/kubedog
chmod +x /tmp/kubedog
sudo mv /tmp/kubedog /usr/local/bin/kubedog
```

### Windows

Download [kubedog.exe](https://dl.bintray.com/flant/kubedog/v0.3.2/kubedog-windows-amd64-v0.3.2.exe).

# Cli usage

**NOTE** Cli utility is intent to be used to observe library abilities and for debug purposes. Further improvement of cli is not planned. Cli is only a minimal viable interface to access library functions.

Kubedog cli utility is a tool that can be used to track what is going on with the specified resource.

There are 3 modes of resource tracking: multitrack, follow and rollout. The commands are `kubedog multitrack ...`, `kubedog follow ...` and `kubedog rollout track ...` respectively.

**DEPRECATION NOTE** Rollout and follow modes are deprecated to use. Old trackers will remain in the cli. But these trackers will not receive future support. The reason is: multitracker solves main kubedog task in the more common way.

## Multitracker cli

There is minimal viable support of multitracker in kubedog cli. To use multitracker user must pass to kubedog stdin json structure which resembles golang structure MultitrackSpecs (see [library description](#multitracker) and [sources](https://github.com/flant/kubedog/blob/master/pkg/trackers/rollout/multitrack/multitrack.go#L57) for more info), for example:

```
cat << EOF | kubedog multitrack
{
  "StatefulSets": [
    {
      "ResourceName": "mysts1",
      "Namespace": "myns"
    }
  ],
  "Deployments": [
    {
      "ResourceName": "mydeploy22",
      "Namespace": "myns"
    }
  ]
}
EOF
```

![Kubedog multitrack cli demo](https://raw.githubusercontent.com/flant/werf-demos/master/kubedog/kubedog-multitrack-cmd.gif)

Multitracker can be used in CI/CD deploy pipeline to make sure that some set of resources is ready or done before proceeding deploy process. In this mode kubedog gives a reasonable error message and ensures to exit with non-zero error code if something wrong with the specified resources. By default kubedog will fail fast giving user fast feedback about failed resources.

## More multitracker demos

![Demo 1](https://raw.githubusercontent.com/flant/werf-demos/master/kubedog/kubedog-multitrack-with-output-prefix.gif)

### Werf demos

[Werf](https://github.com/flant/werf) makes use of kubedog multitracker under the hood, so the output is the same as `kubedog multitrack ...` cli invocation, the modes configured using annotations are passed directly to the `MultitrackSpec` corresponding options:

![Demo 2](https://raw.githubusercontent.com/flant/werf-demos/master/deploy/werf-new-track-modes-1.gif)

![Demo 3](https://raw.githubusercontent.com/flant/werf-demos/master/deploy/werf-new-track-modes-2.gif)

![Demo 4](https://raw.githubusercontent.com/flant/werf-demos/master/deploy/werf-new-track-modes-3.gif)

## Rollout and follow cli (DEPRECATED)

In the rollout and follow modes kubedog will print to the screen logs and other information related to the specified resource. Kubedog aimed to give enough information about resource for the end user, so that no additional kubectl invocation needed to debug and see what is going on with the resource. All data related to the resource will be unified into a single stream of events.

Follow mode can be used as simple `tail -f` tool, but for kubernetes resources. Users of rollout tracker encouraged to migrate to multitracker, because old rollout-trackers will not receive future support.

Rollout mode can be used in CI/CD deploy pipeline to make sure that some resource is ready or done before proceeding deploy process. In this mode kubedog gives a reasonable error message and ensures to exit with non-zero error code if something wrong with the specified resource.

# Library usage: trackers

Kubedog has a low level public methods to get stream of events and logs. These methods can be used to implement different tracking algorithms or `trackers` (more details in [Custom trackers](#library-usage-custom-trackers) section).

Also, kubedog provides a ready-to-go trackers that implement most used tracking logic. These trackers are used by kubedog CLI itself.

Tracker aimed to give enough information about resource for the end user, so that no additional kubectl invocation needed to debug and see what is going on with the resource. All data related to the resource will be combined into a single stream of events.

Trackers are using kubernetes informers under the hood, which is a reliable primitive from kubernetes library, instead of using raw watch kubernetes api.

Currently there is a single main tracker available: [multitracker](#multitracker). Old [follow](#follow-tracker) and [rollout](#rollout-tracker) trackers are deprecated to use, because multitracker is a more common way to solve the same problems, that follow and rollout trackers aimed to solve.

## Multitracker

Multitracker allows tracking multiple resources of multiple kinds at the same time. Multitracker combines all data from all resources into single stream of messages. Also this tracker gives periodical status reports with info about all resources, that are being tracked.

Multitracker is a **rollout style tracker** (see [follow tracker](https://github.com/flant/kubedog#follow-tracker) and [rollout tracker](https://github.com/flant/kubedog#rollout-tracker)), so it runs until all specified resources reach a readiness state.

Import package:

```
import "github.com/flant/kubedog/pkg/trackers/rollout/multitrack"
```

Available functions:

```
Multitrack(
  kube kubernetes.Interface,
  specs MultitrackSpecs,
  opts MultitrackOptions
) error
```

- `kube` — configured Kubernetes client (see [kube.go](pkg/kube/kube.go#L36))
- `specs` — description of objects to track
- `opts` — multitrack specific options

`specs` argument describes what `Deployments`, `StatefulSets`, `DaemonSets` and `Jobs` to track using `MultitrackSpec` structure. `MultitrackSpec` allows to specify different modes of tracking per-resource (such as allowed failures count, log regexp and other):

```
type MultitrackSpecs struct {
	Deployments  []MultitrackSpec
	StatefulSets []MultitrackSpec
	DaemonSets   []MultitrackSpec
	Jobs         []MultitrackSpec
}

type MultitrackSpec struct {
	ResourceName string
	Namespace    string

	TrackTerminationMode    TrackTerminationMode
	FailMode                FailMode
	AllowFailuresCount      *int
	FailureThresholdSeconds *int

	LogRegex                *regexp.Regexp
	LogRegexByContainerName map[string]*regexp.Regexp

	SkipLogs                  bool
	SkipLogsForContainers     []string
	ShowLogsOnlyForContainers []string

	ShowServiceMessages bool
}
```

`Multitrack` function is a blocking call, which will return on error or when all resources are ready accordingly to the specified specs options.

## Follow tracker (DEPRECATED)

Follow tracker simply prints to the screen all resource related events. Follow tracker can be used as simple `tail -f` tool, but for kubernetes resources. This tracker used to implement follow mode of the CLI.

Import package:

```
import "github.com/flant/kubedog/pkg/trackers/follow"
```

Available functions:

```
TrackPod(
    name,
    namespace string,
    kube kubernetes.Interface, opts tracker.Options
) error

TrackJob(
    name,
    namespace string,
    kube kubernetes.Interface, opts tracker.Options
) error

TrackDeployment(
    name,
    namespace string,
    kube kubernetes.Interface,
    opts tracker.Options
) error

TrackDaemonSet(
    name,
    namespace string,
    kube kubernetes.Interface,
    opts tracker.Options
) error

TrackStatefulSet(
    name,
    namespace string,
    kube kubernetes.Interface,
    opts tracker.Options
) error
```

- `name` — name of the resource
- `namespace` — namespace of the resource
- `kube` — configured Kubernetes client (see [kube.go](pkg/kube/kube.go#L36))
- `opts` — tracker options (context, timeout, starting time for logs)

These functions run until specified resource is terminated. Error is returned only in exceptional situation or on timeout.

> Note: Objects’ related Kubernetes errors such as `CrashLoopBackOff`, `ErrImagePull` and others are considered as events. They are printed to the screen and error is not returned in these cases.

## Rollout tracker (DEPRECATED)

Rollout tracker is aimed to be used in the tools for the CI/CD deploy pipeline to make sure that some resource is ready or done before proceeding deploy process. These trackers are used to implement rollout mode of the CLI.

The rollout tracker checks that resource is ready or done (in the case of the Job) before terminating. Resource logs and errors are printed to the screen.

Important differences from the [follow tracker](#follow-tracker) are:

* Function may return error when resource is not ready or some userspace error occured in the resource (such as `CrashLoopBackOff` error in Pod).
* If function returns `nil`, then it is safe to assume, that resource is ready or done (in the case of a Job).

Import package:

```
import "github.com/flant/kubedog/pkg/trackers/rollout"
```

Available functions:

```
TrackPod(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error
TrackJob(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error
TrackDeployment(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error
TrackDaemonSet(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error
TrackStatefulSet(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error
```

- `name` — name of the resource
- `namespace` — namespace of the resource
- `kube` — configured Kubernetes client (see [kube.go](pkg/kube/kube.go#L36))
- `opts` — tracker options (context, timeout, starting time for logs)

## Examples of using trackers

### Track mutiple resources

**Task**: track deployments `tiller-deploy`, `coredns` and job `myjob` simultaneously until all the resources become ready.

**Solution**: Multitrack tracker can be used to track multiple resources at once:

```
package main

import (
  "fmt"
  "os"

  "github.com/flant/kubedog/pkg/kube"
  "github.com/flant/kubedog/pkg/tracker"
  "github.com/flant/kubedog/pkg/trackers/rollout/multitrack"
)

func main() {
  _ = kube.Init(kube.InitOptions{})

  err := multitrack.Multitrack(
    kube.Kubernetes,
    multitrack.MultitrackSpecs{
      Deployments: []multitrack.MultitrackSpec{
        multitrack.MultitrackSpec{
          ResourceName: "tiller-deploy",
          Namespace: "kube-system"},
        multitrack.MultitrackSpec{
          ResourceName: "coredns",
          Namespace: "kube-system"},
      },
      Jobs: []multitrack.MultitrackSpec{
        multitrack.MultitrackSpec{
          ResourceName: "myjob",
          Namespace: "myns"},
      },
    },
    multitrack.MultitrackOptions{}
  )

  if err != nil {
    fmt.Fprintf(
      os.Stderr,
      "ERROR: resources are not reached ready state: %s",
      err
    )
    os.Exit(1)
  }
}
```

# Library usage: Custom trackers

Kubedog defines a `Feed` interface of callbacks that executed on events, so you need to implement callbacks and a `Track` method of this interface to get stream of events and logs.

Kubedog provides convenient helpers for different kind of resources with ready `Track` methods. To create a custom tracker for pod, deployment, statefulset, daemonset or job, one could create feed object with a call to a `NewFeed` function and define callbacks. This tracker can be started with a call of a `Track` method. `NewFeed` helpers available in these packages:

```
import "github.com/flant/kubedog/pkg/tracker/pod"
import "github.com/flant/kubedog/pkg/tracker/deployment"
import "github.com/flant/kubedog/pkg/tracker/statefulset"
import "github.com/flant/kubedog/pkg/tracker/daemonset"
import "github.com/flant/kubedog/pkg/tracker/job"
```

Callback for different resources are slightly differs, for example, `Feed` interface for pod looks like:

```
type Feed interface {
  // Callbacks
  OnAdded(func() error)
  OnSucceeded(func() error)
  OnFailed(func(reason string) error)
  OnEventMsg(func(msg string) error)
  OnReady(func() error)
  OnContainerLogChunk(func(*ContainerLogChunk) error)
  OnContainerError(func(ContainerError) error)
  OnStatusReport(func(PodStatus) error)

  GetStatus() PodStatus

  Track(
    podName,
    namespace string,
    kube kubernetes.Interface,
    opts tracker.Options
  ) error
}
```

`Track` method starts informers and runs callbacks on events. Each  callback may return error with predefined type to interrupt the tracking process with error. An error of type `tracker.StopTrack` can be returned to interrupt the tracking process without error.

Method `GetStatus` can be called by any callback to get a status of tracked resource.

## Example of custom tracker

For example, let’s create a simple tracker that prints events and status from pod `mypod` and exits in case of failure or ready state:

```
package main

import (
  "fmt"
  "os"

  "github.com/flant/kubedog/pkg/kube"
  "github.com/flant/kubedog/pkg/tracker"
  "github.com/flant/kubedog/pkg/tracker/pod"
)

func main() {
  _ = kube.Init(kube.InitOptions{})

  feed := pod.NewFeed()

  feed.OnEventMsg(func(msg string) error {
    fmt.Printf("Pod event: %s\n", msg)
    return nil
  })
  feed.OnReady(func() error {
    fmt.Printf("Pod ready!\n")
    fmt.Printf("Pod status: %#v\n", feed.GetStatus())
    return tracker.StopTrack
  })
  feed.OnFailed(func(reason string) error {
    return fmt.Errorf("pod failed: %s", reason)
  })
  feed.OnStatusReport(func(status pod.PodStatus) error {
    fmt.Printf("Pod status: %#v\n", status)
    return nil
  })

  err := feed.Track(
    "mypod",
    "mynamespace",
    kube.Interface,
    tracker.Options{}
  )
  if err != nil {
    fmt.Fprintf(os.Stderr, "ERROR: po/mypod tracker failed: %s", err)
    os.Exit(1)
  }
}
```

# Support

You can ask for support in [werf cncf slack channel](https://cloud-native.slack.com/messages/CHY2THYUU), [werf chat in Telegram](https://t.me/werf_ru) or simply create an issue.
