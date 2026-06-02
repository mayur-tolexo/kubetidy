// Package kube handles client-go setup (kubeconfig/context resolution, like any kubectl
// plugin) and workload discovery.
package kube

import (
	"context"
	"fmt"

	"github.com/kubetidy/kubetidy/internal/model"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Clients bundles the Kubernetes API clients and resolved context metadata
// that the rest of kubetidy needs.
type Clients struct {
	Kube      kubernetes.Interface
	Metrics   metricsv.Interface
	Dynamic   dynamic.Interface
	Context   string
	Namespace string
}

// Load builds API clients from the user's kubeconfig, honoring the active
// context and namespace like a kubectl plugin would.
func Load(contextOverride, namespaceOverride string) (*Clients, error) {
	overrides := &clientcmd.ConfigOverrides{CurrentContext: contextOverride}
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		overrides,
	)

	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("kube: building rest config from kubeconfig: %w", err)
	}

	// kubetidy scans workloads concurrently and is read-only, so the client-go default rate
	// limit (QPS 5 / burst 10) throttles it hard and spams "client-side throttling" warnings.
	// Raise it: these are cheap GETs/LISTs against the API server, well within reason.
	restConfig.QPS = 50
	restConfig.Burst = 100

	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("kube: creating kubernetes client: %w", err)
	}

	metricsClient, err := metricsv.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("kube: creating metrics client: %w", err)
	}

	// The dynamic client reads/writes the UsageProfile CRD (kubetidy operator tier) without a
	// generated typed scheme.
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("kube: creating dynamic client: %w", err)
	}

	// Resolve the effective context name.
	contextName := contextOverride
	if contextName == "" {
		rawConfig, err := clientConfig.RawConfig()
		if err != nil {
			return nil, fmt.Errorf("kube: reading raw kubeconfig: %w", err)
		}
		contextName = rawConfig.CurrentContext
	}

	// Resolve the effective namespace. An explicit override wins; otherwise
	// fall back to the namespace bound to the active context.
	namespace := namespaceOverride
	if namespace == "" {
		ns, _, err := clientConfig.Namespace()
		if err != nil {
			return nil, fmt.Errorf("kube: resolving namespace: %w", err)
		}
		namespace = ns
	}

	return &Clients{
		Kube:      kubeClient,
		Metrics:   metricsClient,
		Dynamic:   dynamicClient,
		Context:   contextName,
		Namespace: namespace,
	}, nil
}

// Discover lists the workloads (Deployments, StatefulSets, DaemonSets) in the
// given namespace and converts them into the kubetidy domain model. An empty
// namespace means all namespaces.
func Discover(ctx context.Context, c *Clients, namespace string) ([]model.Workload, error) {
	if c == nil || c.Kube == nil {
		return nil, fmt.Errorf("kube: nil clients")
	}

	ns := namespace
	if ns == "" {
		ns = metav1.NamespaceAll
	}

	apps := c.Kube.AppsV1()
	var workloads []model.Workload

	deployments, err := apps.Deployments(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("kube: listing deployments: %w", err)
	}
	for i := range deployments.Items {
		d := &deployments.Items[i]
		replicas := int32(1)
		if d.Spec.Replicas != nil {
			replicas = *d.Spec.Replicas
		}
		workloads = append(workloads, model.Workload{
			Kind:       model.KindDeployment,
			Name:       d.Name,
			Namespace:  d.Namespace,
			Replicas:   replicas,
			Selector:   selectorFrom(d.Spec.Selector),
			Containers: containersFrom(d.Spec.Template.Spec.Containers),
		})
	}

	statefulSets, err := apps.StatefulSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("kube: listing statefulsets: %w", err)
	}
	for i := range statefulSets.Items {
		s := &statefulSets.Items[i]
		replicas := int32(1)
		if s.Spec.Replicas != nil {
			replicas = *s.Spec.Replicas
		}
		workloads = append(workloads, model.Workload{
			Kind:       model.KindStatefulSet,
			Name:       s.Name,
			Namespace:  s.Namespace,
			Replicas:   replicas,
			Selector:   selectorFrom(s.Spec.Selector),
			Containers: containersFrom(s.Spec.Template.Spec.Containers),
		})
	}

	daemonSets, err := apps.DaemonSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("kube: listing daemonsets: %w", err)
	}
	for i := range daemonSets.Items {
		ds := &daemonSets.Items[i]
		replicas := ds.Status.DesiredNumberScheduled
		if replicas == 0 {
			replicas = 1
		}
		workloads = append(workloads, model.Workload{
			Kind:       model.KindDaemonSet,
			Name:       ds.Name,
			Namespace:  ds.Namespace,
			Replicas:   replicas,
			Selector:   selectorFrom(ds.Spec.Selector),
			Containers: containersFrom(ds.Spec.Template.Spec.Containers),
		})
	}

	return workloads, nil
}

// selectorFrom extracts the MatchLabels map from a label selector, returning
// nil if there is no selector.
func selectorFrom(sel *metav1.LabelSelector) map[string]string {
	if sel == nil || len(sel.MatchLabels) == 0 {
		return nil
	}
	out := make(map[string]string, len(sel.MatchLabels))
	for k, v := range sel.MatchLabels {
		out[k] = v
	}
	return out
}

// containersFrom converts pod-template containers into the domain model,
// normalizing CPU to millicores and memory to bytes. Missing values are 0.
func containersFrom(containers []corev1.Container) []model.Container {
	if len(containers) == 0 {
		return nil
	}
	out := make([]model.Container, 0, len(containers))
	for i := range containers {
		ctr := &containers[i]
		out = append(out, model.Container{
			Name:     ctr.Name,
			Requests: amountsFrom(ctr.Resources.Requests),
			Limits:   amountsFrom(ctr.Resources.Limits),
		})
	}
	return out
}

// amountsFrom normalizes a resource list into model.ResourceAmounts.
func amountsFrom(list corev1.ResourceList) model.ResourceAmounts {
	var amounts model.ResourceAmounts
	if cpu, ok := list[corev1.ResourceCPU]; ok {
		amounts.CPUMillicores = cpu.MilliValue()
	}
	if mem, ok := list[corev1.ResourceMemory]; ok {
		amounts.MemoryBytes = mem.Value()
	}
	return amounts
}
