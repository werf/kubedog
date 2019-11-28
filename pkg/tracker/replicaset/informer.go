package replicaset

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/tracker/debug"
	"github.com/flant/kubedog/pkg/tracker/pod"
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
	*pod.PodLogChunk
	ReplicaSet ReplicaSet
}

type ReplicaSetPodError struct {
	pod.PodError
	ReplicaSet ReplicaSet
}

// ReplicaSetInformer monitor ReplicaSet events to use with controllers (Deployment, StatefulSet, DaemonSet)
type ReplicaSetInformer struct {
	tracker.Tracker
	Controller         utils.ControllerMetadata
	ReplicaSetAdded    chan *appsv1.ReplicaSet
	ReplicaSetModified chan *appsv1.ReplicaSet
	ReplicaSetDeleted  chan *appsv1.ReplicaSet
	Errors             chan error
}

func NewReplicaSetInformer(trk *tracker.Tracker, controller utils.ControllerMetadata) *ReplicaSetInformer {
	if debug.Debug() {
		fmt.Printf("> NewReplicaSetInformer\n")
	}
	return &ReplicaSetInformer{
		Tracker: tracker.Tracker{
			Kube:             trk.Kube,
			Namespace:        trk.Namespace,
			FullResourceName: trk.FullResourceName,
			Context:          trk.Context,
		},
		Controller:         controller,
		ReplicaSetAdded:    make(chan *appsv1.ReplicaSet, 1),
		ReplicaSetModified: make(chan *appsv1.ReplicaSet, 1),
		ReplicaSetDeleted:  make(chan *appsv1.ReplicaSet, 1),
		Errors:             make(chan error, 0),
	}
}

func (r *ReplicaSetInformer) WithChannels(added chan *appsv1.ReplicaSet,
	modified chan *appsv1.ReplicaSet,
	deleted chan *appsv1.ReplicaSet,
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
			return client.AppsV1().ReplicaSets(r.Namespace).List(tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.AppsV1().ReplicaSets(r.Namespace).Watch(tweakListOptions(options))
		},
	}

	go func() {
		_, err := watchtools.UntilWithSync(r.Context, lw, &appsv1.ReplicaSet{}, nil, func(e watch.Event) (bool, error) {
			if debug.Debug() {
				fmt.Printf("    %s replica set event: %#v\n", r.FullResourceName, e.Type)
			}

			var object *appsv1.ReplicaSet

			if e.Type != watch.Error {
				var ok bool
				object, ok = e.Object.(*appsv1.ReplicaSet)
				if !ok {
					return true, fmt.Errorf("appsv1.ReplicaSet informer for %s got unexpected object %T", r.FullResourceName, e.Object)
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

		if err := tracker.AdaptInformerError(err); err != nil {
			r.Errors <- err
		}

		if debug.Debug() {
			fmt.Printf("      %s replicaSets informer DONE\n", r.FullResourceName)
		}
	}()

	return
}
