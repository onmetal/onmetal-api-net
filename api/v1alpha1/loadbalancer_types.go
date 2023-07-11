package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type LoadBalancerType string

const (
	LoadBalancerTypePublic   LoadBalancerType = "Public"
	LoadBalancerTypeInternal LoadBalancerType = "Internal"
)

type LoadBalancerSpec struct {
	// Type specifies the type of load balancer.
	Type LoadBalancerType `json:"type"`

	// NetworkRef references the network the load balancer is part of.
	NetworkRef corev1.LocalObjectReference `json:"networkRef"`

	// ReplicasPerPartition specifies the number of replicas for a load balancer.
	// Pointer to distinguish between zero and explicit zero.
	// +kubebuilder:default=1
	ReplicasPerPartition *int32 `json:"replicasPerPartition,omitempty"`

	// IPSelector selects the IPs to allocate for the load balancer.
	// If empty or not present, this load balancer is assumed to have an external process claiming
	// public IPs, which onmetal-api-net will not modify.
	IPSelector *metav1.LabelSelector `json:"ipSelector,omitempty"`

	// IPs are the internal IPs of the load balancer.
	// Can only be specified when Type is LoadBalancerTypeInternal.
	IPs []IP `json:"ips,omitempty"`

	// NetworkInterfaceSelector selects the network interfaces to target with this load balancer.
	// If empty or not present, this load balancer is assumed to have an external process managing
	// its routing, which onmetal-api-net will not modify.
	NetworkInterfaceSelector *metav1.LabelSelector `json:"networkInterfaceSelector,omitempty"`

	// Ports are the ports the load balancer should allow.
	// If empty, the load balancer allows all ports.
	Ports []LoadBalancerPort `json:"ports,omitempty"`
}

type LoadBalancerPort struct {
	// Protocol is the protocol the load balancer should allow.
	// If not specified, defaults to TCP.
	Protocol *corev1.Protocol `json:"protocol,omitempty"`
	// Port is the port to allow.
	Port int32 `json:"port"`
	// EndPort marks the end of the port range to allow.
	// If unspecified, only a single port, Port, will be allowed.
	EndPort *int32 `json:"endPort,omitempty"`
}

type LoadBalancerStatus struct {
	// IPs are the IPs used currently by the load balancer.
	IPs []IP `json:"ips,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// LoadBalancer is the schema for the loadbalancers API.
type LoadBalancer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LoadBalancerSpec   `json:"spec,omitempty"`
	Status LoadBalancerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// LoadBalancerList contains a list of LoadBalancer.
type LoadBalancerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LoadBalancer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LoadBalancer{}, &LoadBalancerList{})
}
