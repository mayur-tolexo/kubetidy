package operator

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"

	"github.com/kubetidy/kubetidy/internal/apis/usageprofile"
)

// dynamicStore is the production Store: it persists UsageProfile objects as CRDs via the
// dynamic client. It upserts (create-or-update) so the operator can run idempotently every
// tick without tracking which profiles already exist.
type dynamicStore struct {
	client dynamic.Interface
}

// NewDynamicStore builds a Store backed by the dynamic client.
func NewDynamicStore(client dynamic.Interface) Store {
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
