package elimination

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/werf/logboek"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
	errorChan := make(chan error)

	for _, spec := range specs {
		tracker := NewEliminationTracker(kubeDynamicClient, spec)

		go func() {
			if debug() {
				fmt.Printf("-- TrackUntilEliminated: start track %s\n", tracker.Spec.String())
			}

			err := tracker.Track(ctx, opts)

			if debug() {
				fmt.Printf("-- TrackUntilEliminated: stop track %s -> %v\n", tracker.Spec.String(), err)
			}

			errorChan <- err

			if debug() {
				fmt.Printf("-- TrackUntilEliminated: %s sent errorChan signal %v\n", tracker.Spec.String(), err)
			}
		}()

		go func() {
			for {
				select {
				case resourceStatus := <-tracker.ResourceStatus:
					func() {
						outputMux.Lock()
						defer outputMux.Unlock()

						logboek.Context(context.Background()).Default().LogF("Resource status:\n%s\n---\n", resourceStatus.ManifestJson)
						logboek.Context(context.Background()).Default().LogOptionalLn()
					}()
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	var errors []error
	pendingJobs := len(specs)
	for {
		select {
		case err := <-errorChan:
			if debug() {
				fmt.Printf("-- TrackUntilEliminated: received errorChan signal %v current pendingJobs=%d\n", err, pendingJobs)
			}

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

				if debug() {
					fmt.Printf("-- TrackUntilEliminated: no pending jobs: exiting\n")
				}

				return nil
			}
		case <-ctx.Done():
			if ctx.Err() == context.Canceled {
				return nil
			}
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

	resourceEliminated chan struct{}
	stopInformer       chan struct{}
}

func NewEliminationTracker(kubeDynamicClient dynamic.Interface, spec *EliminationTrackerSpec) *EliminationTracker {
	return &EliminationTracker{
		KubeDynamicClient:  kubeDynamicClient,
		Spec:               spec,
		ResourceStatus:     make(chan ResourceStatus, 10),
		stopInformer:       make(chan struct{}),
		resourceEliminated: make(chan struct{}),
	}
}

func (tracker *EliminationTracker) Track(ctx context.Context, opts EliminationTrackerOptions) error {
	tracker.startInformer(ctx)

	list, err := tracker.KubeDynamicClient.Resource(tracker.Spec.GroupVersionResource).Namespace(tracker.Spec.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("unable to get list of %q in the namespace %q: %s", tracker.Spec.GroupVersionResource, tracker.Spec.Namespace, err)
	}

	var found bool
	for i := range list.Items {
		if list.Items[i].GetName() == tracker.Spec.ResourceName {
			found = true
			break
		}
	}

	if found {
		if debug() {
			fmt.Printf("-- EliminationTracker: found resource %s: waiting for delete event\n", tracker.Spec.String())
		}
		<-tracker.resourceEliminated
	} else {
		if debug() {
			fmt.Printf("-- EliminationTracker: not found resource %s: exiting\n", tracker.Spec.String())
		}
		close(tracker.stopInformer)
	}

	return nil
}

func (tracker *EliminationTracker) startInformer(ctx context.Context) {
	informerFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(tracker.KubeDynamicClient, 0, tracker.Spec.Namespace, nil)
	informer := informerFactory.ForResource(tracker.Spec.GroupVersionResource)

	handlers := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if debug() {
				objDump, _ := json.MarshalIndent(obj, "", "  ")
				fmt.Printf("Added object:\n%s\n---\n", objDump)
			}
		},
		UpdateFunc: func(obj, newObj interface{}) {
			if debug() {
				objDump, _ := json.MarshalIndent(newObj, "", "  ")
				fmt.Printf("Updated object:\n%s\n---\n", objDump)
			}
		},
		DeleteFunc: func(obj interface{}) {
			if debug() {
				objDump, _ := json.MarshalIndent(obj, "", "  ")
				fmt.Printf("Deleted object:\n%s\n---\n", objDump)
			}

			u, ok := obj.(*unstructured.Unstructured)
			if !ok {
				return
			}

			if u.GetName() == tracker.Spec.ResourceName {
				if debug() {
					fmt.Printf("-- EliminationTracker: stopping informer for %s\n", tracker.Spec.String())
				}

				close(tracker.stopInformer)
				close(tracker.resourceEliminated)
			}
		},
	}

	informer.Informer().AddEventHandler(handlers)

	if debug() {
		fmt.Printf("-- EliminationTracker: starting informer for %s\n", tracker.Spec.String())
	}

	go informer.Informer().Run(tracker.stopInformer)

	for {
		if informer.Informer().HasSynced() {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if debug() {
		fmt.Printf("-- EliminationTracker: informer for %s has been synced\n", tracker.Spec.String())
	}
}

func debug() bool {
	return os.Getenv("KUBEDOG_ELIMINATION_TRACKER_DEBUG") == "1"
}
