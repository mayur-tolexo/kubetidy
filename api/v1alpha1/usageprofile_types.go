package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TargetRef identifies the workload a kubetidy resource describes.
type TargetRef struct {
	// Kind is the workload controller kind (Deployment, StatefulSet, DaemonSet).
	Kind string `json:"kind"`
	// Name is the workload's name.
	Name string `json:"name"`
}

// MetricHistory is the recorded usage for one metric (CPU or memory) of one container: the
// summary percentiles plus the encoded decaying-histogram state used to rehydrate exactly.
type MetricHistory struct {
	// P50 is the median observed value (CPU in millicores, memory in bytes).
	P50 float64 `json:"p50"`
	// P95 is the 95th-percentile observed value.
	P95 float64 `json:"p95"`
	// Max is the largest observed value.
	Max float64 `json:"max"`
	// Histogram is the base64-encoded decaying-histogram snapshot, so the operator can resume
	// exact percentile tracking after a restart. Consumers needing only summaries can ignore it.
	// +optional
	Histogram string `json:"histogram,omitempty"`
}

// ContainerHistory holds the recorded history for a single named container.
type ContainerHistory struct {
	// Name is the container name.
	Name string `json:"name"`
	// CPU is the CPU usage history (millicores).
	CPU MetricHistory `json:"cpu"`
	// Memory is the memory usage history (bytes).
	Memory MetricHistory `json:"memory"`
}

// UsageProfileSpec is the small, user/operator-set part of a UsageProfile.
type UsageProfileSpec struct {
	// TargetRef identifies the workload this profile describes.
	TargetRef TargetRef `json:"targetRef"`
}

// UsageProfileStatus is the operator-maintained recorded history.
type UsageProfileStatus struct {
	// ObservedSince is when the operator first recorded usage for this workload (RFC3339).
	// +optional
	ObservedSince string `json:"observedSince,omitempty"`
	// LastUpdated is the most recent checkpoint time (RFC3339).
	// +optional
	LastUpdated string `json:"lastUpdated,omitempty"`
	// SampleCount is the total number of usage samples folded into the history.
	// +optional
	SampleCount int64 `json:"sampleCount,omitempty"`
	// WindowSeconds is the observation window covered, in seconds.
	// +optional
	WindowSeconds float64 `json:"windowSeconds,omitempty"`
	// Containers holds the per-container recorded usage.
	// +optional
	Containers []ContainerHistory `json:"containers,omitempty"`
	// Extensions is an open key/value area for experimental or vendor data, so the resource
	// can be extended without a schema migration. Promote stable fields into the typed core.
	// +optional
	Extensions map[string]string `json:"extensions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=up
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetRef.name`
// +kubebuilder:printcolumn:name="Samples",type=integer,JSONPath=`.status.sampleCount`
// +kubebuilder:printcolumn:name="Window(s)",type=number,JSONPath=`.status.windowSeconds`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// UsageProfile records a workload's per-container resource usage over time, so kubetidy can
// produce Prometheus-grade rightsizing recommendations with no Prometheus.
type UsageProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UsageProfileSpec   `json:"spec,omitempty"`
	Status UsageProfileStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// UsageProfileList is a list of UsageProfile.
type UsageProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []UsageProfile `json:"items"`
}
