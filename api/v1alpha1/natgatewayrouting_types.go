package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// +kubebuilder:object:root=true

// NATGatewayRouting is the schema for the natgatewayroutings API.
type NATGatewayRouting struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	IPFamily corev1.IPFamily `json:"ipFamily"`

	// IPs are the IPs the routing is for.
	IPs []IP `json:"ips,omitempty"`

	Destinations []NATGatewayDestination `json:"destinations,omitempty"`
}

type NATGatewayDestination struct {
	Name string    `json:"name"`
	UID  types.UID `json:"uid"`

	NATIP NATIP `json:"natIP"`

	// NodeRef references the node the destination network interface is on.
	NodeRef corev1.LocalObjectReference `json:"nodeRef"`
}

// +kubebuilder:object:root=true

// NATGatewayRoutingList contains a list of NATGatewayRouting.
type NATGatewayRoutingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NATGatewayRouting `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NATGatewayRouting{}, &NATGatewayRoutingList{})
}
