package controllers

import (
	"context"

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

func SetupNetwork(namespace *corev1.Namespace) *v1alpha1.Network {
	network := &v1alpha1.Network{}

	BeforeEach(func(ctx SpecContext) {
		By("creating a network")
		*network = v1alpha1.Network{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    namespace.Name,
				GenerateName: "network-",
			},
		}
		Expect(k8sClient.Create(ctx, network)).To(Succeed())

		By("waiting for the network to be allocated")
		Eventually(Object(network)).Should(Satisfy(apiutils.IsNetworkAllocated))
	})

	return network
}

func BeControlledBy(owner client.Object) types.GomegaMatcher {
	return gcustom.MakeMatcher(func(obj client.Object) (bool, error) {
		return metav1.IsControlledBy(obj, owner), nil
	}).WithTemplate("Expected\n{{ .FormattedActual}}\n{{.To}} be owned by \n{{format .Data 1}}", owner)
}

func DeleteIfExists(obj client.Object) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		return client.IgnoreNotFound(k8sClient.Delete(ctx, obj))
	}
}
