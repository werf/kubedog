package main

import (
	"github.com/flant/kubedog/pkg/kube"
	"github.com/flant/kubedog/pkg/trackers/rollout/multitrack"
)

func main() {
	err := kube.Init(kube.InitOptions{})
	if err != nil {
		panic(err.Error())
	}

	err = multitrack.Multitrack(kube.Kubernetes, multitrack.MultitrackSpecs{
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
		panic(err.Error())
	}

	// err = rollout.TrackJobTillDone("helo", "", kube.Kubernetes, tracker.Options{})
	// if err != nil {
	// 	panic(err.Error())
	// }
}
