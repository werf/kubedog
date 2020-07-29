package utils

import (
	"context"
	"fmt"
	"strings"

	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
)

// SortableEvents implements sort.Interface for []api.Event based on the Timestamp field
type SortableEvents []corev1.Event

func (list SortableEvents) Len() int {
	return len(list)
}

func (list SortableEvents) Swap(i, j int) {
	list[i], list[j] = list[j], list[i]
}

func (list SortableEvents) Less(i, j int) bool {
	return list[i].LastTimestamp.Time.Before(list[j].LastTimestamp.Time)
}

// formatEventSource formats EventSource as a comma separated string excluding Host when empty
func FormatEventSource(es corev1.EventSource) string {
	EventSourceString := []string{es.Component}
	if len(es.Host) > 0 {
		EventSourceString = append(EventSourceString, es.Host)
	}
	return strings.Join(EventSourceString, ", ")
}

func DescribeEvents(el *corev1.EventList) {
	if len(el.Items) == 0 {
		fmt.Printf("Events:\t<none>\n")
		return
	}
	//w.Flush()
	sort.Sort(SortableEvents(el.Items))
	//w.Write(LEVEL_0, "Events:\n  Type\tReason\tAge\tFrom\tMessage\n")
	fmt.Printf("Events:\n  Type\tReason\tAge\tFrom\tAction\tMessage\n")
	//w.Write(LEVEL_1, "----\t------\t----\t----\t-------\n")
	fmt.Printf("----\t------\t----\t----\t-------\n")
	for _, e := range el.Items {
		var interval string
		if e.Count > 1 {
			interval = fmt.Sprintf("%s (x%d over %s)", TranslateTimestampSince(e.LastTimestamp), e.Count, TranslateTimestampSince(e.FirstTimestamp))
		} else {
			interval = TranslateTimestampSince(e.FirstTimestamp)
		}
		//w.Write(LEVEL_1, "%v\t%v\t%s\t%v\t%v\n",
		fmt.Printf("%v\t%v\t%s\t%v\t%v\t%v\n",
			e.Type,
			e.Reason,
			interval,
			FormatEventSource(e.Source),
			e.Action,
			strings.TrimSpace(e.Message),
		)
	}

}

func EventFieldSelectorFromResource(obj interface{}) string {
	meta := ControllerAccessor(obj)
	field := fields.Set{}
	field["involvedObject.name"] = meta.Name()
	field["involvedObject.namespace"] = meta.Namespace()
	field["involvedObject.uid"] = string(meta.UID())
	return field.AsSelector().String()
}

func ListEventsForObject(ctx context.Context, client kubernetes.Interface, obj interface{}) (*corev1.EventList, error) {
	options := metav1.ListOptions{
		FieldSelector: EventFieldSelectorFromResource(obj),
	}
	evList, err := client.CoreV1().Events(ControllerAccessor(obj).Namespace()).List(ctx, options)
	if err != nil {
		return nil, err
	}
	return evList, nil
}
