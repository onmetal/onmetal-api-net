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

package controllers

import (
	"github.com/onmetal/onmetal-api-net/api/core/v1alpha1"
	"github.com/onmetal/onmetal-api-net/apimachinery/api/net"
	. "github.com/onmetal/onmetal-api/utils/testing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("NATGatewayController", func() {
	ns := SetupNamespace(&k8sClient)
	network := SetupNetwork(ns)

	It("should correctly reconcile the NAT gateway", func(ctx SpecContext) {
		By("creating a NAT gateway")
		natGateway := &v1alpha1.NATGateway{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "nat-gateway-",
			},
			Spec: v1alpha1.NATGatewaySpec{
				IPFamily:                 corev1.IPv4Protocol,
				NetworkRef:               corev1.LocalObjectReference{Name: network.Name},
				PortsPerNetworkInterface: 64,
				IPs: []v1alpha1.NATGatewayIP{
					{Name: "ip-1"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, natGateway)).To(Succeed())
		natGatewayIP := natGateway.Spec.IPs[0].IP

		By("creating a network interface as potential target")
		nic := &v1alpha1.NetworkInterface{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "nic-",
			},
			Spec: v1alpha1.NetworkInterfaceSpec{
				NodeRef:    corev1.LocalObjectReference{Name: "my-node"},
				NetworkRef: corev1.LocalObjectReference{Name: network.Name},
				IPs:        []net.IP{net.MustParseIP("10.0.0.1")},
			},
		}
		Expect(k8sClient.Create(ctx, nic)).To(Succeed())

		By("waiting for the NAT table to be updated")
		natTable := &v1alpha1.NATTable{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: ns.Name,
				Name:      natGateway.Name,
			},
		}
		Eventually(Object(natTable)).Should(HaveField("IPs", ConsistOf(
			v1alpha1.NATIP{
				IP: natGatewayIP,
				Sections: []v1alpha1.NATIPSection{
					{
						IP:      net.MustParseIP("10.0.0.1"),
						Port:    1024,
						EndPort: 1087,
						TargetRef: &v1alpha1.NATTableIPTargetRef{
							UID:     nic.UID,
							Name:    nic.Name,
							NodeRef: nic.Spec.NodeRef,
						},
					},
				},
			},
		)))
	})
})
