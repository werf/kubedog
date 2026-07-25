package replicaset

import (
	"context"
	"fmt"

	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	apilabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"

	"github.com/werf/kubedog/pkg/dyntracker/util"
	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/pod"
	"github.com/werf/kubedog/pkg/utils"
)

type ReplicaSet struct {
	Name  string
	IsNew bool
}

// TODO: add containers!
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
	Controller           utils.ControllerMetadata
	ReplicaSetAdded      chan *appsv1.ReplicaSet
	ReplicaSetModified   chan *appsv1.ReplicaSet
	ReplicaSetDeleted    chan *appsv1.ReplicaSet
	replicaSetUnselected chan *appsv1.ReplicaSet
	replicaSetSynced     chan struct{}
	Errors               chan error
}

func NewReplicaSetInformer(trk *tracker.Tracker, controller utils.ControllerMetadata) *ReplicaSetInformer {
	return &ReplicaSetInformer{
		Tracker: tracker.Tracker{
			Kube:                            trk.Kube,
			Namespace:                       trk.Namespace,
			ResourceName:                    trk.ResourceName,
			FullResourceName:                trk.FullResourceName,
			InformerFactory:                 trk.InformerFactory,
			SaveLogsOnlyForNumberOfReplicas: trk.SaveLogsOnlyForNumberOfReplicas,
		},
		Controller:         controller,
		ReplicaSetAdded:    make(chan *appsv1.ReplicaSet, 1),
		ReplicaSetModified: make(chan *appsv1.ReplicaSet, 1),
		ReplicaSetDeleted:  make(chan *appsv1.ReplicaSet, 1),
		Errors:             make(chan error, 1),
	}
}

func (r *ReplicaSetInformer) WithChannels(added chan *appsv1.ReplicaSet,
	modified chan *appsv1.ReplicaSet,
	deleted chan *appsv1.ReplicaSet,
	errors chan error,
) *ReplicaSetInformer {
	r.ReplicaSetAdded = added
	r.ReplicaSetModified = modified
	r.ReplicaSetDeleted = deleted
	r.Errors = errors
	return r
}

// WithUnselectedChannel reports ReplicaSets that stop matching the controller selector
// without being deleted. Consumers can remove them without permanently tombstoning the UID.
func (r *ReplicaSetInformer) WithUnselectedChannel(unselected chan *appsv1.ReplicaSet) *ReplicaSetInformer {
	r.replicaSetUnselected = unselected
	return r
}

// WithSyncedChannel reports when this handler has consumed its initial ReplicaSet list.
func (r *ReplicaSetInformer) WithSyncedChannel(synced chan struct{}) *ReplicaSetInformer {
	r.replicaSetSynced = synced
	return r
}

func (r *ReplicaSetInformer) Run(ctx context.Context) (cleanupFn func(), err error) {
	// The event channels may be unbuffered, so a send with no receiver left blocks the
	// shared informer goroutine forever: stop sending once the consumer is gone.
	ctx, stopSending := context.WithCancel(ctx)
	defer func() {
		if err != nil {
			stopSending()
		}
	}()

	var inform *util.Concurrent[*informer.Informer]
	if err := r.InformerFactory.RWTransactionErr(func(factory *informer.InformerFactory) error {
		inform, err = factory.ForNamespace(schema.GroupVersionResource{
			Group:    "apps",
			Version:  "v1",
			Resource: "replicasets",
		}, r.Namespace)
		if err != nil {
			return fmt.Errorf("get informer from factory: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	labelSelector, err := metav1.LabelSelectorAsSelector(r.Controller.LabelSelector())
	if err != nil {
		return nil, fmt.Errorf("convert label selector: %w", err)
	}

	if err := inform.RWTransactionErr(func(inf *informer.Informer) error {
		toReplicaSet := func(obj interface{}) *appsv1.ReplicaSet {
			if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = d.Obj
			}

			rsObj := &appsv1.ReplicaSet{}
			lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, rsObj))
			return rsObj
		}
		matches := func(rs *appsv1.ReplicaSet) bool {
			return labelSelector.Matches(apilabels.Set(rs.GetLabels()))
		}

		handler, err := inf.AddEventHandler(
			cache.ResourceEventHandlerFuncs{
				AddFunc: func(obj interface{}) {
					rsObj := toReplicaSet(obj)
					if matches(rsObj) {
						sendReplicaSet(ctx, r.ReplicaSetAdded, rsObj)
					}
				},
				UpdateFunc: func(oldObj, newObj interface{}) {
					oldReplicaSet := toReplicaSet(oldObj)
					newReplicaSet := toReplicaSet(newObj)
					oldMatches := matches(oldReplicaSet)
					newMatches := matches(newReplicaSet)

					switch {
					case oldReplicaSet.UID != newReplicaSet.UID:
						if oldMatches {
							sendReplicaSet(ctx, r.ReplicaSetDeleted, oldReplicaSet)
						}
						if newMatches {
							sendReplicaSet(ctx, r.ReplicaSetAdded, newReplicaSet)
						}
					case oldMatches && newMatches:
						sendReplicaSet(ctx, r.ReplicaSetModified, newReplicaSet)
					case oldMatches:
						if r.replicaSetUnselected != nil {
							sendReplicaSet(ctx, r.replicaSetUnselected, newReplicaSet)
						} else {
							sendReplicaSet(ctx, r.ReplicaSetDeleted, oldReplicaSet)
						}
					case newMatches:
						sendReplicaSet(ctx, r.ReplicaSetAdded, newReplicaSet)
					}
				},
				DeleteFunc: func(obj interface{}) {
					rsObj := toReplicaSet(obj)
					if matches(rsObj) {
						sendReplicaSet(ctx, r.ReplicaSetDeleted, rsObj)
					}
				},
			},
		)
		if err != nil {
			return fmt.Errorf("add event handler: %w", err)
		}

		cleanupFn = func() {
			stopSending()
			inf.RemoveEventHandler(handler)
		}

		inf.Run()
		if r.replicaSetSynced != nil {
			go func() {
				if !cache.WaitForCacheSync(ctx.Done(), handler.HasSynced) {
					return
				}

				select {
				case r.replicaSetSynced <- struct{}{}:
				case <-ctx.Done():
				}
			}()
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return cleanupFn, nil
}

func sendReplicaSet(ctx context.Context, ch chan<- *appsv1.ReplicaSet, rsObj *appsv1.ReplicaSet) {
	select {
	case ch <- rsObj:
	case <-ctx.Done():
	}
}
