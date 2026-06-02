package cleanup

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func i32(v int32) *int32 { return &v }

func svc(ns, name string, t corev1.ServiceType, selector map[string]string) corev1.Service {
	return corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       corev1.ServiceSpec{Type: t, Selector: selector},
	}
}

func pod(ns, name string, labels map[string]string, pvcs ...string) corev1.Pod {
	p := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels}}
	for _, c := range pvcs {
		p.Spec.Volumes = append(p.Spec.Volumes, corev1.Volume{
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: c}},
		})
	}
	return p
}

func pvc(ns, name, size string) corev1.PersistentVolumeClaim {
	return corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)},
			},
		},
	}
}

func has(findings []Finding, cat Category, ns, name string) bool {
	for _, f := range findings {
		if f.Category == cat && f.Namespace == ns && f.Name == name {
			return true
		}
	}
	return false
}

func TestOrphanedServices(t *testing.T) {
	in := Inputs{
		Services: []corev1.Service{
			svc("shop", "live", corev1.ServiceTypeClusterIP, map[string]string{"app": "live"}),
			svc("shop", "dead", corev1.ServiceTypeClusterIP, map[string]string{"app": "dead"}),
			svc("shop", "headless", corev1.ServiceTypeClusterIP, nil),                       // no selector → skipped
			svc("shop", "ext", corev1.ServiceTypeExternalName, map[string]string{"x": "y"}), // ExternalName → skipped
		},
		Pods: []corev1.Pod{pod("shop", "live-1", map[string]string{"app": "live"})},
	}
	f := Detect(in)
	if !has(f, OrphanedService, "shop", "dead") {
		t.Error("expected 'dead' service flagged as orphaned")
	}
	if has(f, OrphanedService, "shop", "live") {
		t.Error("'live' service has a matching pod; must not be flagged")
	}
	if has(f, OrphanedService, "shop", "headless") || has(f, OrphanedService, "shop", "ext") {
		t.Error("selector-less / ExternalName services must be skipped")
	}
}

func TestUnusedPVCs(t *testing.T) {
	in := Inputs{
		Pods: []corev1.Pod{pod("data", "user", nil, "in-use")},
		PVCs: []corev1.PersistentVolumeClaim{
			pvc("data", "in-use", "10Gi"),
			pvc("data", "orphan", "50Gi"),
		},
		StoragePerGiBMonth: 0.10,
	}
	f := Detect(in)
	if has(f, UnusedPVC, "data", "in-use") {
		t.Error("mounted PVC must not be flagged")
	}
	var orphan *Finding
	for i := range f {
		if f[i].Category == UnusedPVC && f[i].Name == "orphan" {
			orphan = &f[i]
		}
	}
	if orphan == nil {
		t.Fatal("expected 'orphan' PVC flagged")
	}
	if orphan.MonthlyCost < 4.9 || orphan.MonthlyCost > 5.1 { // 50Gi * $0.10
		t.Errorf("orphan cost = %.2f, want ~5.00", orphan.MonthlyCost)
	}
	if orphan.Detail != "50Gi" {
		t.Errorf("orphan detail = %q, want 50Gi", orphan.Detail)
	}
}

func TestIdleNamespaces(t *testing.T) {
	in := Inputs{
		Namespaces: []corev1.Namespace{
			{ObjectMeta: metav1.ObjectMeta{Name: "busy"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "empty"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}}, // system → never idle
		},
		Deployments: []appsv1.Deployment{
			{ObjectMeta: metav1.ObjectMeta{Namespace: "busy", Name: "api"}, Spec: appsv1.DeploymentSpec{Replicas: i32(2)}},
		},
	}
	f := Detect(in)
	if !has(f, IdleNamespace, "", "empty") {
		t.Error("expected 'empty' namespace flagged as idle")
	}
	if has(f, IdleNamespace, "", "busy") {
		t.Error("'busy' has a live deployment; must not be flagged")
	}
	if has(f, IdleNamespace, "", "kube-system") {
		t.Error("system namespaces must never be flagged idle")
	}
}

func TestZombieWorkloads(t *testing.T) {
	in := Inputs{
		Deployments: []appsv1.Deployment{
			{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "batch"}, Spec: appsv1.DeploymentSpec{Replicas: i32(0)}},
			{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "api"}, Spec: appsv1.DeploymentSpec{Replicas: i32(3)}},
		},
		StatefulSets: []appsv1.StatefulSet{
			{ObjectMeta: metav1.ObjectMeta{Namespace: "ops", Name: "old-cache"}, Spec: appsv1.StatefulSetSpec{Replicas: i32(0)}},
		},
	}
	f := Detect(in)
	if !has(f, ZombieWorkload, "shop", "batch") || !has(f, ZombieWorkload, "ops", "old-cache") {
		t.Error("expected scaled-to-0 Deployment + StatefulSet flagged as zombies")
	}
	if has(f, ZombieWorkload, "shop", "api") {
		t.Error("running deployment must not be flagged")
	}
}

func TestTotalMonthlyCost(t *testing.T) {
	f := []Finding{{MonthlyCost: 5}, {MonthlyCost: 18}, {MonthlyCost: 0}}
	if got := TotalMonthlyCost(f); got != 23 {
		t.Errorf("total = %.2f, want 23", got)
	}
}
