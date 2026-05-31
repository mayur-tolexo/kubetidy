package kube

import (
	"context"
	"sort"
	"testing"

	"github.com/kubetidy/kubetidy/internal/model"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func ptrInt32(i int32) *int32 { return &i }

func resList(cpu, mem string) corev1.ResourceList {
	rl := corev1.ResourceList{}
	if cpu != "" {
		rl[corev1.ResourceCPU] = resource.MustParse(cpu)
	}
	if mem != "" {
		rl[corev1.ResourceMemory] = resource.MustParse(mem)
	}
	return rl
}

func makeDeployment(ns, name string, replicas int32, sel map[string]string, ctrs []corev1.Container) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptrInt32(replicas),
			Selector: &metav1.LabelSelector{MatchLabels: sel},
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: ctrs}},
		},
	}
}

func makeStatefulSet(ns, name string, replicas int32, sel map[string]string, ctrs []corev1.Container) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptrInt32(replicas),
			Selector: &metav1.LabelSelector{MatchLabels: sel},
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: ctrs}},
		},
	}
}

func makeDaemonSet(ns, name string, desired int32, sel map[string]string, ctrs []corev1.Container) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: sel},
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: ctrs}},
		},
		Status: appsv1.DaemonSetStatus{DesiredNumberScheduled: desired},
	}
}

func TestDiscoverNormalization(t *testing.T) {
	depCtrs := []corev1.Container{
		{
			Name:      "web",
			Resources: corev1.ResourceRequirements{Requests: resList("250m", "128Mi"), Limits: resList("500m", "256Mi")},
		},
		{
			Name:      "sidecar",
			Resources: corev1.ResourceRequirements{Requests: resList("1", "1Gi")}, // no limits
		},
	}
	stsCtrs := []corev1.Container{
		{Name: "db", Resources: corev1.ResourceRequirements{Requests: resList("2", "2Gi"), Limits: resList("4", "4Gi")}},
	}
	dsCtrs := []corev1.Container{
		{Name: "agent", Resources: corev1.ResourceRequirements{}}, // missing => 0
	}

	objs := []interface{}{
		makeDeployment("ns1", "dep1", 3, map[string]string{"app": "web"}, depCtrs),
		makeStatefulSet("ns1", "sts1", 2, map[string]string{"app": "db"}, stsCtrs),
		makeDaemonSet("ns2", "ds1", 5, map[string]string{"app": "agent"}, dsCtrs),
	}
	clientset := fake.NewSimpleClientset()
	for _, o := range objs {
		switch v := o.(type) {
		case *appsv1.Deployment:
			if _, err := clientset.AppsV1().Deployments(v.Namespace).Create(context.Background(), v, metav1.CreateOptions{}); err != nil {
				t.Fatalf("create deployment: %v", err)
			}
		case *appsv1.StatefulSet:
			if _, err := clientset.AppsV1().StatefulSets(v.Namespace).Create(context.Background(), v, metav1.CreateOptions{}); err != nil {
				t.Fatalf("create statefulset: %v", err)
			}
		case *appsv1.DaemonSet:
			if _, err := clientset.AppsV1().DaemonSets(v.Namespace).Create(context.Background(), v, metav1.CreateOptions{}); err != nil {
				t.Fatalf("create daemonset: %v", err)
			}
		}
	}

	c := &Clients{Kube: clientset}

	// All namespaces.
	got, err := Discover(context.Background(), c, "")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 workloads, got %d", len(got))
	}

	byName := map[string]int{}
	for i, w := range got {
		byName[w.Name] = i
	}

	// Deployment checks.
	dep := got[byName["dep1"]]
	if dep.Kind != model.KindDeployment || dep.Namespace != "ns1" || dep.Replicas != 3 {
		t.Errorf("dep1 metadata wrong: %+v", dep)
	}
	if dep.Selector["app"] != "web" {
		t.Errorf("dep1 selector wrong: %+v", dep.Selector)
	}
	if len(dep.Containers) != 2 {
		t.Fatalf("dep1 expected 2 containers, got %d", len(dep.Containers))
	}
	web := dep.Containers[0]
	if web.Requests.CPUMillicores != 250 || web.Requests.MemoryBytes != 128*1024*1024 {
		t.Errorf("web requests wrong: %+v", web.Requests)
	}
	if web.Limits.CPUMillicores != 500 || web.Limits.MemoryBytes != 256*1024*1024 {
		t.Errorf("web limits wrong: %+v", web.Limits)
	}
	side := dep.Containers[1]
	if side.Requests.CPUMillicores != 1000 || side.Requests.MemoryBytes != 1024*1024*1024 {
		t.Errorf("sidecar requests wrong: %+v", side.Requests)
	}
	if side.Limits.CPUMillicores != 0 || side.Limits.MemoryBytes != 0 {
		t.Errorf("sidecar missing limits should be 0: %+v", side.Limits)
	}

	// StatefulSet checks.
	sts := got[byName["sts1"]]
	if sts.Kind != model.KindStatefulSet || sts.Replicas != 2 {
		t.Errorf("sts1 metadata wrong: %+v", sts)
	}
	if sts.Containers[0].Requests.CPUMillicores != 2000 || sts.Containers[0].Limits.MemoryBytes != 4*1024*1024*1024 {
		t.Errorf("sts1 container wrong: %+v", sts.Containers[0])
	}

	// DaemonSet checks: replicas from DesiredNumberScheduled, zero requests/limits.
	ds := got[byName["ds1"]]
	if ds.Kind != model.KindDaemonSet || ds.Namespace != "ns2" || ds.Replicas != 5 {
		t.Errorf("ds1 metadata wrong: %+v", ds)
	}
	if ds.Containers[0].Requests.CPUMillicores != 0 || ds.Containers[0].Requests.MemoryBytes != 0 {
		t.Errorf("ds1 missing requests should be 0: %+v", ds.Containers[0].Requests)
	}
}

func TestDiscoverDaemonSetZeroDesiredDefaultsToOne(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		makeDaemonSet("ns1", "ds-zero", 0, map[string]string{"k": "v"}, []corev1.Container{{Name: "c"}}),
	)
	c := &Clients{Kube: clientset}
	got, err := Discover(context.Background(), c, "ns1")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 workload, got %d", len(got))
	}
	if got[0].Replicas != 1 {
		t.Errorf("expected daemonset with zero desired to default to 1 replica, got %d", got[0].Replicas)
	}
}

func TestDiscoverNamespaceFiltering(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		makeDeployment("ns1", "a", 1, map[string]string{"x": "1"}, nil),
		makeDeployment("ns2", "b", 1, map[string]string{"x": "2"}, nil),
		makeStatefulSet("ns2", "c", 1, map[string]string{"x": "3"}, nil),
	)
	c := &Clients{Kube: clientset}

	got, err := Discover(context.Background(), c, "ns2")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	names := make([]string, 0, len(got))
	for _, w := range got {
		if w.Namespace != "ns2" {
			t.Errorf("namespace filter leaked: %+v", w)
		}
		names = append(names, w.Name)
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "b" || names[1] != "c" {
		t.Errorf("expected [b c] in ns2, got %v", names)
	}
}

func TestDiscoverNilClients(t *testing.T) {
	if _, err := Discover(context.Background(), nil, ""); err == nil {
		t.Errorf("expected error for nil clients")
	}
	if _, err := Discover(context.Background(), &Clients{}, ""); err == nil {
		t.Errorf("expected error for nil kube client")
	}
}
