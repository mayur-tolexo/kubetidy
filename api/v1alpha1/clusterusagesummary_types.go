package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkloadTarget is one workload's contribution to the cluster rollup: the improvement
// opportunity dashboards and Cluster API integrations surface per workload.
type WorkloadTarget struct {
	// TargetRef identifies the workload.
	TargetRef TargetRef `json:"targetRef"`
	// Namespace is the workload's namespace.
	Namespace string `json:"namespace"`
	// MonthlySavings is the estimated $/month recoverable by rightsizing this workload.
	MonthlySavings float64 `json:"monthlySavings"`
	// Confidence is the 0..100 confidence in this workload's recommendation.
	// +optional
	Confidence int `json:"confidence,omitempty"`
}

// ClusterUsageSummarySpec selects what the summary covers. An empty Scope means the whole
// cluster.
type ClusterUsageSummarySpec struct {
	// Scope optionally narrows the summary (e.g. a namespace). Empty = whole cluster.
	// +optional
	Scope string `json:"scope,omitempty"`
}

// ClusterUsageSummaryStatus is the operator-maintained per-cluster rollup that external
// consumers (dashboards, Cluster API) read for a usage/cost/improvement view.
type ClusterUsageSummaryStatus struct {
	// GeneratedAt is when this summary was computed (RFC3339).
	// +optional
	GeneratedAt string `json:"generatedAt,omitempty"`
	// EfficiencyScore is the 0..100 cluster efficiency score.
	// +optional
	EfficiencyScore int `json:"efficiencyScore,omitempty"`
	// WorkloadCount is the number of workloads considered.
	// +optional
	WorkloadCount int `json:"workloadCount,omitempty"`
	// TotalMonthlyCost is the estimated current spend ($/month) across covered workloads.
	// +optional
	TotalMonthlyCost float64 `json:"totalMonthlyCost,omitempty"`
	// WastedMonthlyCost is the estimated recoverable spend ($/month).
	// +optional
	WastedMonthlyCost float64 `json:"wastedMonthlyCost,omitempty"`
	// TopTargets lists the highest-impact rightsizing opportunities, ranked by savings.
	// +optional
	TopTargets []WorkloadTarget `json:"topTargets,omitempty"`
	// Extensions is an open key/value area for experimental or vendor data.
	// +optional
	Extensions map[string]string `json:"extensions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=cus
// +kubebuilder:printcolumn:name="Score",type=integer,JSONPath=`.status.efficiencyScore`
// +kubebuilder:printcolumn:name="Wasted/mo",type=number,JSONPath=`.status.wastedMonthlyCost`
// +kubebuilder:printcolumn:name="Workloads",type=integer,JSONPath=`.status.workloadCount`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ClusterUsageSummary is a per-cluster rollup of usage, cost, and improvement opportunities —
// the stable, typed view external products read for a per-cluster dashboard.
type ClusterUsageSummary struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterUsageSummarySpec   `json:"spec,omitempty"`
	Status ClusterUsageSummaryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterUsageSummaryList is a list of ClusterUsageSummary.
type ClusterUsageSummaryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterUsageSummary `json:"items"`
}
