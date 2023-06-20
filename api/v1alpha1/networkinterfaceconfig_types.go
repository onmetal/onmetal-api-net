package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

type NetworkInterfaceConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Network specifies the network configuration.
	Network  NetworkConfig `json:"network"`
	IPs      []IP          `json:"ips"`
	Prefixes []IPPrefix    `json:"prefixes,omitempty"`
	// +listType=map
	// +listMapKey=ipFamily
	ExternalIPs []ExternalIPConfig `json:"externalIPs,omitempty"`
}

type NetworkConfig struct {
	Name string `json:"name"`
	VNI  int32  `json:"vni"`
}

type ExternalIPConfig struct {
	IPFamily  corev1.IPFamily `json:"ipFamily"`
	SourceRef *SourceRef      `json:"sourceRef,omitempty"`
	PublicIP  *IP             `json:"publicIP,omitempty"`
	NATIP     *NATIP          `json:"natIP,omitempty"`
}

// +kubebuilder:object:root=true

// NetworkInterfaceConfigList contains a list of NetworkInterfaceConfig.
type NetworkInterfaceConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkInterfaceConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NetworkInterfaceConfig{}, &NetworkInterfaceConfigList{})
}
