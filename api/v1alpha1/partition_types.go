package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PartitionSpec struct {
}

type PartitionStatus struct {
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status

// Partition is the schema for the partitions API.
type Partition struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PartitionSpec   `json:"spec,omitempty"`
	Status PartitionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PartitionList contains a list of Partition.
type PartitionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Partition `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Partition{}, &PartitionList{})
}
