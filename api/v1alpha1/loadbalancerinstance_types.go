package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type LoadBalancerInstanceSpec struct {
	// Type specifies the type of load balancer instance.
	Type LoadBalancerType `json:"type"`

	// NetworkRef references the network the load balancer instance is part of.
	NetworkRef corev1.LocalObjectReference `json:"networkRef"`

	// PartitionRef references the partition hosting the load balancer instance.
	PartitionRef corev1.LocalObjectReference `json:"partitionRef"`

	// NodeRef references the node hosting the load balancer instance.
	// If empty, a scheduler of the partition sets this.
	NodeRef *corev1.LocalObjectReference `json:"nodeRef,omitempty"`

	// IP are the IPs of the load balancer instance.
	IPs []IP `json:"ips,omitempty"`

	// Ports are the ports the load balancer instance should allow.
	// If empty, the load balancer instance allows all ports.
	Ports []LoadBalancerPort `json:"ports,omitempty"`
}

type LoadBalancerInstanceStatus struct {
	IPs            []IP   `json:"ips,omitempty"`
	CollisionCount *int32 `json:"collisionCount,omitempty"`
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
