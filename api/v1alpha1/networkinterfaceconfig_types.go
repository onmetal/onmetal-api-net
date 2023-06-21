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
	Network NetworkConfig `json:"network"`
	// Node specifies the node configuration.
	Node NodeConfig `json:"node"`
	// IPs are the internal IPs of the network interface.
	IPs []IP `json:"ips"`
	// Prefixes are the prefixes of the network interface.
	Prefixes []IPPrefix `json:"prefixes,omitempty"`
	// +listType=map
	// +listMapKey=ipFamily
	ExternalIPs []ExternalIPConfig `json:"externalIPs,omitempty"`
}

type NetworkConfig struct {
	// Name is the name of the network.
	Name string `json:"name"`
	// VNI is the vni of the network.
	VNI int32 `json:"vni"`
}

type NodeConfig struct {
	// Name is the name of the node.
	Name string `json:"name"`
	// Partition is the partition the node is located in.
	Partition string `json:"partition"`
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
