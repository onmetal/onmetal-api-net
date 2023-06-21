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
	"time"

	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	"github.com/onmetal/onmetal-api/utils/generic"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("NATGatewayAutoscalerController", func() {
	ns := SetupTest()
	network := SetupNetwork(ns)

	It("should add and remove public IPs depending on the demand", func(ctx SpecContext) {
		By("creating a NAT gateway")
		natGateway := &v1alpha1.NATGateway{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "nat-gateway-",
			},
			Spec: v1alpha1.NATGatewaySpec{
				IPFamily:                 corev1.IPv4Protocol,
				NetworkRef:               corev1.LocalObjectReference{Name: network.Name},
				PortsPerNetworkInterface: 64512,
			},
		}
		Expect(k8sClient.Create(ctx, natGateway)).To(Succeed())

		By("creating a NAT gateway autoscaler")
		natGatewayAutoscaler := &v1alpha1.NATGatewayAutoscaler{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "nat-gateway-as-",
			},
			Spec: v1alpha1.NATGatewayAutoscalerSpec{
				NATGatewayRef: corev1.LocalObjectReference{
					Name: natGateway.Name,
				},
				MinPublicIPs: generic.Pointer[int32](1),
				MaxPublicIPs: generic.Pointer[int32](3),
			},
		}
		Expect(k8sClient.Create(ctx, natGatewayAutoscaler)).To(Succeed())

		By("waiting for the NAT gateway to have a public IP")
		Eventually(Object(natGateway)).Should(HaveField("Status.IPs", HaveLen(1)))

		By("asserting there is only a single public IP in the namespace")
		publicIPList := &v1alpha1.PublicIPList{}
		Expect(k8sClient.List(ctx, publicIPList, client.InNamespace(ns.Name))).To(Succeed())
		Expect(publicIPList.Items).To(HaveLen(1))

		By("requesting more NAT IPs via network interfaces & configs")
		nics := make([]v1alpha1.NetworkInterface, 4)
		for i := range nics {
			nics[i] = v1alpha1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:    ns.Name,
					GenerateName: "nic-",
				},
				Spec: v1alpha1.NetworkInterfaceSpec{
					NodeRef:    corev1.LocalObjectReference{Name: "my-node"},
					NetworkRef: corev1.LocalObjectReference{Name: network.Name},
					IPs:        []v1alpha1.IP{v1alpha1.MustParseIP("10.0.0.1")},
				},
			}
			Expect(k8sClient.Create(ctx, &nics[i])).To(Succeed())
		}

		By("waiting for the NAT gateway to have three (maximum amount) public IPs")
		Eventually(Object(natGateway)).WithTimeout(10 * time.Second).Should(HaveField("Status.IPs", HaveLen(3)))

		By("asserting there are only two public IPs in the namespace")
		Expect(k8sClient.List(ctx, publicIPList, client.InNamespace(ns.Name))).To(Succeed())
		Expect(publicIPList.Items).To(HaveLen(3))

		By("deleting the network interfaces")
		for i := range nics {
			Expect(k8sClient.Delete(ctx, &nics[i])).To(Succeed())
		}

		By("waiting for the NAT gateway to have a single public IP again")
		Eventually(Object(natGateway)).WithTimeout(10 * time.Second).Should(HaveField("Status.IPs", HaveLen(1)))

		By("waiting for all public IPs to be one except one")
		Eventually(func(g Gomega) []v1alpha1.PublicIP {
			g.Expect(k8sClient.List(ctx, publicIPList, client.InNamespace(ns.Name))).To(Succeed())
			return publicIPList.Items
		}).Should(HaveLen(1))
	})
})
