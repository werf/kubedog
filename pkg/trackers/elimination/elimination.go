package elimination

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/werf/logboek"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"k8s.io/client-go/dynamic"
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

						logboek.Context(ctx).Default().LogF("Resource status:\n%s\n---\n", resourceStatus.ManifestJson)
						logboek.Context(ctx).Default().LogOptionalLn()
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
	for {
		var err error
		var obj *unstructured.Unstructured

		if tracker.Spec.Namespace == "" {
			obj, err = tracker.KubeDynamicClient.Resource(tracker.Spec.GroupVersionResource).Get(context.Background(), tracker.Spec.ResourceName, metav1.GetOptions{})
		} else {
			obj, err = tracker.KubeDynamicClient.Resource(tracker.Spec.GroupVersionResource).Namespace(tracker.Spec.Namespace).Get(context.Background(), tracker.Spec.ResourceName, metav1.GetOptions{})
		}

		if errors.IsNotFound(err) {
			return nil
		} else if err != nil {
			return fmt.Errorf("error getting resource %s: %s", tracker.Spec.String(), err)
		}

		data, err := json.MarshalIndent(obj, "", "\t")
		if err != nil {
			return fmt.Errorf("resource %s json marshal failed: %s", tracker.Spec.String(), err)
		}

		tracker.ResourceStatus <- ResourceStatus{
			Spec:         tracker.Spec,
			ManifestJson: data,
		}

		time.Sleep(500 * time.Millisecond)
	}
}
