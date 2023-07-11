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
	metalnetv1alpha1 "github.com/onmetal/metalnet/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("LoadBalancerInstanceController", func() {
	const vni = 300
	ns := SetupTest()
	metalnetNode := SetupMetalnetNode()
	network := SetupNetwork(ns, vni)

	It("should reconcile the metalnet load balancers for the load balancer instance", func(ctx SpecContext) {
		By("creating a load balancer instance")
		loadBalancerInstance := &v1alpha1.LoadBalancerInstance{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "lb-inst-",
			},
			Spec: v1alpha1.LoadBalancerInstanceSpec{
				Type:         v1alpha1.LoadBalancerTypePublic,
				NetworkRef:   corev1.LocalObjectReference{Name: network.Name},
				PartitionRef: corev1.LocalObjectReference{Name: partitionName},
				NodeRef:      &corev1.LocalObjectReference{Name: PartitionNodeName(partitionName, metalnetNode.Name)},
				IPs: []v1alpha1.IP{
					v1alpha1.MustParseIP("10.0.0.1"),
					v1alpha1.MustParseIP("10.0.0.2"),
				},
			},
		}
		Expect(k8sClient.Create(ctx, loadBalancerInstance)).To(Succeed())

		By("waiting for the metalnet load balancers to appear")
		metalnetLoadBalancerList := &metalnetv1alpha1.LoadBalancerList{}
		Eventually(ObjectList(metalnetLoadBalancerList)).Should(HaveField("Items", ConsistOf(
			HaveField("Spec.IP", metalnetv1alpha1.MustParseIP("10.0.0.1")),
			HaveField("Spec.IP", metalnetv1alpha1.MustParseIP("10.0.0.2")),
		)))

		By("removing an IP from the load balancer instance")
		Eventually(Update(loadBalancerInstance, func() {
			loadBalancerInstance.Spec.IPs = []v1alpha1.IP{v1alpha1.MustParseIP("10.0.0.1")}
		})).Should(Succeed())

		By("waiting for the metalnet load balancer to have only a single element")
		Eventually(ObjectList(metalnetLoadBalancerList)).Should(HaveField("Items", ConsistOf(
			HaveField("Spec.IP", metalnetv1alpha1.MustParseIP("10.0.0.1")),
		)))
	})
})
