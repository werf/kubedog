package elimination

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/werf/logboek"
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
				fmt.Printf("[TrackUntilEliminated][%s] start track\n", tracker.Spec.String())
			}

			err := tracker.Track(ctx, opts)

			if debug() {
				fmt.Printf("[TrackUntilEliminated][%s] stop track -> %v\n", tracker.Spec.String(), err)
			}

			errorChan <- err

			if debug() {
				fmt.Printf("[TrackUntilEliminated][%s] sent errorChan signal %v\n", tracker.Spec.String(), err)
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
				fmt.Printf("[TrackUntilEliminated] received errorChan signal %v current pendingJobs=%d\n", err, pendingJobs)
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
					fmt.Printf("[TrackUntilEliminated] no pending jobs: exiting\n")
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
	listOpts := metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", tracker.Spec.ResourceName),
	}

	lwe := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return tracker.KubeDynamicClient.Resource(tracker.Spec.GroupVersionResource).Namespace(tracker.Spec.Namespace).List(ctx, listOpts)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return tracker.KubeDynamicClient.Resource(tracker.Spec.GroupVersionResource).Namespace(tracker.Spec.Namespace).Watch(ctx, listOpts)
		},
	}

	_, err := watchtools.UntilWithSync(ctx, lwe, &unstructured.Unstructured{}, func(store cache.Store) (bool, error) {
		objs := store.List()

		if debug() {
			fmt.Printf("[TrackUntilEliminated][%s] Precondition called with %d items in the list\n", tracker.Spec.String(), len(objs))
		}

		for _, obj := range objs {
			u := obj.(*unstructured.Unstructured)

			if u.GetName() == tracker.Spec.ResourceName {
				if debug() {
					fmt.Printf("[TrackUntilEliminated][%s] Found existing object: wait for deletion event\n", tracker.Spec.String())
				}
				return false, nil
			}
		}

		if debug() {
			fmt.Printf("[TrackUntilEliminated][%s][%d] Not found existing object: stop tracking\n", tracker.Spec.String(), len(objs))
		}
		return true, nil
	}, func(ev watch.Event) (bool, error) {
		if debug() {
			fmt.Printf("[TrackUntilEliminated][%s] event: %#v\n", tracker.Spec.String(), ev.Type)
		}

		obj := ev.Object

		switch ev.Type {
		case watch.Added:
			if debug() {
				objDump, _ := json.MarshalIndent(obj, "", "  ")
				fmt.Printf("[TrackUntilEliminated][%s] Added object:\n%s\n---\n", tracker.Spec.String(), objDump)
			}
		case watch.Modified:
			if debug() {
				objDump, _ := json.MarshalIndent(obj, "", "  ")
				fmt.Printf("[TrackUntilEliminated][%s] Updated object:\n%s\n---\n", tracker.Spec.String(), objDump)
			}
		case watch.Deleted:
			if debug() {
				objDump, _ := json.MarshalIndent(obj, "", "  ")
				fmt.Printf("[TrackUntilEliminated][%s] Deleted object:\n%s\n---\n", tracker.Spec.String(), objDump)
			}

			u := obj.(*unstructured.Unstructured)

			if u.GetName() == tracker.Spec.ResourceName {
				close(tracker.resourceEliminated)
				return true, nil
			}

		case watch.Error:
			errData, err := json.Marshal(ev.Object)
			if err != nil {
				panic(err.Error())
			}
			return true, fmt.Errorf("%s watch error: %s", tracker.Spec.String(), errData)
		}

		return false, nil
	})

	return err
}

func debug() bool {
	return os.Getenv("KUBEDOG_ELIMINATION_TRACKER_DEBUG") == "1"
}
