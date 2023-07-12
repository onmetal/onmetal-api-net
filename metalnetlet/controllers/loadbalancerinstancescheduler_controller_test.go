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
	"fmt"

	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	cclient "sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("LoadBalancerInstanceSchedulerController", func() {
	const vni = 300
	ns := SetupTest()
	network := SetupNetwork(ns, vni)

	Context("when a node is present", func() {
		metalnetNode := SetupMetalnetNode()

		It("should set the instance's node ref to the available node", func(ctx SpecContext) {
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
					IPs:          []v1alpha1.IP{v1alpha1.MustParseIP("10.0.0.1")},
				},
			}
			Expect(k8sClient.Create(ctx, loadBalancerInstance)).To(Succeed())

			By("waiting for the load balancer instance to be scheduled")
			Eventually(Object(loadBalancerInstance)).Should(HaveField("Spec.NodeRef", &corev1.LocalObjectReference{
				Name: PartitionNodeName(partitionName, metalnetNode.Name),
			}))
		})
	})

	Context("when no node is present", func() {
		It("leave the instance's node ref empty", func(ctx SpecContext) {
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
					IPs:          []v1alpha1.IP{v1alpha1.MustParseIP("10.0.0.1")},
				},
			}
			Expect(k8sClient.Create(ctx, loadBalancerInstance)).To(Succeed())

			By("waiting for the load balancer instance to be scheduled")
			Consistently(Object(loadBalancerInstance)).Should(HaveField("Spec.NodeRef", BeNil()))
		})
	})

	Context("when multiple nodes are present", func() {
		const nodeCt = 3

		nodes := make([]*corev1.Node, nodeCt)
		for i := range nodes {
			nodes[i] = SetupMetalnetNode()
		}

		FIt(fmt.Sprintf("should evenly spread out the load balancer instance over %d nodes", nodeCt), func(ctx SpecContext) {
			By(fmt.Sprintf("waiting for %d nodes to be available", nodeCt))
			Eventually(ObjectList(&v1alpha1.NodeList{})).Should(HaveField("Items", HaveLen(nodeCt)))

			const multiple = 3
			const instanceCt = nodeCt * multiple
			By(fmt.Sprintf("creating a multiple (%d) of %d instances", multiple, nodeCt))

			for i := 0; i < instanceCt; i++ {
				loadBalancerInstance := &v1alpha1.LoadBalancerInstance{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:    ns.Name,
						GenerateName: "lb-inst-",
					},
					Spec: v1alpha1.LoadBalancerInstanceSpec{
						Type:         v1alpha1.LoadBalancerTypePublic,
						NetworkRef:   corev1.LocalObjectReference{Name: network.Name},
						PartitionRef: corev1.LocalObjectReference{Name: partitionName},
						IPs:          []v1alpha1.IP{v1alpha1.MustParseIP("10.0.0.1")},
					},
				}
				Expect(k8sClient.Create(ctx, loadBalancerInstance)).To(Succeed())
			}

			By("asserting the instances have been evenly spread between the nodes")
			elements := make([]any, instanceCt)
			for i, node := range nodes {
				for j := 0; j < multiple; j++ {
					elements[i*multiple+j] = HaveField("Spec.NodeRef", &corev1.LocalObjectReference{
						Name: PartitionNodeName(partitionName, node.Name),
					})
				}
			}
			Eventually(ObjectList(&v1alpha1.LoadBalancerInstanceList{}, cclient.InNamespace(ns.Name))).
				Should(HaveField("Items", ConsistOf(elements...)))
		})
	})
})
