package operator

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"

	"github.com/kubetidy/kubetidy/api/v1alpha1"
	"github.com/kubetidy/kubetidy/internal/apis/usageprofile"
)

// SummaryNamespace and SummaryName are the fixed location of the singleton ClusterUsageSummary
// the operator maintains (one per cluster, in the operator's namespace).
const (
	SummaryNamespace = "kubetidy-system"
	SummaryName      = "cluster"
)

// dynamicStore is the production Store: it persists UsageProfile objects as CRDs via the
// dynamic client. It upserts (create-or-update) so the operator can run idempotently every
// tick without tracking which profiles already exist.
type dynamicStore struct {
	client dynamic.Interface
}

// ProfileStore is the full persistence surface the dynamic store provides: UsageProfile
// reads/writes (Store) plus the ClusterUsageSummary rollup (SummaryWriter).
type ProfileStore interface {
	Store
	SummaryWriter
}

// NewDynamicStore builds a dynamic-client-backed ProfileStore.
func NewDynamicStore(client dynamic.Interface) ProfileStore {
	return &dynamicStore{client: client}
}

// Get fetches a UsageProfile. A not-found result returns ok=false with no error, so callers can
// distinguish "absent" from "failed".
func (s *dynamicStore) Get(ctx context.Context, namespace, name string) (usageprofile.UsageProfile, bool, error) {
	obj, err := s.client.
		Resource(usageprofile.GroupVersionResource()).
		Namespace(namespace).
		Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return usageprofile.UsageProfile{}, false, nil
	}
	if err != nil {
		return usageprofile.UsageProfile{}, false, fmt.Errorf("operator: get usageprofile %s/%s: %w", namespace, name, err)
	}
	return usageprofile.FromUnstructured(obj), true, nil
}

// Save upserts a UsageProfile: it creates the object if absent, otherwise updates it in place
// (preserving the server's resourceVersion to avoid conflicts).
func (s *dynamicStore) Save(ctx context.Context, profile usageprofile.UsageProfile) error {
	res := s.client.Resource(usageprofile.GroupVersionResource()).Namespace(profile.Namespace)

	existing, err := res.Get(ctx, profile.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := res.Create(ctx, profile.ToUnstructured(), metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("operator: create usageprofile %s/%s: %w", profile.Namespace, profile.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("operator: get-before-update usageprofile %s/%s: %w", profile.Namespace, profile.Name, err)
	}

	// Carry the resourceVersion forward so the update is accepted.
	updated := profile.ToUnstructured()
	updated.SetResourceVersion(existing.GetResourceVersion())
	if _, err := res.Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("operator: update usageprofile %s/%s: %w", profile.Namespace, profile.Name, err)
	}
	return nil
}

// SaveSummary upserts the singleton ClusterUsageSummary (named "cluster" in kubetidy-system)
// and writes the rollup into its status. It satisfies the SummaryWriter interface.
func (s *dynamicStore) SaveSummary(ctx context.Context, status v1alpha1.ClusterUsageSummaryStatus) error {
	res := s.client.Resource(v1alpha1.ClusterUsageSummaryGVR).Namespace(SummaryNamespace)

	cus := &v1alpha1.ClusterUsageSummary{}
	cus.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("ClusterUsageSummary"))
	cus.Name = SummaryName
	cus.Namespace = SummaryNamespace
	cus.Status = status

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cus)
	if err != nil {
		return fmt.Errorf("operator: encoding cluster summary: %w", err)
	}
	u := &unstructured.Unstructured{Object: obj}

	existing, err := res.Get(ctx, SummaryName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		created, cerr := res.Create(ctx, u, metav1.CreateOptions{})
		if cerr != nil {
			return fmt.Errorf("operator: create cluster summary: %w", cerr)
		}
		// Status is a subresource: set it after create.
		created.Object["status"] = u.Object["status"]
		if _, uerr := res.UpdateStatus(ctx, created, metav1.UpdateOptions{}); uerr != nil {
			return fmt.Errorf("operator: set cluster summary status: %w", uerr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("operator: get-before-update cluster summary: %w", err)
	}

	existing.Object["status"] = u.Object["status"]
	if _, err := res.UpdateStatus(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("operator: update cluster summary status: %w", err)
	}
	return nil
}
