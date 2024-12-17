package pod

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/werf/kubedog/for-werf-helm/pkg/tracker"
	"github.com/werf/kubedog/for-werf-helm/pkg/tracker/debug"
	"github.com/werf/kubedog/for-werf-helm/pkg/utils"
)

// PodsInformer monitor pod add events to use with controllers (Deployment, StatefulSet, DaemonSet)
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
		},
		Controller: controller,
		PodAdded:   make(chan *corev1.Pod, 1),
		Errors:     make(chan error),
	}
}

func (p *PodsInformer) WithChannels(added chan *corev1.Pod, errors chan error) *PodsInformer {
	p.PodAdded = added
	p.Errors = errors
	return p
}

func (p *PodsInformer) Run(ctx context.Context) {
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
			return client.CoreV1().Pods(p.Namespace).List(ctx, tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.CoreV1().Pods(p.Namespace).Watch(ctx, tweakListOptions(options))
		},
	}

	go func() {
		_, err := watchtools.UntilWithSync(ctx, lw, &corev1.Pod{}, nil, func(e watch.Event) (bool, error) {
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

			if e.Type == watch.Added {
				p.PodAdded <- object
			}

			return false, nil
		})

		if err := tracker.AdaptInformerError(err); err != nil {
			p.Errors <- fmt.Errorf("%s pods informer error: %w", p.FullResourceName, err)
		}

		if debug.Debug() {
			fmt.Printf("      %s pods informer DONE\n", p.FullResourceName)
		}
	}()
}
