package generic

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/werf/kubedog-for-werf-helm/pkg/tracker/indicators"
)

type ResourceStatus struct {
	Indicator *indicators.StringEqualConditionIndicator

	isReady            bool
	isDeleted          bool
	isFailed           bool
	failureReason      string
	humanConditionPath string
}

func NewResourceStatus(object *unstructured.Unstructured) (*ResourceStatus, error) {
	resourceStatusIndicator, humanJSONPath, err := NewResourceStatusIndicator(object)
	if err != nil {
		return nil, fmt.Errorf("error getting resource status indicator: %w", err)
	}

	return &ResourceStatus{
		Indicator:          resourceStatusIndicator,
		isReady:            resourceStatusIndicator == nil || (resourceStatusIndicator != nil && resourceStatusIndicator.IsReady()),
		isFailed:           resourceStatusIndicator != nil && resourceStatusIndicator.IsFailed(),
		humanConditionPath: humanJSONPath,
	}, nil
}

func NewSucceededResourceStatus() *ResourceStatus {
	return &ResourceStatus{
		isReady: true,
	}
}

func NewFailedResourceStatus(failureReason string) *ResourceStatus {
	return &ResourceStatus{
		isFailed:      true,
		failureReason: failureReason,
	}
}

func NewDeletedResourceStatus() *ResourceStatus {
	return &ResourceStatus{
		isDeleted: true,
	}
}

func (s *ResourceStatus) IsReady() bool {
	return s.isReady
}

func (s *ResourceStatus) IsFailed() bool {
	return s.isFailed
}

func (s *ResourceStatus) IsDeleted() bool {
	return s.isDeleted
}

func (s *ResourceStatus) FailureReason() string {
	return s.failureReason
}

func (s *ResourceStatus) HumanConditionPath() string {
	return s.humanConditionPath
}

func (s *ResourceStatus) DiffersFrom(newStatus *ResourceStatus) bool {
	if s.IsReady() != newStatus.IsReady() ||
		s.IsDeleted() != newStatus.IsDeleted() ||
		s.IsFailed() != newStatus.IsFailed() ||
		s.FailureReason() != newStatus.FailureReason() ||
		s.HumanConditionPath() != newStatus.HumanConditionPath() {
		return true
	}

	return false
}
