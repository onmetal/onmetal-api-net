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
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("NetworkInterfaceController", func() {
	ns := SetupTest()
	network := SetupNetwork(ns)

	It("should apply a network interface config for a network interface", func(ctx SpecContext) {
		By("creating a public IP for the network interface")
		nicPublicIP := &v1alpha1.PublicIP{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "public-ip-",
			},
			Spec: v1alpha1.PublicIPSpec{
				IPFamily: corev1.IPv6Protocol,
			},
		}
		Expect(k8sClient.Create(ctx, nicPublicIP)).To(Succeed())

		By("waiting for the network interface public IP to be allocated")
		Eventually(Object(nicPublicIP)).Should(BeAllocatedPublicIP())

		By("creating a public IP for the NAT gateway")
		natGatewayPublicIP := &v1alpha1.PublicIP{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "public-ip-",
			},
			Spec: v1alpha1.PublicIPSpec{
				IPFamily: corev1.IPv4Protocol,
			},
		}
		Expect(k8sClient.Create(ctx, natGatewayPublicIP)).To(Succeed())

		By("waiting for the NAT gateway public IP to be allocated")
		Eventually(Object(natGatewayPublicIP)).Should(BeAllocatedPublicIP())

		By("creating a NAT gateway")
		natGateway := &v1alpha1.NATGateway{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "nat-gateway-",
			},
			Spec: v1alpha1.NATGatewaySpec{
				NetworkRef: corev1.LocalObjectReference{Name: network.Name},
				IPFamily:   corev1.IPv4Protocol,
				PublicIPRefs: []corev1.LocalObjectReference{
					{Name: natGatewayPublicIP.Name},
				},
				PortsPerNetworkInterface: 64,
			},
		}
		Expect(k8sClient.Create(ctx, natGateway)).To(Succeed())

		By("creating a network interface")
		nic := &v1alpha1.NetworkInterface{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "nic-",
			},
			Spec: v1alpha1.NetworkInterfaceSpec{
				NetworkRef: corev1.LocalObjectReference{Name: network.Name},
				IPFamilies: []corev1.IPFamily{corev1.IPv6Protocol, corev1.IPv4Protocol},
				IPs: []v1alpha1.NetworkInterfaceIP{
					{
						IP: v1alpha1.MustParseNewIP("ffff::0001"),
					},
					{
						IP: v1alpha1.MustParseNewIP("10.0.0.1"),
					},
				},
				PartitionRef: corev1.LocalObjectReference{Name: "my-partition"},
				PublicIPRefs: []v1alpha1.NetworkInterfacePublicIPRef{
					{
						IPFamily: corev1.IPv6Protocol,
						Name:     nicPublicIP.Name,
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, nic)).To(Succeed())

		By("waiting for the network interface config to be up-to-date")
		nicCfg := &v1alpha1.NetworkInterfaceConfig{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: ns.Name,
				Name:      nic.Name,
			},
		}
		Eventually(Object(nicCfg)).Should(SatisfyAll(
			BeControlledBy(nic),
			HaveField("ExternalIPs", []v1alpha1.ExternalIPConfig{
				{
					IPFamily: corev1.IPv4Protocol,
					SourceRef: &v1alpha1.SourceRef{
						Kind: "NATGateway",
						Name: natGateway.Name,
						UID:  natGateway.UID,
					},
					NATIP: &v1alpha1.NATIP{
						IP:      *natGatewayPublicIP.Spec.IP,
						Port:    1024,
						EndPort: 1087,
					},
				},
				{
					IPFamily: corev1.IPv6Protocol,
					SourceRef: &v1alpha1.SourceRef{
						Kind: "PublicIP",
						Name: nicPublicIP.Name,
						UID:  nicPublicIP.UID,
					},
					PublicIP: nicPublicIP.Spec.IP,
				},
			})),
		)

		By("deleting the network interface public IP ref")
		Eventually(Update(nic, func() {
			nic.Spec.PublicIPRefs = nil
		})).Should(Succeed())

		By("waiting for the network interface to only report the NAT IP")
		Eventually(Object(nicCfg)).Should(HaveField("ExternalIPs", Equal([]v1alpha1.ExternalIPConfig{
			{
				IPFamily: corev1.IPv4Protocol,
				SourceRef: &v1alpha1.SourceRef{
					Kind: "NATGateway",
					Name: natGateway.Name,
					UID:  natGateway.UID,
				},
				NATIP: &v1alpha1.NATIP{
					IP:      *natGatewayPublicIP.Spec.IP,
					Port:    1024,
					EndPort: 1087,
				},
			},
		})))
	})
})
