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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("MetalnetNodeController", func() {
	It("should reconcile the metalnet nodes with the nodes", func(ctx SpecContext) {
		By("creating a metalnet node")
		metalnetNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "metalnet-",
			},
		}
		Expect(k8sClient.Create(ctx, metalnetNode)).To(Succeed())
		DeferCleanup(DeleteIfExists(metalnetNode))

		By("waiting for the metalnet node to have a finalizer")
		Eventually(Object(metalnetNode)).Should(HaveField("Finalizers", []string{metalnetNodeFinalizer}))

		By("waiting for the corresponding node to appear")
		node := &v1alpha1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: PartitionNodeName(partitionName, metalnetNode.Name),
			},
		}
		Eventually(Get(node)).Should(Succeed())

		By("inspecting the node")
		Expect(node.Spec.PartitionRef).To(Equal(corev1.LocalObjectReference{Name: partitionName}))

		By("deleting the metalnet node")
		Expect(k8sClient.Delete(ctx, metalnetNode)).To(Succeed())

		By("waiting for the node to be gone")
		Eventually(Get(node)).Should(Satisfy(apierrors.IsNotFound))
	})
})
