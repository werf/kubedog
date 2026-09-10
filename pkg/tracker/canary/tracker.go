package canary

import (
	"context"
	"fmt"
	"time"

	"github.com/fluxcd/flagger/pkg/apis/flagger/v1beta1"
	flaggerscheme "github.com/fluxcd/flagger/pkg/client/clientset/versioned/scheme"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/debug"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

func init() {
	flaggerscheme.AddToScheme(scheme.Scheme)
}

type FailedReport struct {
	FailedReason string
	CanaryStatus CanaryStatus
}

type Tracker struct {
	tracker.Tracker
	LogsFromTime time.Time

	Added     chan CanaryStatus
	Succeeded chan CanaryStatus
	Failed    chan CanaryStatus
	Status    chan CanaryStatus

	EventMsg chan string

	State tracker.TrackerState

	lastObject     *v1beta1.Canary
	failedReason   string
	canaryStatuses map[string]v1beta1.CanaryStatus

	errors chan error

	objectAdded    chan *v1beta1.Canary
	objectModified chan *v1beta1.Canary
	objectDeleted  chan *v1beta1.Canary
	objectFailed   chan interface{}

	dynamicClient dynamic.Interface
}

func NewTracker(name, namespace string, kube kubernetes.Interface, dynamicClient dynamic.Interface, informerFactory *util.Concurrent[*informer.InformerFactory], opts tracker.Options) *Tracker {
	return &Tracker{
		Tracker: tracker.Tracker{
			Kube:             kube,
			Namespace:        namespace,
			FullResourceName: fmt.Sprintf("canary/%s", name),
			ResourceName:     name,
			LogsFromTime:     opts.LogsFromTime,
			InformerFactory:  informerFactory,
		},

		Added:     make(chan CanaryStatus, 1),
		Succeeded: make(chan CanaryStatus),
		Failed:    make(chan CanaryStatus),
		Status:    make(chan CanaryStatus, 100),

		EventMsg: make(chan string, 1),

		State: tracker.Initial,

		objectAdded:    make(chan *v1beta1.Canary),
		objectModified: make(chan *v1beta1.Canary),
		objectDeleted:  make(chan *v1beta1.Canary),
		objectFailed:   make(chan interface{}, 1),
		errors:         make(chan error, 1),

		dynamicClient: dynamicClient,
	}
}

func (canary *Tracker) Track(ctx context.Context) error {
	canaryInformerCleanupFn, err := canary.runInformer(ctx)
	if err != nil {
		return err
	}
	defer canaryInformerCleanupFn()

	for {
		select {
		case object := <-canary.objectAdded:
			if err := canary.handleCanaryState(ctx, object); err != nil {
				return err
			}
		case object := <-canary.objectModified:
			if err := canary.handleCanaryState(ctx, object); err != nil {
				return err
			}
		case failure := <-canary.objectFailed:
			switch failure := failure.(type) {
			case string:
				canary.State = tracker.ResourceFailed
				canary.failedReason = failure

				var status CanaryStatus

				status = CanaryStatus{IsFailed: true, FailedReason: failure}
				canary.Failed <- status
			default:
				panic(fmt.Errorf("unexpected type %T", failure))
			}
		case <-ctx.Done():
			if debug.Debug() {
				fmt.Printf("Canary `%s` tracker context canceled: %s\n", canary.ResourceName, context.Cause(ctx))
			}

			return context.Cause(ctx)
		case err := <-canary.errors:
			return err
		}
	}
}

func (canary *Tracker) runInformer(ctx context.Context) (cleanupFn func(), err error) {
	var inform *util.Concurrent[*informer.Informer]
	if err := canary.InformerFactory.RWTransactionErr(func(factory *informer.InformerFactory) error {
		inform, err = factory.ForNamespace(schema.GroupVersionResource{
			Group:    "flagger.app",
			Version:  "v1beta1",
			Resource: "canaries",
		}, canary.Namespace)
		if err != nil {
			return fmt.Errorf("get informer from factory: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	if err := inform.RWTransactionErr(func(inf *informer.Informer) error {
		handler, err := inf.AddEventHandler(
			cache.FilteringResourceEventHandler{
				FilterFunc: func(obj interface{}) bool {
					if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
						obj = d.Obj
					}

					canaryObj := &v1beta1.Canary{}
					lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, canaryObj))
					return canaryObj.Name == canary.ResourceName &&
						canaryObj.Namespace == canary.Namespace
				},
				Handler: cache.ResourceEventHandlerFuncs{
					AddFunc: func(obj interface{}) {
						if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
							obj = d.Obj
						}

						canaryObj := &v1beta1.Canary{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, canaryObj))
						canary.objectAdded <- canaryObj
					},
					UpdateFunc: func(oldObj, newObj interface{}) {
						if d, ok := newObj.(cache.DeletedFinalStateUnknown); ok {
							newObj = d.Obj
						}

						canaryObj := &v1beta1.Canary{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(newObj.(*unstructured.Unstructured).Object, canaryObj))
						canary.objectModified <- canaryObj
					},
					DeleteFunc: func(obj interface{}) {
						if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
							obj = d.Obj
						}

						canaryObj := &v1beta1.Canary{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, canaryObj))
						canary.objectDeleted <- canaryObj
					},
				},
			},
		)
		if err != nil {
			return fmt.Errorf("add event handler: %w", err)
		}

		cleanupFn = func() {
			inf.RemoveEventHandler(handler)
		}

		inf.Run()

		return nil
	}); err != nil {
		return nil, err
	}

	return cleanupFn, nil
}

func (canary *Tracker) handleCanaryState(ctx context.Context, object *v1beta1.Canary) error {
	canary.lastObject = object
	canary.StatusGeneration++

	status := NewCanaryStatus(object, canary.StatusGeneration, canary.State == tracker.ResourceFailed, canary.failedReason, canary.canaryStatuses)

	switch canary.State {
	case tracker.Initial:
		switch {
		case status.IsSucceeded:
			canary.State = tracker.ResourceSucceeded
			canary.Succeeded <- status
		case status.IsFailed:
			canary.State = tracker.ResourceFailed
			canary.Failed <- status
		default:
			canary.State = tracker.ResourceAdded
			canary.Added <- status
		}
	case tracker.ResourceAdded, tracker.ResourceFailed:
		switch {
		case status.IsSucceeded:
			canary.State = tracker.ResourceSucceeded
			canary.Succeeded <- status
		case status.IsFailed:
			canary.State = tracker.ResourceFailed
			canary.Failed <- status
		default:
			canary.Status <- status
		}
	case tracker.ResourceSucceeded:
		canary.State = tracker.ResourceSucceeded
		canary.Succeeded <- status
	}

	return nil
}
