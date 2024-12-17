package main

import (
	"github.com/werf/kubedog/for-werf-helm/pkg/kube"
	"github.com/werf/kubedog/for-werf-helm/pkg/trackers/rollout/multitrack"
)

func main() {
	err := kube.Init(kube.InitOptions{})
	if err != nil {
		panic(err.Error())
	}

	err = multitrack.Multitrack(kube.Kubernetes, multitrack.MultitrackSpecs{
		Deployments: []multitrack.MultitrackSpec{
			{ResourceName: "tiller-deploy", Namespace: "kube-system"},
			{ResourceName: "coredns", Namespace: "kube-system"},
		},
		Jobs: []multitrack.MultitrackSpec{
			{ResourceName: "myjob", Namespace: "myns"},
		},
	}, multitrack.MultitrackOptions{})
	if err != nil {
		panic(err.Error())
	}

	// err = rollout.TrackJobTillDone("helo", "", kube.Kubernetes, tracker.Options{})
	// if err != nil {
	// 	panic(err.Error())
	// }
}
