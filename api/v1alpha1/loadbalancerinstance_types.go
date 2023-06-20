package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type LoadBalancerInstanceSpec struct {
}

type LoadBalancerInstanceStatus struct {
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// LoadBalancerInstance is the schema for the loadbalancerinstances API.
type LoadBalancerInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LoadBalancerInstanceSpec   `json:"spec,omitempty"`
	Status LoadBalancerInstanceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// LoadBalancerInstanceList contains a list of LoadBalancerInstance.
type LoadBalancerInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LoadBalancerInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LoadBalancerInstance{}, &LoadBalancerInstanceList{})
}
