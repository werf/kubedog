package tracker

import (
	"fmt"

	extensions "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/flant/kubedog/pkg/utils"
)

type ReplicaSet struct {
	Name  string
	IsNew bool
}

// TODO add containers!
type ReplicaSetPod struct {
	ReplicaSet ReplicaSet
	Name       string
}

type ReplicaSetPodLogChunk struct {
	*PodLogChunk
	ReplicaSet ReplicaSet
}

type ReplicaSetPodError struct {
	PodError
	ReplicaSet ReplicaSet
}

// ReplicaSetInformer monitor ReplicaSet events to use with controllers (Deployment, StatefulSet, DaemonSet)
type ReplicaSetInformer struct {
	Tracker
	Controller         utils.ControllerMetadata
	ReplicaSetAdded    chan *extensions.ReplicaSet
	ReplicaSetModified chan *extensions.ReplicaSet
	ReplicaSetDeleted  chan *extensions.ReplicaSet
	Errors             chan error
}

func NewReplicaSetInformer(tracker Tracker, controller utils.ControllerMetadata) *ReplicaSetInformer {
	if debug() {
		fmt.Printf("> NewReplicaSetInformer\n")
	}
	return &ReplicaSetInformer{
		Tracker: Tracker{
			Kube:             tracker.Kube,
			Namespace:        tracker.Namespace,
			FullResourceName: tracker.FullResourceName,
			Context:          tracker.Context,
			ContextCancel:    tracker.ContextCancel,
		},
		Controller:         controller,
		ReplicaSetAdded:    make(chan *extensions.ReplicaSet, 1),
		ReplicaSetModified: make(chan *extensions.ReplicaSet, 1),
		ReplicaSetDeleted:  make(chan *extensions.ReplicaSet, 1),
		Errors:             make(chan error, 0),
	}
}

func (r *ReplicaSetInformer) WithChannels(added chan *extensions.ReplicaSet,
	modified chan *extensions.ReplicaSet,
	deleted chan *extensions.ReplicaSet,
	errors chan error) *ReplicaSetInformer {
	r.ReplicaSetAdded = added
	r.ReplicaSetModified = modified
	r.ReplicaSetDeleted = deleted
	r.Errors = errors
	return r
}

func (r *ReplicaSetInformer) Run() {
	client := r.Kube

	selector, err := metav1.LabelSelectorAsSelector(r.Controller.LabelSelector())
	if err != nil {
		// TODO rescue this error!
		return
	}

	tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
		options.LabelSelector = selector.String()
		return options
	}
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return client.ExtensionsV1beta1().ReplicaSets(r.Namespace).List(tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.ExtensionsV1beta1().ReplicaSets(r.Namespace).Watch(tweakListOptions(options))
		},
	}

	go func() {
		_, err := watchtools.UntilWithSync(r.Context, lw, &extensions.ReplicaSet{}, nil, func(e watch.Event) (bool, error) {
			if debug() {
				fmt.Printf("    %s replica set event: %#v\n", r.FullResourceName, e.Type)
			}

			var object *extensions.ReplicaSet

			if e.Type != watch.Error {
				var ok bool
				object, ok = e.Object.(*extensions.ReplicaSet)
				if !ok {
					return true, fmt.Errorf("extensions.ReplicaSet informer for %s got unexpected object %T", r.FullResourceName, e.Object)
				}
			}

			switch e.Type {
			case watch.Added:
				r.ReplicaSetAdded <- object
			case watch.Modified:
				r.ReplicaSetModified <- object
			case watch.Deleted:
				r.ReplicaSetDeleted <- object
			}

			return false, nil
		})

		if err != nil {
			r.Errors <- err
		}

		if debug() {
			fmt.Printf("      %s replicaSets informer DONE\n", r.FullResourceName)
		}
	}()

	return
}
