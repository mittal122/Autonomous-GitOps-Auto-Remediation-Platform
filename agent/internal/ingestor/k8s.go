package ingestor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/uid"
)

// reasonMapping maps a Kubernetes event Reason to the normalized AutoSRE values.
type reasonMapping struct {
	reason   string
	severity string
}

// watchedEventReasons maps Kubernetes event Reason values that can be classified
// without inspecting the event message. Reasons requiring message inspection are
// handled inline in mapEvent.
var watchedEventReasons = map[string]reasonMapping{
	"OOMKilling":       {"OOMKilled", "critical"},
	"BackOff":          {"CrashLoopBackOff", "critical"},
	"FailedScheduling": {"FailedScheduling", "warning"},
	"NodeNotReady":     {"NotReady", "critical"},
}

type k8sWatcher struct {
	client kubernetes.Interface
	log    *slog.Logger
}

func newK8sWatcher(client kubernetes.Interface, log *slog.Logger) *k8sWatcher {
	return &k8sWatcher{client: client, log: log}
}

// Start blocks until ctx is cancelled, streaming normalized Signals onto out.
// It must be called in a goroutine.
func (w *k8sWatcher) Start(ctx context.Context, out chan<- contracts.Signal) {
	factory := informers.NewSharedInformerFactory(w.client, 30*time.Second)

	// Watch v1.Event objects — primary detection path.
	factory.Core().V1().Events().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) { w.handleEvent(obj, out) },
		// UpdateFunc fires when Count increments on an existing event (recurring failure).
		UpdateFunc: func(_, obj interface{}) { w.handleEvent(obj, out) },
	})

	// Watch v1.Pod objects — secondary path that catches crash states even when
	// event volume is throttled by Kubernetes' event aggregation.
	factory.Core().V1().Pods().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(_, obj interface{}) { w.handlePod(obj, out) },
	})

	// Watch v1.Node objects for NotReady conditions.
	factory.Core().V1().Nodes().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(_, obj interface{}) { w.handleNode(obj, out) },
	})

	factory.Start(ctx.Done())

	synced := factory.WaitForCacheSync(ctx.Done())
	for inf, ok := range synced {
		if !ok {
			w.log.Error("k8s informer cache sync failed", "informer", fmt.Sprintf("%T", inf))
		}
	}
	w.log.Info("k8s informer caches synced; watching for cluster events")

	<-ctx.Done()
	w.log.Info("k8s watcher stopped")
}

func (w *k8sWatcher) handleEvent(obj interface{}, out chan<- contracts.Signal) {
	event, ok := obj.(*corev1.Event)
	if !ok {
		return
	}
	// Skip Normal informational events; only Warning events indicate problems.
	if event.Type != corev1.EventTypeWarning {
		return
	}
	sig, emit := w.mapEvent(event)
	if emit {
		out <- sig
	}
}

// mapEvent converts a Kubernetes Warning event to a Signal.
// Returns (Signal, true) if the event matches a watched condition, (zero, false) otherwise.
// Exported for testing via the internal test package.
func (w *k8sWatcher) mapEvent(event *corev1.Event) (contracts.Signal, bool) {
	var mapping reasonMapping

	if m, ok := watchedEventReasons[event.Reason]; ok {
		mapping = m
	} else {
		// Reasons that require message inspection to distinguish from benign cases.
		switch event.Reason {
		case "Killing":
			// kubelet emits "Killing" for both graceful stops and OOM kills;
			// only the OOM variant contains "OOM" in the message.
			if !strings.Contains(event.Message, "OOM") {
				return contracts.Signal{}, false
			}
			mapping = reasonMapping{"OOMKilled", "critical"}
		case "Failed":
			// "Failed" covers many pod failures; we only care about image pull failures.
			if !strings.Contains(event.Message, "ImagePullBackOff") &&
				!strings.Contains(event.Message, "Back-off pulling") {
				return contracts.Signal{}, false
			}
			mapping = reasonMapping{"ImagePullBackOff", "warning"}
		default:
			w.log.Debug("ignoring unrecognized k8s event reason", "reason", event.Reason)
			return contracts.Signal{}, false
		}
	}

	ts := event.LastTimestamp.Time
	if ts.IsZero() {
		ts = time.Now()
	}

	return contracts.Signal{
		ID:        uid.New(),
		Source:    "k8s-event",
		Namespace: event.InvolvedObject.Namespace,
		Kind:      event.InvolvedObject.Kind,
		Resource:  event.InvolvedObject.Name,
		Reason:    mapping.reason,
		Message:   event.Message,
		Severity:  mapping.severity,
		Labels: map[string]string{
			"k8s_reason":   event.Reason,
			"k8s_count":    fmt.Sprintf("%d", event.Count),
			"involved_uid": string(event.InvolvedObject.UID),
		},
		ReceivedAt: time.Now(),
	}, true
}

func (w *k8sWatcher) handlePod(obj interface{}, out chan<- contracts.Signal) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	for _, sig := range w.mapPodCrash(pod) {
		out <- sig
	}
}

// mapPodCrash inspects pod container statuses and emits Signals for crash conditions.
// It is the secondary detection path for states that may not surface as Warning events.
func (w *k8sWatcher) mapPodCrash(pod *corev1.Pod) []contracts.Signal {
	var sigs []contracts.Signal
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			switch cs.State.Waiting.Reason {
			case "CrashLoopBackOff":
				sigs = append(sigs, makePodSignal(pod, cs.Name, "CrashLoopBackOff", "critical", cs.State.Waiting.Message))
			case "ImagePullBackOff", "ErrImagePull":
				sigs = append(sigs, makePodSignal(pod, cs.Name, "ImagePullBackOff", "warning", cs.State.Waiting.Message))
			}
		}
		// Detect OOMKill from last termination state — useful when the event is
		// deduplicated away by Kubernetes' event aggregation.
		if cs.LastTerminationState.Terminated != nil &&
			cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
			sigs = append(sigs, makePodSignal(pod, cs.Name, "OOMKilled", "critical", "container was OOM killed"))
		}
	}
	return sigs
}

func (w *k8sWatcher) handleNode(obj interface{}, out chan<- contracts.Signal) {
	node, ok := obj.(*corev1.Node)
	if !ok {
		return
	}
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionFalse {
			out <- contracts.Signal{
				ID:       uid.New(),
				Source:   "k8s-event",
				Kind:     "Node",
				Resource: node.Name,
				Reason:   "NotReady",
				Message:  cond.Message,
				Severity: "critical",
				Labels:   map[string]string{"node": node.Name},
				ReceivedAt: time.Now(),
			}
		}
	}
}

func makePodSignal(pod *corev1.Pod, container, reason, severity, message string) contracts.Signal {
	return contracts.Signal{
		ID:        uid.New(),
		Source:    "k8s-event",
		Namespace: pod.Namespace,
		Kind:      "Pod",
		Resource:  pod.Name,
		Reason:    reason,
		Message:   message,
		Severity:  severity,
		Labels: map[string]string{
			"container": container,
			"pod_uid":   string(pod.UID),
		},
		ReceivedAt: time.Now(),
	}
}
