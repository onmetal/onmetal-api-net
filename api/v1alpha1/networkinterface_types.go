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
	"k8s.io/apimachinery/pkg/types"
)

type NetworkInterfaceSpec struct {
	// NodeRef references the node hosting the network interface.
	NodeRef corev1.LocalObjectReference `json:"nodeRef"`
	// NetworkRef references the network that the network interface is part of.
	NetworkRef corev1.LocalObjectReference `json:"networkRef"`
	// IPs specifies the internal IPs for the network interface.
	// TODO: Allow for ephemeral internal IPs to be requested.
	IPs []IP `json:"ips"`
	// Prefixes specifies the additional IP prefixes for the network interface.
	// TODO: Allow for ephemeral internal prefixes to be requested.
	Prefixes []IPPrefix `json:"prefixes,omitempty"`
	// PublicIPs specifies the public IPs for the network interface.
	PublicIPs []NetworkInterfacePublicIPRef `json:"publicIPs,omitempty"`
}

type NetworkInterfacePublicIPRef struct {
	// IPFamily is the IP family of the public IP.
	IPFamily corev1.IPFamily `json:"ipFamily"`
	// PublicIPRef references the public IP to allocate.
	// If empty or not present, this network interface is assumed to have an external process managing
	// the allocation of public IPs (via PublicIP's claimerRef), which onmetal-api-net will not modify.
	// Pointer to distinguish between empty and explicit zero.
	PublicIPRef *corev1.LocalObjectReference `json:"publicIPRef,omitempty"`
}

type ExternalIP struct {
	IP    *IP    `json:"ip,omitempty"`
	NATIP *NATIP `json:"natIP,omitempty"`
}

// NATIP is a NATed IP.
type NATIP struct {
	// IP is the NATed IP to use.
	IP IP `json:"ip"`
	// Port is the first port to use.
	Port int32 `json:"port"`
	// EndPort is the last port to use.
	EndPort int32 `json:"endPort"`
}

// SourceRef references a source providing a virtual or NAT IP.
type SourceRef struct {
	// Kind is the kind of the providing source.
	Kind string `json:"kind"`
	// Name is the name of the providing source.
	Name string `json:"name"`
	// UID is the UID of the providing source.
	UID types.UID `json:"uid"`
}

type NetworkInterfaceStatus struct {
	// IPs reports the current IPs of the network interface.
	IPs []IP `json:"ips,omitempty"`
	// Prefixes reports the current IPPrefixes of the network interface.
	Prefixes []IPPrefix `json:"prefixes,omitempty"`
	// ExternalIPs specifies the currently allocated external IPs.
	ExternalIPs []ExternalIP `json:"externalIPs,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Partition",type=string,JSONPath=`.spec.partitionName`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// NetworkInterface is the schema for the networkinterfaces API.
type NetworkInterface struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NetworkInterfaceSpec   `json:"spec,omitempty"`
	Status NetworkInterfaceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NetworkInterfaceList contains a list of NetworkInterface.
type NetworkInterfaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkInterface `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NetworkInterface{}, &NetworkInterfaceList{})
}
