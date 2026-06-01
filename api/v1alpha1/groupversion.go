// Package v1alpha1 contains the typed, versioned API for kubetidy's custom resources
// (UsageProfile, ClusterUsageSummary, Recommendation). The types here are annotated with
// kubebuilder markers; controller-gen generates the CRD OpenAPI schema (config/crd) and the
// DeepCopy methods (zz_generated.deepcopy.go) from them.
//
// External consumers (Cluster API, dashboards, a future LLM recommender) import this package
// to read kubetidy data through a stable, validated contract.
//
// +kubebuilder:object:generate=true
// +groupName=kubetidy.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupVersion is the API group and version for all kubetidy custom resources.
var GroupVersion = schema.GroupVersion{Group: "kubetidy.io", Version: "v1alpha1"}

// GroupVersionResource coordinates for the dynamic client, so existing dynamic-client call
// sites can reference them without adopting a typed clientset.
var (
	// UsageProfileGVR is the GroupVersionResource for UsageProfile objects.
	UsageProfileGVR = GroupVersion.WithResource("usageprofiles")
	// ClusterUsageSummaryGVR is the GroupVersionResource for ClusterUsageSummary objects.
	ClusterUsageSummaryGVR = GroupVersion.WithResource("clusterusagesummaries")
	// RecommendationGVR is the GroupVersionResource for Recommendation objects.
	RecommendationGVR = GroupVersion.WithResource("recommendations")
)

// SchemeBuilder registers this group/version's types into a runtime scheme. It uses the
// apimachinery SchemeBuilder (not controller-runtime's deprecated helper) so this api package
// keeps minimal dependencies and is easy for external consumers to import.
var SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

// AddToScheme adds all v1alpha1 types to the given scheme.
var AddToScheme = SchemeBuilder.AddToScheme

func addKnownTypes(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion,
		&UsageProfile{}, &UsageProfileList{},
		&ClusterUsageSummary{}, &ClusterUsageSummaryList{},
		&Recommendation{}, &RecommendationList{},
	)
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}
