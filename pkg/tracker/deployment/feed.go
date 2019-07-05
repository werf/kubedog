package deployment

import (
	"context"
	"fmt"
	"sync"

	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/tracker/controller"
	"github.com/flant/kubedog/pkg/tracker/debug"
	"k8s.io/client-go/kubernetes"

	watchtools "k8s.io/client-go/tools/watch"
)

type Feed interface {
	controller.ControllerFeed

	OnStatusReport(func(DeploymentStatus) error)
	GetStatus() DeploymentStatus
	Track(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error
}

func NewFeed() Feed {
	return &feed{}
}

type feed struct {
	controller.CommonControllerFeed
	OnStatusReportFunc func(DeploymentStatus) error

	statusMux sync.Mutex
	status    DeploymentStatus
}

func (f *feed) OnStatusReport(function func(DeploymentStatus) error) {
	f.OnStatusReportFunc = function
}

func (f *feed) Track(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan bool, 0)

	parentContext := opts.ParentContext
	if parentContext == nil {
		parentContext = context.Background()
	}
	ctx, cancel := watchtools.ContextWithOptionalTimeout(parentContext, opts.Timeout)
	defer cancel()

	deploymentTracker := NewTracker(ctx, name, namespace, kube, opts)

	go func() {
		if debug.Debug() {
			fmt.Printf("  goroutine: start deploy/%s tracker\n", name)
		}
		err := deploymentTracker.Track()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- true
		}
	}()

	if debug.Debug() {
		fmt.Printf("  deploy/%s: for-select DeploymentTracker channels\n", name)
	}

	for {
		select {
		case isReady := <-deploymentTracker.Added:
			if debug.Debug() {
				fmt.Printf("    deploy/%s added\n", name)
			}

			if f.OnAddedFunc != nil {
				err := f.OnAddedFunc(isReady)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case <-deploymentTracker.Ready:
			if debug.Debug() {
				fmt.Printf("    deploy/%s ready: desired: %d, current: %d/%d, up-to-date: %d, available: %d\n",
					name,
					deploymentTracker.FinalDeploymentStatus.Replicas,
					deploymentTracker.FinalDeploymentStatus.ReadyReplicas,
					deploymentTracker.FinalDeploymentStatus.UnavailableReplicas,
					deploymentTracker.FinalDeploymentStatus.UpdatedReplicas,
					deploymentTracker.FinalDeploymentStatus.AvailableReplicas,
				)
			}

			if f.OnReadyFunc != nil {
				err := f.OnReadyFunc()
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case reason := <-deploymentTracker.Failed:
			if debug.Debug() {
				fmt.Printf("    deploy/%s failed. Tracker state: `%s`", name, deploymentTracker.State)
			}

			if f.OnFailedFunc != nil {
				err := f.OnFailedFunc(reason)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case msg := <-deploymentTracker.EventMsg:
			if debug.Debug() {
				fmt.Printf("    deploy/%s event: %s\n", name, msg)
			}

			if f.OnEventMsgFunc != nil {
				err := f.OnEventMsgFunc(msg)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case rs := <-deploymentTracker.AddedReplicaSet:
			if debug.Debug() {
				fmt.Printf("    deploy/%s got new replicaset `%s` (is new: %v)\n", deploymentTracker.ResourceName, rs.Name, rs.IsNew)
			}

			if f.OnAddedReplicaSetFunc != nil {
				err := f.OnAddedReplicaSetFunc(rs)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case pod := <-deploymentTracker.AddedPod:
			if debug.Debug() {
				fmt.Printf("    deploy/%s got new pod `%s`\n", deploymentTracker.ResourceName, pod.Name)
			}

			if f.OnAddedPodFunc != nil {
				err := f.OnAddedPodFunc(pod)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case chunk := <-deploymentTracker.PodLogChunk:
			if debug.Debug() {
				fmt.Printf("    deploy/%s pod `%s` log chunk\n", deploymentTracker.ResourceName, chunk.PodName)
				for _, line := range chunk.LogLines {
					fmt.Printf("po/%s [%s] %s\n", chunk.PodName, line.Timestamp, line.Message)
				}
			}

			if f.OnPodLogChunkFunc != nil {
				err := f.OnPodLogChunkFunc(chunk)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case podError := <-deploymentTracker.PodError:
			if debug.Debug() {
				fmt.Printf("    deploy/%s pod error: %s\n", deploymentTracker.ResourceName, podError.Message)
			}

			if f.OnPodErrorFunc != nil {
				err := f.OnPodErrorFunc(podError)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case status := <-deploymentTracker.StatusReport:
			f.setStatus(status)

			if f.OnStatusReportFunc != nil {
				err := f.OnStatusReportFunc(status)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case err := <-errorChan:
			return err

		case <-doneChan:
			return nil
		}
	}
}

func (f *feed) setStatus(status DeploymentStatus) {
	f.statusMux.Lock()
	defer f.statusMux.Unlock()
	f.status = status
}

func (f *feed) GetStatus() DeploymentStatus {
	f.statusMux.Lock()
	defer f.statusMux.Unlock()
	return f.status
}
