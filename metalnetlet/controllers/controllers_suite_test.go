/*
Copyright 2022 The Metal Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/onmetal/controller-utils/modutils"
	metalnetv1alpha1 "github.com/onmetal/metalnet/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/metalnetlet/scheduler"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
)

const (
	pollingInterval      = 50 * time.Millisecond
	eventuallyTimeout    = 3 * time.Second
	consistentlyDuration = 1 * time.Second
)

const (
	partitionName     = "test-metalnetlet"
	metalnetNamespace = "metalnetlet-system"
)

func TestControllers(t *testing.T) {
	SetDefaultConsistentlyPollingInterval(pollingInterval)
	SetDefaultEventuallyPollingInterval(pollingInterval)
	SetDefaultEventuallyTimeout(eventuallyTimeout)
	SetDefaultConsistentlyDuration(consistentlyDuration)

	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(GinkgoLogr)

	modutils := modutils.NewExecutor(modutils.ExecutorOptions{Dir: "../.."})

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "onmetal-api-net", "crd", "bases"),
			filepath.Join(modutils.Dir("github.com/onmetal/metalnet", "config", "crd", "bases")),
		},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())
	DeferCleanup(testEnv.Stop)

	Expect(v1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(metalnetv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	SetClient(k8sClient)

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:             scheme.Scheme,
		Host:               "127.0.0.1",
		MetricsBindAddress: "0",
	})
	Expect(err).ToNot(HaveOccurred())

	metalnetNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: metalnetNamespace,
		},
	}
	Expect(k8sClient.Create(context.TODO(), metalnetNs)).To(Succeed())

	// register reconciler here
	partitionReconciler := &PartitionReconciler{
		Client:        k8sManager.GetClient(),
		PartitionName: partitionName,
	}
	Expect(partitionReconciler.SetupWithManager(k8sManager)).To(Succeed())

	Expect(SetupRunAfter(k8sManager, func(ctx context.Context) error {
		defer GinkgoRecover()
		Expect((&NetworkReconciler{
			Client:            k8sManager.GetClient(),
			MetalnetCluster:   k8sManager,
			PartitionName:     partitionName,
			MetalnetNamespace: metalnetNamespace,
		}).SetupWithManager(k8sManager)).To(Succeed())

		Expect((&MetalnetNodeReconciler{
			Client:         k8sManager.GetClient(),
			MetalnetClient: k8sManager.GetClient(),
			PartitionName:  partitionName,
		}).SetupWithManager(k8sManager, k8sManager.GetCache())).To(Succeed())

		Expect((&NetworkInterfaceReconciler{
			Client:            k8sManager.GetClient(),
			MetalnetClient:    k8sManager.GetClient(),
			PartitionName:     partitionName,
			MetalnetNamespace: metalnetNamespace,
		}).SetupWithManager(k8sManager, k8sManager.GetCache())).To(Succeed())

		Expect((&LoadBalancerInstanceReconciler{
			Client:            k8sManager.GetClient(),
			MetalnetClient:    k8sManager.GetClient(),
			PartitionName:     partitionName,
			MetalnetNamespace: metalnetNamespace,
		}).SetupWithManager(k8sManager, k8sManager.GetCache())).To(Succeed())

		schedulerCache := scheduler.NewCache(k8sManager.GetLogger(), scheduler.DefaultCacheStrategy)
		Expect(k8sManager.Add(schedulerCache)).To(Succeed())

		Expect((&LoadBalancerInstanceSchedulerReconciler{
			Client:         k8sManager.GetClient(),
			EventRecorder:  &record.FakeRecorder{},
			MetalnetClient: k8sManager.GetClient(),
			Cache:          schedulerCache,
			PartitionName:  partitionName,
		}).SetupWithManager(k8sManager)).To(Succeed())

		return nil
	}, []WaitFor{partitionReconciler}, WithTimeout(10*time.Second))).To(Succeed())

	mgrCtx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	go func() {
		defer GinkgoRecover()
		Expect(k8sManager.Start(mgrCtx)).To(Succeed(), "failed to start manager")
	}()
})

func SetupTest() *corev1.Namespace {
	ns := &corev1.Namespace{}

	BeforeEach(func(ctx SpecContext) {

		*ns = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "testns-",
			},
		}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed(), "failed to create test namespace")

		DeferCleanup(func(ctx context.Context) {
			Expect(k8sClient.Delete(ctx, ns)).To(Succeed(), "failed to delete test namespace")
		})
	})

	return ns
}

func DeleteIfExists(obj client.Object) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		return client.IgnoreNotFound(k8sClient.Delete(ctx, obj))
	}
}

func SetupMetalnetNode() *corev1.Node {
	node := &corev1.Node{}
	BeforeEach(func(ctx SpecContext) {
		*node = corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "node-",
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())

		DeferCleanup(DeleteIfExists(node))
	})
	return node
}

func SetupNetwork(ns *corev1.Namespace, vni int32) *v1alpha1.Network {
	network := &v1alpha1.Network{}

	BeforeEach(func(ctx SpecContext) {
		*network = v1alpha1.Network{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    ns.Name,
				GenerateName: "network-",
			},
			Spec: v1alpha1.NetworkSpec{
				VNI: pointer.Int32(vni),
			},
		}
		By("creating a network")
		Expect(k8sClient.Create(ctx, network)).To(Succeed())

		By("patching the network to be allocated")
		Eventually(UpdateStatus(network, func() {
			network.Status.Conditions = []v1alpha1.NetworkCondition{
				{
					Type:   v1alpha1.NetworkAllocated,
					Status: corev1.ConditionTrue,
				},
			}
		})).Should(Succeed())
	})

	return network
}
