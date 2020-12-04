package elimination

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/werf/logboek"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

type EliminationTrackerSpec struct {
	ResourceName         string
	Namespace            string
	GroupVersionResource schema.GroupVersionResource
}

func (spec *EliminationTrackerSpec) String() string {
	var groupVersionParts []string
	if spec.GroupVersionResource.Group != "" {
		groupVersionParts = append(groupVersionParts, spec.GroupVersionResource.Group)
	}
	if spec.GroupVersionResource.Version != "" {
		groupVersionParts = append(groupVersionParts, spec.GroupVersionResource.Version)
	}
	return fmt.Sprintf("ns/%s %s %s/%s", spec.Namespace, strings.Join(groupVersionParts, "/"), spec.GroupVersionResource.Resource, spec.ResourceName)
}

type EliminationTrackerOptions struct {
	Timeout              time.Duration
	StatusProgressPeriod time.Duration
}

func TrackUntilEliminated(ctx context.Context, kubeDynamicClient dynamic.Interface, specs []*EliminationTrackerSpec, opts EliminationTrackerOptions) error {
	if opts.Timeout != 0 {
		_ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
		ctx = _ctx
	} else {
		_ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		ctx = _ctx
	}

	var outputMux sync.Mutex
	errorChan := make(chan error, 0)

	for _, spec := range specs {
		tracker := NewEliminationTracker(kubeDynamicClient, spec)

		go func() {
			errorChan <- tracker.Track(ctx, opts)
		}()

		go func() {
			for {
				select {
				case resourceStatus := <-tracker.ResourceStatus:
					func() {
						outputMux.Lock()
						defer outputMux.Unlock()

						logboek.Default().LogF("Resource status:\n%s\n---\n", resourceStatus.ManifestJson)
						logboek.Default().LogOptionalLn()
					}()
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	var errors []error
	var pendingJobs = len(specs)
	for {
		select {
		case err := <-errorChan:
			pendingJobs--
			if err != nil {
				errors = append(errors, err)
			}

			if pendingJobs == 0 {
				if len(errors) > 0 {
					errorMsgs := []string{}
					for _, err := range errors {
						errorMsgs = append(errorMsgs, err.Error())
					}
					return fmt.Errorf("%s", strings.Join(errorMsgs, "; "))
				}
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type ResourceStatus struct {
	Spec         *EliminationTrackerSpec
	ManifestJson []byte
}

type EliminationTracker struct {
	KubeDynamicClient dynamic.Interface
	Spec              *EliminationTrackerSpec

	ResourceStatus chan ResourceStatus
}

func NewEliminationTracker(kubeDynamicClient dynamic.Interface, spec *EliminationTrackerSpec) *EliminationTracker {
	return &EliminationTracker{
		KubeDynamicClient: kubeDynamicClient,
		Spec:              spec,
		ResourceStatus:    make(chan ResourceStatus, 10),
	}
}

func (tracker *EliminationTracker) Track(ctx context.Context, opts EliminationTrackerOptions) error {
	list, err := tracker.KubeDynamicClient.Resource(tracker.Spec.GroupVersionResource).Namespace(tracker.Spec.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("unable to get list of %q in the namespace %q: %s", tracker.Spec.GroupVersionResource, tracker.Spec.Namespace, err)
	}

	var found bool
	for i := range list.Items {
		logboek.Context(context.Background()).Debug().LogF("Check resource %q %q against %q!\n", tracker.Spec.GroupVersionResource, list.Items[i].GetName(), tracker.Spec.ResourceName)
		if list.Items[i].GetName() == tracker.Spec.ResourceName {
			logboek.Context(context.Background()).Debug().LogF("Found resource %q %q!\n", tracker.Spec.GroupVersionResource, tracker.Spec.ResourceName)
			found = true
		}
	}
	if !found {
		return nil
	}

	informerFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(tracker.KubeDynamicClient, 0, tracker.Spec.Namespace, nil)
	informer := informerFactory.ForResource(tracker.Spec.GroupVersionResource)

	stopCh := make(chan struct{})

	handlers := cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(obj interface{}) {
			u, ok := obj.(*unstructured.Unstructured)
			if !ok {
				return
			}

			if u.GetName() == tracker.Spec.ResourceName {
				logboek.Context(context.Background()).Debug().LogF("Stopping informer for %q %q\n", tracker.Spec.GroupVersionResource, tracker.Spec.ResourceName)
				close(stopCh)
			}
		},
	}

	informer.Informer().AddEventHandler(handlers)

	logboek.Context(context.Background()).Debug().LogF("Starting informer for %q %q\n", tracker.Spec.GroupVersionResource, tracker.Spec.ResourceName)

	informer.Informer().Run(stopCh)

	return nil
}
