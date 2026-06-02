package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kfake "k8s.io/client-go/kubernetes/fake"

	"github.com/kubetidy/kubetidy/internal/kube"
)

func sweepFakeClients(objs ...runtime.Object) *kube.Clients {
	return &kube.Clients{Kube: kfake.NewSimpleClientset(objs...), Context: "test-ctx"}
}

func TestSweepWithClients(t *testing.T) {
	i32 := func(v int32) *int32 { return &v }
	objs := []runtime.Object{
		// live service (has a matching pod) + orphaned service (no match)
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "live"}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Selector: map[string]string{"app": "live"}}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "dead"}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Selector: map[string]string{"app": "dead"}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "live-1", Labels: map[string]string{"app": "live"}}, Spec: corev1.PodSpec{Volumes: []corev1.Volume{{VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "used"}}}}}},
		// PVCs: used + orphan(50Gi)
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "used"}, Spec: corev1.PersistentVolumeClaimSpec{Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")}}}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "orphan"}, Spec: corev1.PersistentVolumeClaimSpec{Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("50Gi")}}}},
		// zombie deployment (0 replicas)
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "batch"}, Spec: appsv1.DeploymentSpec{Replicas: i32(0)}},
		// namespaces: shop (busy via the live pod), idle
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "shop"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "idle"}},
	}
	clients := sweepFakeClients(objs...)
	findings, err := sweepWithClients(context.Background(), &sweepFlags{storageCost: 0.10}, clients)
	if err != nil {
		t.Fatalf("sweepWithClients: %v", err)
	}

	var buf bytes.Buffer
	if err := renderSweep(&buf, clients.Context, findings, "table"); err != nil {
		t.Fatalf("renderSweep: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"orphaned service", "shop/dead",
		"unused pvc", "shop/orphan", "50Gi", "$5/mo",
		"idle namespace", "idle",
		"zombie workload", "shop/batch",
		"never deletes anything",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sweep output missing %q\n--- got ---\n%s", want, out)
		}
	}
	if strings.Contains(out, "shop/live") || strings.Contains(out, "shop/used") {
		t.Errorf("live service / used PVC must not be flagged\n--- got ---\n%s", out)
	}
}

func TestRenderSweepEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := renderSweep(&buf, "ctx", nil, "table"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Nothing to clean up") {
		t.Errorf("empty sweep should say nothing to clean up: %s", buf.String())
	}
}
