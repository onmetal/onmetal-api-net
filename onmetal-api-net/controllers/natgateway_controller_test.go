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
	"github.com/onmetal/onmetal-api-net/apiutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("NATGatewayController", func() {
	ns := SetupTest()
	network := SetupNetwork(ns)

	It("should reconcile the NAT gateway destinations", func(ctx SpecContext) {
		By("creating a public IP")
		publicIP := &v1alpha1.PublicIP{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "public-ip-",
			},
			Spec: v1alpha1.PublicIPSpec{
				IPFamily: corev1.IPv4Protocol,
			},
		}
		Expect(k8sClient.Create(ctx, publicIP)).To(Succeed())

		By("waiting for the public IP to be allocated")
		Eventually(Object(publicIP)).Should(Satisfy(apiutils.IsPublicIPAllocated))

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
				PublicIPRefs: []corev1.LocalObjectReference{
					{Name: publicIP.Name},
				},
			},
		}
		Expect(k8sClient.Create(ctx, natGateway)).To(Succeed())

		By("waiting for the NAT gateway to report the public IPs")
		Eventually(Object(natGateway)).Should(HaveField("Status.IPs", Equal([]v1alpha1.IP{*publicIP.Spec.IP})))

		By("creating a network interface that is a target to the NAT gateway")
		nic := &v1alpha1.NetworkInterface{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "nic-",
			},
			Spec: v1alpha1.NetworkInterfaceSpec{
				NetworkRef:   corev1.LocalObjectReference{Name: network.Name},
				IPFamilies:   []corev1.IPFamily{corev1.IPv4Protocol},
				PartitionRef: corev1.LocalObjectReference{Name: "my-partition"},
				IPs: []v1alpha1.NetworkInterfaceIP{
					{IP: v1alpha1.MustParseNewIP("10.0.0.1")},
				},
			},
		}
		Expect(k8sClient.Create(ctx, nic)).To(Succeed())

		By("waiting for the network interface config to get a NAT IP")
		nicCfg := &v1alpha1.NetworkInterfaceConfig{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: ns.Name,
				Name:      nic.Name,
			},
		}
		Eventually(Object(nicCfg)).Should(
			HaveField("ExternalIPs", []v1alpha1.ExternalIPConfig{
				{
					IPFamily: corev1.IPv4Protocol,
					SourceRef: &v1alpha1.SourceRef{
						Kind: "NATGateway",
						Name: natGateway.Name,
						UID:  natGateway.UID,
					},
					NATIP: &v1alpha1.NATIP{
						IP:      *publicIP.Spec.IP,
						Port:    1024,
						EndPort: 1087,
					},
				},
			}),
		)

		By("observing the used NAT gateway NAT IPs")
		Eventually(Object(natGateway)).Should(HaveField("Status.UsedNATIPs", int64(1)))
	})
})
