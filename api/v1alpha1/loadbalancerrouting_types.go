package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// +kubebuilder:object:root=true

// LoadBalancerRouting is the schema for the loadbalancerroutings API.
type LoadBalancerRouting struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Destinations []LoadBalancerDestination `json:"destinations,omitempty"`
}

type LoadBalancerDestination struct {
	Name string    `json:"name"`
	UID  types.UID `json:"uid"`

	// PartitionRef references the partition the destination is on.
	PartitionRef *corev1.LocalObjectReference `json:"partitionRef"`
}

// +kubebuilder:object:root=true

// LoadBalancerRoutingList contains a list of LoadBalancerRouting.
type LoadBalancerRoutingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LoadBalancerRouting `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LoadBalancerRouting{}, &LoadBalancerRoutingList{})
}
