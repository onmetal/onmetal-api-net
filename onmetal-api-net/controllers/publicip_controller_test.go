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
	"github.com/onsi/gomega/gcustom"
	"github.com/onsi/gomega/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("PublicIPController", func() {
	ns := SetupTest()

	It("should allocate a public ip", func(ctx SpecContext) {
		By("creating a public ip")
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

		By("waiting for the public ip to be allocated")
		Eventually(Object(publicIP)).Should(BeAllocatedPublicIP())
	})

	PIt("should mark public ips as pending if they can't be allocated and allocate them as soon as there's space", func(ctx SpecContext) {
		By("creating public ips until we run out of addresses")
		publicIPKeys := make([]client.ObjectKey, NoOfIPv4Addresses)
		for i := 0; i < NoOfIPv4Addresses; i++ {
			publicIP := &v1alpha1.PublicIP{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:    ns.Name,
					GenerateName: "block-public-ip-",
				},
				Spec: v1alpha1.PublicIPSpec{
					IPFamily: corev1.IPv4Protocol,
				},
			}
			Expect(k8sClient.Create(ctx, publicIP)).To(Succeed())
			publicIPKeys[i] = client.ObjectKeyFromObject(publicIP)

			By("waiting for the public ip to be allocated")
			Eventually(Object(publicIP)).Should(BeAllocatedPublicIP())
		}

		By("creating another public ip")
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

		By("asserting the public IP will not be allocated")
		Consistently(Object(publicIP)).Should(Not(BeAllocatedPublicIP()))

		By("deleting one of the original public ips")
		Expect(k8sClient.Delete(ctx, &v1alpha1.PublicIP{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: publicIPKeys[0].Namespace,
				Name:      publicIPKeys[0].Name,
			},
		})).To(Succeed())

		By("waiting for the ip to be allocated")
		Eventually(Object(publicIP)).Should(BeAllocatedPublicIP())
	})
})

func BeAllocatedPublicIP() types.GomegaMatcher {
	return gcustom.MakeMatcher(func(publicIP *v1alpha1.PublicIP) (bool, error) {
		return apiutils.IsPublicIPAllocated(publicIP), nil
	}).WithTemplate("Expected\n{{ .FormattedActual}}\n{{.To}} be allocated")
}
