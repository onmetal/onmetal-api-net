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
	"github.com/google/uuid"
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("PublicIPGarbageCollectorController", func() {
	ns := SetupTest()
	network := SetupNetwork(ns)

	It("should release a public ip with a non-existent claimer", func(ctx SpecContext) {
		By("creating a public ip")
		publicIP := &v1alpha1.PublicIP{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "public-ip-",
			},
			Spec: v1alpha1.PublicIPSpec{
				IPFamily: corev1.IPv4Protocol,
				ClaimerRef: &v1alpha1.PublicIPClaimerRef{
					Kind: natGatewayKind,
					UID:  types.UID(uuid.NewString()),
					Name: "should-not-exist",
				},
			},
		}
		Expect(k8sClient.Create(ctx, publicIP)).To(Succeed())

		By("waiting for the public ip to be released")
		Eventually(Object(publicIP)).Should(HaveField("Spec.ClaimerRef", BeNil()))
	})

	It("should release a public IP whose owner got deleted", func(ctx SpecContext) {
		By("creating a network interface")
		nic := &v1alpha1.NetworkInterface{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "nic-",
			},
			Spec: v1alpha1.NetworkInterfaceSpec{
				NetworkRef: corev1.LocalObjectReference{Name: network.Name},
				IPs: []v1alpha1.IP{
					v1alpha1.MustParseIP("10.0.0.1"),
				},
				NodeRef: corev1.LocalObjectReference{Name: "my-node"},
			},
		}
		Expect(k8sClient.Create(ctx, nic)).To(Succeed())

		By("creating a public ip")
		publicIP := &v1alpha1.PublicIP{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "public-ip-",
			},
			Spec: v1alpha1.PublicIPSpec{
				IPFamily: corev1.IPv4Protocol,
				ClaimerRef: &v1alpha1.PublicIPClaimerRef{
					Kind: natGatewayKind,
					UID:  types.UID(uuid.NewString()),
					Name: "should-not-exist",
				},
			},
		}
		Expect(k8sClient.Create(ctx, publicIP)).To(Succeed())

		By("modifying the network interface to claim the public IP")
		Eventually(Update(nic, func() {
			nic.Spec.PublicIPRefs = []v1alpha1.NetworkInterfacePublicIPRef{
				{
					IPFamily: corev1.IPv4Protocol,
					Name:     publicIP.Name,
				},
			}
		})).Should(Succeed())

		By("waiting for the public ip to be allocated")
		Eventually(Object(publicIP)).Should(HaveField("Spec.ClaimerRef", &v1alpha1.PublicIPClaimerRef{
			Kind: networkInterfaceKind,
			Name: nic.Name,
			UID:  nic.UID,
		}))

		By("deleting the network interface")
		Expect(k8sClient.Delete(ctx, nic)).To(Succeed())

		By("waiting for the network interface to be gone")
		Eventually(Get(nic)).Should(Satisfy(apierrors.IsNotFound))

		By("waiting for the public ip to be released")
		Eventually(Object(publicIP)).Should(HaveField("Spec.ClaimerRef", BeNil()))
	})
})
