// Tests for the k8sWatcher's signal-mapping logic.
// These are unit tests: they exercise mapEvent and mapPodCrash directly,
// with no Kubernetes cluster required.
package ingestor

import (
	"log/slog"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newTestWatcher() *k8sWatcher {
	return &k8sWatcher{log: slog.Default()}
}

func makeWarningEvent(reason, message, kind, ns, name string) *corev1.Event {
	return &corev1.Event{
		Type:    corev1.EventTypeWarning,
		Reason:  reason,
		Message: message,
		InvolvedObject: corev1.ObjectReference{
			Kind:      kind,
			Namespace: ns,
			Name:      name,
		},
		Count: 1,
	}
}

// ---------------------------------------------------------------------------
// mapEvent tests
// ---------------------------------------------------------------------------

func TestMapEvent_OOMKilling(t *testing.T) {
	w := newTestWatcher()
	ev := makeWarningEvent("OOMKilling", "killing process due to memory limit", "Pod", "production", "payment-abc")
	sig, ok := w.mapEvent(ev)
	if !ok {
		t.Fatal("expected signal, got none")
	}
	assertEqual(t, "OOMKilled", sig.Reason)
	assertEqual(t, "critical", sig.Severity)
	assertEqual(t, "k8s-event", sig.Source)
	assertEqual(t, "Pod", sig.Kind)
	assertEqual(t, "production", sig.Namespace)
	assertEqual(t, "payment-abc", sig.Resource)
}

func TestMapEvent_BackOff_CrashLoop(t *testing.T) {
	w := newTestWatcher()
	ev := makeWarningEvent("BackOff", "Back-off restarting failed container", "Pod", "default", "api-xyz")
	sig, ok := w.mapEvent(ev)
	if !ok {
		t.Fatal("expected signal")
	}
	assertEqual(t, "CrashLoopBackOff", sig.Reason)
	assertEqual(t, "critical", sig.Severity)
}

func TestMapEvent_FailedScheduling(t *testing.T) {
	w := newTestWatcher()
	ev := makeWarningEvent("FailedScheduling", "0/3 nodes are available", "Pod", "staging", "worker-1")
	sig, ok := w.mapEvent(ev)
	if !ok {
		t.Fatal("expected signal")
	}
	assertEqual(t, "FailedScheduling", sig.Reason)
	assertEqual(t, "warning", sig.Severity)
}

func TestMapEvent_NodeNotReady(t *testing.T) {
	w := newTestWatcher()
	ev := makeWarningEvent("NodeNotReady", "Node is not ready", "Node", "", "worker-node-1")
	sig, ok := w.mapEvent(ev)
	if !ok {
		t.Fatal("expected signal")
	}
	assertEqual(t, "NotReady", sig.Reason)
	assertEqual(t, "critical", sig.Severity)
}

func TestMapEvent_Failed_ImagePullBackOff(t *testing.T) {
	w := newTestWatcher()
	ev := makeWarningEvent("Failed", "Back-off pulling image: ImagePullBackOff", "Pod", "prod", "frontend-1")
	sig, ok := w.mapEvent(ev)
	if !ok {
		t.Fatal("expected signal")
	}
	assertEqual(t, "ImagePullBackOff", sig.Reason)
	assertEqual(t, "warning", sig.Severity)
}

func TestMapEvent_Failed_UnrelatedMessage_Ignored(t *testing.T) {
	w := newTestWatcher()
	ev := makeWarningEvent("Failed", "probe failed: connection refused", "Pod", "prod", "svc-1")
	_, ok := w.mapEvent(ev)
	if ok {
		t.Error("expected no signal for unrelated Failed reason")
	}
}

func TestMapEvent_Killing_WithOOM_Emits(t *testing.T) {
	w := newTestWatcher()
	ev := makeWarningEvent("Killing", "Stopping container because of OOM kill", "Pod", "prod", "mem-hog")
	sig, ok := w.mapEvent(ev)
	if !ok {
		t.Fatal("expected signal for OOM killing")
	}
	assertEqual(t, "OOMKilled", sig.Reason)
}

func TestMapEvent_Killing_WithoutOOM_Ignored(t *testing.T) {
	w := newTestWatcher()
	// Graceful pod stop — should NOT produce a signal.
	ev := makeWarningEvent("Killing", "Stopping container gracefully", "Pod", "prod", "app-1")
	_, ok := w.mapEvent(ev)
	if ok {
		t.Error("expected no signal for graceful Killing event")
	}
}

func TestMapEvent_NormalEvent_Ignored(t *testing.T) {
	w := newTestWatcher()
	ev := &corev1.Event{
		Type:   corev1.EventTypeNormal,
		Reason: "Pulled",
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: "default", Name: "app"},
	}
	_, ok := w.mapEvent(ev)
	if ok {
		t.Error("expected no signal for Normal event")
	}
}

func TestMapEvent_UnknownReason_Ignored(t *testing.T) {
	w := newTestWatcher()
	ev := makeWarningEvent("SomeUnknownReason", "something happened", "Pod", "default", "app")
	_, ok := w.mapEvent(ev)
	if ok {
		t.Error("expected no signal for unknown reason")
	}
}

// ---------------------------------------------------------------------------
// mapPodCrash tests
// ---------------------------------------------------------------------------

func TestMapPodCrash_CrashLoopBackOff(t *testing.T) {
	w := newTestWatcher()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-abc", Namespace: "production"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "app",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "CrashLoopBackOff",
							Message: "back-off 5m0s restarting failed container",
						},
					},
				},
			},
		},
	}
	sigs := w.mapPodCrash(pod)
	if len(sigs) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(sigs))
	}
	assertEqual(t, "CrashLoopBackOff", sigs[0].Reason)
	assertEqual(t, "critical", sigs[0].Severity)
	assertEqual(t, "production", sigs[0].Namespace)
}

func TestMapPodCrash_ImagePullBackOff(t *testing.T) {
	w := newTestWatcher()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "frontend-1", Namespace: "staging"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "ui",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"},
					},
				},
			},
		},
	}
	sigs := w.mapPodCrash(pod)
	if len(sigs) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(sigs))
	}
	assertEqual(t, "ImagePullBackOff", sigs[0].Reason)
	assertEqual(t, "warning", sigs[0].Severity)
}

func TestMapPodCrash_OOMKilledTermination(t *testing.T) {
	w := newTestWatcher()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "mem-hog", Namespace: "production"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "worker",
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"},
					},
				},
			},
		},
	}
	sigs := w.mapPodCrash(pod)
	if len(sigs) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(sigs))
	}
	assertEqual(t, "OOMKilled", sigs[0].Reason)
	assertEqual(t, "critical", sigs[0].Severity)
}

func TestMapPodCrash_HealthyPod_NoSignals(t *testing.T) {
	w := newTestWatcher()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "healthy-pod", Namespace: "default"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:  "app",
					Ready: true,
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{},
					},
				},
			},
		},
	}
	sigs := w.mapPodCrash(pod)
	if len(sigs) != 0 {
		t.Errorf("expected no signals for healthy pod, got %d", len(sigs))
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func assertEqual(t *testing.T, want, got string) {
	t.Helper()
	if got != want {
		// Include context about which label or field differs.
		t.Errorf("got %q, want %q", got, want)
	}
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("%q does not contain %q", haystack, needle)
	}
}
