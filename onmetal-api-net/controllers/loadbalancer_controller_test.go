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

var _ = Describe("LoadBalancerController", func() {
	ns := SetupTest()
	network := SetupNetwork(ns)

	It("should reconcile the load balancer destinations", func(ctx SpecContext) {
		By("creating a public IP")
		publicIP := &v1alpha1.PublicIP{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "public-ip-",
				Labels: map[string]string{
					"app": "web",
				},
			},
			Spec: v1alpha1.PublicIPSpec{
				IPFamily: corev1.IPv4Protocol,
			},
		}
		Expect(k8sClient.Create(ctx, publicIP)).To(Succeed())

		By("waiting for the public IP to be allocated")
		Eventually(Object(publicIP)).Should(Satisfy(apiutils.IsPublicIPAllocated))

		By("creating a load balancer")
		loadBalancer := &v1alpha1.LoadBalancer{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "load-balancer-",
			},
			Spec: v1alpha1.LoadBalancerSpec{
				Type:       v1alpha1.LoadBalancerTypePublic,
				NetworkRef: corev1.LocalObjectReference{Name: network.Name},
				NetworkInterfaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"foo": "bar",
					},
				},
				IPSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"app": "web",
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, loadBalancer)).To(Succeed())

		By("waiting for the load balancer to report the public IPs")
		Eventually(Object(loadBalancer)).Should(HaveField("Status.IPs", Equal([]v1alpha1.IP{*publicIP.Spec.IP})))

		By("creating a network interface that is a target to the load balancer")
		nic := &v1alpha1.NetworkInterface{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "nic-",
				Labels: map[string]string{
					"foo": "bar",
				},
			},
			Spec: v1alpha1.NetworkInterfaceSpec{
				NetworkRef:   corev1.LocalObjectReference{Name: network.Name},
				PartitionRef: corev1.LocalObjectReference{Name: "my-partition"},
				IPs: []v1alpha1.IP{
					v1alpha1.MustParseIP("10.0.0.1"),
				},
			},
		}
		Expect(k8sClient.Create(ctx, nic)).To(Succeed())

		By("waiting for the load balancer routing to include the network interface")
		loadBalancerRouting := &v1alpha1.LoadBalancerRouting{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: ns.Name,
				Name:      loadBalancer.Name,
			},
		}
		Eventually(Object(loadBalancerRouting)).Should(SatisfyAll(
			BeControlledBy(loadBalancer),
			HaveField("Destinations", Equal([]v1alpha1.LoadBalancerDestination{
				{
					Name:         nic.Name,
					UID:          nic.UID,
					PartitionRef: &nic.Spec.PartitionRef,
				},
			})),
		))
	})
})
