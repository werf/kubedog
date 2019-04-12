# kubedog

Kubedog is a library and cli utility that allows watching and following kubernetes resources in CI/CD deploy pipelines.

This library is used in the [werf CI/CD tool](https://github.com/flant/werf) to track resources during deploy process.

## Installation

### Install library

```
go get github.com/flant/kubedog
```

### Download cli util binary

The latest release can be reached via [this page](https://bintray.com/flant/kubedog/kubedog/_latestVersion).

##### MacOS

```bash
curl -L https://dl.bintray.com/flant/kubedog/v0.1.0/kubedog-darwin-amd64-v0.1.0 -o /tmp/kubedog
chmod +x /tmp/kubedog
sudo mv /tmp/kubedog /usr/local/bin/kubedog
```

##### Linux

```bash
curl -L https://dl.bintray.com/flant/kubedog/v0.1.0/kubedog-linux-amd64-v0.1.0 -o /tmp/kubedog
chmod +x /tmp/kubedog
sudo mv /tmp/kubedog /usr/local/bin/kubedog
```

##### Windows

Download [kubedog.exe](https://dl.bintray.com/flant/kubedog/v0.1.0/kubedog-windows-amd64-v0.1.0.exe).

## Cli

Kubedog cli utility is a tool that can be used to track what is going on with the specified resource.

There are 2 modes of resource tracking: follow and rollout. The commands are `kubedog follow ...` and `kubedog rollout track ...` respectively.

In the rollout and follow modes kubedog will print to the screen logs and other information related to the specified resource. Kubedog aimed to give enough information about resource for the end user, so that no additional kubectl invocation needed to debug and see what is going on with the resource. All data related to the resource will be unified into a single stream of events.

Follow mode can be used as simple `tail -f` tool, but for kubernetes resources.

Rollout mode can be used in CI/CD deploy pipeline to make sure that some resource is ready or done before proceeding deploy process. In this mode kubedog gives a reasonable error message and ensures to exit with non-zero error code if something wrong with the specified resource.

![Deployment Rollout Animation](doc/deployment_rollout.gif)

![Deployment Follow Animation](doc/deployment_follow.gif)

See `kubedog --help` for more info.

## Trackers

The tracker is the function that tracks events for the specified resource. There may be multiple types of trackers depending on the required actions in respond to resource related events. There are 2 tracker types currently available: [follow](#follow-tracker) and [rollout](#rollout-tracker).

Tracker aimed to give enough information about resource for the end user, so that no additional kubectl invocation needed to debug and see what is going on with the resource. All data related to the resource will be unified into a single stream of events.

Trackers are using kubernetes informers under the hood, which is a reliable primitive from kubernetes library, instead of using raw watch kubernetes api.

### Follow tracker

Follow tracker simply prints to the screen all resource related events. Follow tracker can be used as simple `tail -f` tool, but for kubernetes resources.

Import package:

```
import "github.com/flant/kubedog/pkg/trackers/follow"
```

Available functions:

```
TrackPod(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error
TrackJob(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error
TrackDeployment(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error
TrackDaemonSet(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error
TrackStatefulSet(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error
```

Each function will block till specified resource is terminated. Returns error only in exceptional situation or on timeout.

Pod's related errors such as `CrashLoopBackOff`, `ErrImagePull` and other will be printed to the screen and error code will no be returned in this case.

`TrackDeployment` function will wait infinitely, until Deployment exists.

### Rollout tracker

Rollout tracker aimed to be used in the tools for the CI/CD deploy pipeline to make sure that some resource is ready or done before proceeding deploy process.

This tracker checks that resource is ready or done (in the case of the Job) before terminating. Resource logs and errors also printed to the screen.

Important differences from the [follow tracker](#follow-tracker) are:

* Function may return error code when resource is not ready or some userspace error occured in the resource (such as `CrashLoopBackOff` error in pod).
* If function returned `nil`, then it is safe to assume, that resource is ready or done (in the case of a Job).

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

### Multitracker (NEW)

Multitracker allows tracking multiple resources of multiple kinds at the same time. Multitracker combines all data from all resources into single stream of messages. Also this tracker gives periodical status reports with info about all resources, that are being tracked.

Multitracker is now available only as rollout-type tracker, so it will block until all resources reach readyness condition.

Import package:

```
import "github.com/flant/kubedog/pkg/trackers/rollout/multitrack"
```

Available functions:

```
Multitrack(kube kubernetes.Interface, specs MultitrackSpecs, opts MultitrackOptions) error
```

With `MultitrackSpecs` user can specify `Pods`, `Deployments`, `StatefulSets`, `DaemonSets` and `Jobs` to track using `MultitrackSpec` structure. `MultitrackSpec` allow to specify different modes of tracking (such as allowed failures count, log regex and other) per-resource:

```
type MultitrackSpecs struct {
	Pods         []MultitrackSpec
	Deployments  []MultitrackSpec
	StatefulSets []MultitrackSpec
	DaemonSets   []MultitrackSpec
	Jobs         []MultitrackSpec
}

type MultitrackSpec struct {
	ResourceName string
	Namespace    string

	FailMode                FailMode
	AllowFailuresCount      *int
	FailureThresholdSeconds *int

	LogWatchRegex                string
	LogWatchRegexByContainerName map[string]string
	ShowLogsUntil                DeployCondition
	SkipLogsForContainers        []string
	ShowLogsOnlyForContainers    []string
}
```

For example let's track deployments `tiller-deploy`, `coredns`, pod `etcd-minikube` and job `myjob` at the same time till all of the resources reach ready conditions:

```
package main

import (
	"fmt"
	"os"

	"github.com/flant/kubedog/pkg/tracker"
  "github.com/flant/kubedog/pkg/trackers/rollout/multitrack"
)

func main() {
  _ = kube.Init(kube.InitOptions{})

	err := multitrack.Multitrack(kube.Kubernetes, multitrack.MultitrackSpecs{
		Pods: []multitrack.MultitrackSpec{
			multitrack.MultitrackSpec{ResourceName: "etcd-minikube", Namespace: "kube-system"},
		},
		Deployments: []multitrack.MultitrackSpec{
			multitrack.MultitrackSpec{ResourceName: "tiller-deploy", Namespace: "kube-system"},
			multitrack.MultitrackSpec{ResourceName: "coredns", Namespace: "kube-system"},
		},
		Jobs: []multitrack.MultitrackSpec{
			multitrack.MultitrackSpec{ResourceName: "myjob", Namespace: "myns"},
		},
	}, multitrack.MultitrackOptions{})

	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: resources cannot reach ready condition: %s", err)
    os.Exit(1)
	}
}
```

## Use trackers examples

Track `mydeployment` Deployment in namespace `mynamespace` using rollout tracker:

```
import (
  "fmt"
  "os"

  "github.com/flant/kubedog/pkg/trackers/rollout"
  "github.com/flant/kubedog/pkg/tracker"
  "github.com/flant/kubedog/pkg/kube"
)

func main() {
  _ = kube.Init()

  err := rollout.TrackDeployment("mydeployment", "mynamespace", kube.Kubernetes, tracker.Options{Timeout: 300 * time.Second})
  if err != nil {
    fmt.Fprintf(os.Stderr, "ERROR: %s\n", err)
    os.Exit(1)
  }
}
```

Track `myjob` Job in namespace `mynamespace` using follow tracker:

```
import (
  "fmt"
  "os"

  "github.com/flant/kubedog/pkg/trackers/follow"
  "github.com/flant/kubedog/pkg/tracker"
  "github.com/flant/kubedog/pkg/kube"
)

func main() {
  _ = kube.Init()

  err = follow.TrackJob("myjob", "mynamespace", kube.Kubernetes, tracker.Options{Timeout: 300 * time.Second})
  if err != nil {
    fmt.Fprintf(os.Stderr, "ERROR: %s\n", err)
    os.Exit(1)
  }
}
```

## Custom trackers

To define a custom tracker one should create feed object with `NewFeed` function for needed tracker from one of the following packages:

```
import "github.com/flant/kubedog/pkg/tracker/pod"
import "github.com/flant/kubedog/pkg/tracker/deployment"
import "github.com/flant/kubedog/pkg/tracker/statefulset"
import "github.com/flant/kubedog/pkg/tracker/daemonset"
import "github.com/flant/kubedog/pkg/tracker/job"
```

User then can specify needed callbacks using `On*` methods of feed object. For example pod feed looks like:

```
type Feed interface {
	OnAdded(func() error)
	OnSucceeded(func() error)
	OnFailed(func(reason string) error)
	OnEventMsg(func(msg string) error)
	OnReady(func() error)
	OnContainerLogChunk(func(*ContainerLogChunk) error)
	OnContainerError(func(ContainerError) error)
	OnStatusReport(func(PodStatus) error)

	GetStatus() PodStatus
	Track(podName, namespace string, kube kubernetes.Interface, opts tracker.Options) error
}
```

Function `feed.Track` is blocking and must be called to start tracking.

Each callback may return some error to interrupt the whole tracking process with error. Also special value `tracker.StopTrack` can be returned to interrupt tracking process without error.

Function `feed.GetStatus` can be called in any callback to get structured resource status avaiable at the moment.

For example let's track pod `mypod` :

```
package main

import (
	"fmt"
  "os"

	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/tracker/pod"
)

func main() {
	kube.Init(kube.InitOptions{})

	feed := pod.NewFeed()

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

	err := feed.Track("mypod", "mynamespace", kube.Interface, tracker.Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: po/mypod tracker failed: %s", err)
    os.Exit(1)
	}
}
```

# Support

You can ask for support in [werf chat in Telegram](https://t.me/werf_ru).
