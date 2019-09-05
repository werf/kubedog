package pod

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/tracker/debug"
	"github.com/flant/kubedog/pkg/utils"
)

// PodInformer monitor pod add events to use with controllers (Deployment, StatefulSet, DaemonSet)
type PodsInformer struct {
	tracker.Tracker
	Controller utils.ControllerMetadata
	PodAdded   chan *corev1.Pod
	Errors     chan error
}

func NewPodsInformer(trk *tracker.Tracker, controller utils.ControllerMetadata) *PodsInformer {
	return &PodsInformer{
		Tracker: tracker.Tracker{
			Kube:             trk.Kube,
			Namespace:        trk.Namespace,
			FullResourceName: trk.FullResourceName,
			Context:          trk.Context,
		},
		Controller: controller,
		PodAdded:   make(chan *corev1.Pod, 1),
		Errors:     make(chan error, 0),
	}
}

func (p *PodsInformer) WithChannels(added chan *corev1.Pod, errors chan error) *PodsInformer {
	p.PodAdded = added
	p.Errors = errors
	return p
}

func (p *PodsInformer) Run() {
	if debug.Debug() {
		fmt.Printf("> PodsInformer.Run\n")
	}

	client := p.Kube

	selector, err := metav1.LabelSelectorAsSelector(p.Controller.LabelSelector())
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
			return client.CoreV1().Pods(p.Namespace).List(tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.CoreV1().Pods(p.Namespace).Watch(tweakListOptions(options))
		},
	}

	go func() {
		_, err := watchtools.UntilWithSync(p.Context, lw, &corev1.Pod{}, nil, func(e watch.Event) (bool, error) {
			if debug.Debug() {
				fmt.Printf("    %s pod event: %#v\n", p.FullResourceName, e.Type)
			}

			var object *corev1.Pod

			if e.Type != watch.Error {
				var ok bool
				object, ok = e.Object.(*corev1.Pod)
				if !ok {
					return true, fmt.Errorf("corev1.Pod informer for %s got unexpected object %T", p.FullResourceName, e.Object)
				}
			}

			switch e.Type {
			case watch.Added:
				p.PodAdded <- object
				// case watch.Modified:
				// 	d.resourceModified <- object
				// case watch.Deleted:
				// 	d.resourceDeleted <- object
			}

			return false, nil
		})

		if err != nil {
			p.Errors <- err
		}

		if debug.Debug() {
			fmt.Printf("      %s pods informer DONE\n", p.FullResourceName)
		}
	}()

	return
}
