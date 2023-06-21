// Copyright 2022 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type NATGatewaySpec struct {
	// IPFamily is the IP family of the NAT gateway.
	IPFamily corev1.IPFamily `json:"ipFamily"`

	// NetworkRef references the network the NAT gateway is part of.
	NetworkRef corev1.LocalObjectReference `json:"networkRef"`

	// PortsPerNetworkInterface specifies how many ports to allocate per network interface.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=64512
	// +kubebuilder:default=2048
	PortsPerNetworkInterface int32 `json:"portsPerNetworkInterface"`

	// IPSelector selects the IPs to allocate for the NAT gateway.
	// If empty or not present, this NAT gateway is assumed to have an external process claiming
	// public IPs, which onmetal-api-net will not modify.
	IPSelector *metav1.LabelSelector `json:"ipSelector,omitempty"`
}

type NATGatewayStatus struct {
	// IPs are the IPs currently in-use by the NAT gateway.
	IPs []IP `json:"ips,omitempty"`
	// UsedNATIPs is the number of NAT IPs in-use.
	UsedNATIPs int64 `json:"usedNATIPs,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NATGateway is the schema for the natgateways API.
type NATGateway struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NATGatewaySpec   `json:"spec,omitempty"`
	Status NATGatewayStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NATGatewayList contains a list of NATGateway.
type NATGatewayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NATGateway `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NATGateway{}, &NATGatewayList{})
}
