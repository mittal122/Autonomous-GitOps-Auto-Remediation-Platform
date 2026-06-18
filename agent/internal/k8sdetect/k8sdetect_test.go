package k8sdetect_test

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/apimachinery/pkg/version"

	"github.com/autosre/agent/internal/k8sdetect"
)

var alertmanagerConfigGVR = schema.GroupVersionResource{
	Group: "monitoring.coreos.com", Version: "v1alpha1", Resource: "alertmanagerconfigs",
}

func TestStatus_NilDetector(t *testing.T) {
	var d *k8sdetect.Detector
	st := d.Status(context.Background())
	if st.Connected {
		t.Error("expected Connected=false for a nil detector")
	}
}

func TestStatus_Connected(t *testing.T) {
	cs := fake.NewSimpleClientset()
	fd := cs.Discovery().(*fakediscovery.FakeDiscovery)
	fd.FakedServerVersion = &version.Info{GitVersion: "v1.29.3"}

	d := k8sdetect.New(cs, nil, true, "autosre")
	st := d.Status(context.Background())
	if !st.Connected || st.ServerVersion != "v1.29.3" || !st.InCluster {
		t.Errorf("unexpected status: %+v", st)
	}
}

func TestOperatorDetected_FalseWithoutCRD(t *testing.T) {
	cs := fake.NewSimpleClientset()
	d := k8sdetect.New(cs, nil, false, "autosre")
	if d.OperatorDetected(context.Background()) {
		t.Error("expected OperatorDetected=false when no CRD is registered")
	}
}

func TestOperatorDetected_TrueWithCRD(t *testing.T) {
	cs := fake.NewSimpleClientset()
	fd := cs.Discovery().(*fakediscovery.FakeDiscovery)
	fd.Resources = []*metav1.APIResourceList{
		{GroupVersion: alertmanagerConfigGVR.GroupVersion().String()},
	}

	d := k8sdetect.New(cs, nil, false, "autosre")
	if !d.OperatorDetected(context.Background()) {
		t.Error("expected OperatorDetected=true when the CRD is registered")
	}
}

func TestApplyAlertmanagerWebhook_NoOperator(t *testing.T) {
	cs := fake.NewSimpleClientset()
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)

	d := k8sdetect.New(cs, dyn, false, "autosre")
	applied, reason := d.ApplyAlertmanagerWebhook(context.Background(), "http://example.com/webhook/alertmanager")
	if applied {
		t.Error("expected applied=false when the Operator CRD isn't installed")
	}
	if reason == "" {
		t.Error("expected a non-empty reason")
	}
}

func TestApplyAlertmanagerWebhook_NoDynamicClient(t *testing.T) {
	d := k8sdetect.New(fake.NewSimpleClientset(), nil, false, "autosre")
	applied, reason := d.ApplyAlertmanagerWebhook(context.Background(), "http://example.com/webhook/alertmanager")
	if applied {
		t.Error("expected applied=false when there's no dynamic client")
	}
	if reason == "" {
		t.Error("expected a non-empty reason")
	}
}

func TestApplyAlertmanagerWebhook_CreatesAndUpdates(t *testing.T) {
	cs := fake.NewSimpleClientset()
	fd := cs.Discovery().(*fakediscovery.FakeDiscovery)
	fd.Resources = []*metav1.APIResourceList{
		{GroupVersion: alertmanagerConfigGVR.GroupVersion().String()},
	}

	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)

	d := k8sdetect.New(cs, dyn, false, "autosre")

	applied, reason := d.ApplyAlertmanagerWebhook(context.Background(), "http://example.com/webhook/alertmanager")
	if !applied || reason != "created" {
		t.Fatalf("expected applied=true reason=created, got applied=%v reason=%q", applied, reason)
	}

	// Applying again with a different URL should update the existing object, not error.
	applied, reason = d.ApplyAlertmanagerWebhook(context.Background(), "http://example.com/webhook/alertmanager-v2")
	if !applied || reason != "updated" {
		t.Fatalf("expected applied=true reason=updated, got applied=%v reason=%q", applied, reason)
	}

	obj, err := dyn.Resource(alertmanagerConfigGVR).Namespace("autosre").Get(context.Background(), "autosre-webhook", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	spec, _ := obj.Object["spec"].(map[string]any)
	receivers, _ := spec["receivers"].([]any)
	if len(receivers) == 0 {
		t.Fatal("expected at least one receiver in the updated object")
	}
}
