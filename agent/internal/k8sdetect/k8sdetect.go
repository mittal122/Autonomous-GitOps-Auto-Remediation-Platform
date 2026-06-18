// Package k8sdetect provides read-only Kubernetes connectivity status and a
// best-effort, additive way to wire up Alertmanager's webhook receiver — without
// ever reading or mutating the cluster's existing Alertmanager configuration.
//
// "Best-effort" means: if the Prometheus Operator's AlertmanagerConfig CRD isn't
// installed, ApplyAlertmanagerWebhook reports why and does nothing — it never
// attempts to parse or patch a raw alertmanager.yml ConfigMap/Secret, since that
// risks corrupting unrelated alert routes the operator already trusts.
package k8sdetect

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

var alertmanagerConfigGVR = schema.GroupVersionResource{
	Group:    "monitoring.coreos.com",
	Version:  "v1alpha1",
	Resource: "alertmanagerconfigs",
}

const objectName = "autosre-webhook"

// Detector reports Kubernetes connectivity and applies the additive AlertmanagerConfig
// object when the Prometheus Operator is present. A nil *Detector or nil clients are
// handled gracefully — every method degrades to "not available" rather than panicking.
type Detector struct {
	client    kubernetes.Interface
	dyn       dynamic.Interface
	inCluster bool
	namespace string
}

// New constructs a Detector. client and dyn may be nil if Kubernetes access is
// unavailable; namespace defaults to "default" when empty.
func New(client kubernetes.Interface, dyn dynamic.Interface, inCluster bool, namespace string) *Detector {
	if namespace == "" {
		namespace = "default"
	}
	return &Detector{client: client, dyn: dyn, inCluster: inCluster, namespace: namespace}
}

// Status is a read-only snapshot of Kubernetes connectivity for the integrations dashboard.
type Status struct {
	Connected     bool   `json:"connected"`
	InCluster     bool   `json:"in_cluster"`
	ServerVersion string `json:"server_version,omitempty"`
	Error         string `json:"error,omitempty"`
}

// Status reports whether the configured Kubernetes client can reach the API server.
func (d *Detector) Status(_ context.Context) Status {
	if d == nil || d.client == nil {
		return Status{Connected: false, Error: "no Kubernetes client configured"}
	}
	ver, err := d.client.Discovery().ServerVersion()
	if err != nil {
		return Status{Connected: false, InCluster: d.inCluster, Error: err.Error()}
	}
	return Status{Connected: true, InCluster: d.inCluster, ServerVersion: ver.GitVersion}
}

// OperatorDetected reports whether the Prometheus Operator's AlertmanagerConfig CRD
// is installed in this cluster.
func (d *Detector) OperatorDetected(_ context.Context) bool {
	if d == nil || d.client == nil {
		return false
	}
	_, err := d.client.Discovery().ServerResourcesForGroupVersion(alertmanagerConfigGVR.GroupVersion().String())
	return err == nil
}

// ApplyAlertmanagerWebhook creates or updates a single additive AlertmanagerConfig
// object (name "autosre-webhook") routing all alerts to webhookURL. It only ever
// touches that one object — never the user's existing Alertmanager Secret/ConfigMap —
// so it cannot corrupt unrelated routes. Whether the cluster's Alertmanager actually
// picks up this object depends on its alertmanagerConfigSelector matching label
// app.kubernetes.io/managed-by=autosre, which is the caller's responsibility to verify.
func (d *Detector) ApplyAlertmanagerWebhook(ctx context.Context, webhookURL string) (applied bool, reason string) {
	if d == nil || d.dyn == nil {
		return false, "Kubernetes API access unavailable"
	}
	if !d.OperatorDetected(ctx) {
		return false, "Prometheus Operator AlertmanagerConfig CRD not found in this cluster"
	}

	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1alpha1",
		"kind":       "AlertmanagerConfig",
		"metadata": map[string]any{
			"name":      objectName,
			"namespace": d.namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "autosre",
			},
		},
		"spec": map[string]any{
			"route": map[string]any{
				"receiver": "autosre-webhook",
			},
			"receivers": []any{
				map[string]any{
					"name": "autosre-webhook",
					"webhookConfigs": []any{
						map[string]any{
							"url":          webhookURL,
							"sendResolved": true,
						},
					},
				},
			},
		},
	}}

	client := d.dyn.Resource(alertmanagerConfigGVR).Namespace(d.namespace)
	_, err := client.Create(ctx, obj, metav1.CreateOptions{})
	if err == nil {
		return true, "created"
	}
	if !apierrors.IsAlreadyExists(err) {
		return false, fmt.Sprintf("create failed: %v", err)
	}

	existing, getErr := client.Get(ctx, objectName, metav1.GetOptions{})
	if getErr != nil {
		return false, fmt.Sprintf("object exists but could not be read: %v", getErr)
	}
	existing.Object["spec"] = obj.Object["spec"]
	if _, updErr := client.Update(ctx, existing, metav1.UpdateOptions{}); updErr != nil {
		return false, fmt.Sprintf("update failed: %v", updErr)
	}
	return true, "updated"
}
