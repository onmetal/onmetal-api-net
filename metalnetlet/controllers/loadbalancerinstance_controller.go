package controllers

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/go-logr/logr"
	"github.com/onmetal/controller-utils/clientutils"
	metalnetv1alpha1 "github.com/onmetal/metalnet/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/apiutils"
	metalnetletclient "github.com/onmetal/onmetal-api-net/metalnetlet/client"
	"github.com/onmetal/onmetal-api/utils/generic"
	"golang.org/x/exp/slices"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	loadBalancerInstanceNamespaceLabel = "metalnetlet.apinet.onmetal.de/loadbalancerinstance-namespace"
	loadBalancerInstanceNameLabel      = "metalnetlet.apinet.onmetal.de/loadbalancerinstance-name"
	loadBalancerInstanceUIDLabel       = "metalnetlet.apinet.onmetal.de/loadbalancerinstance-uid"
)

type LoadBalancerInstanceReconciler struct {
	client.Client
	MetalnetClient client.Client

	PartitionName string

	MetalnetNamespace string
}

func (r *LoadBalancerInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	loadBalancerInstance := &v1alpha1.LoadBalancerInstance{}
	if err := r.Get(ctx, req.NamespacedName, loadBalancerInstance); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		log.V(1).Info("Deleting any leftover metalnet load balancers")
		anyExists, err := r.deleteMetalnetLoadBalancersByLoadBalancerInstanceKeyAndAnyExists(ctx, req.NamespacedName)
		if err != nil {
			return ctrl.Result{}, err
		}
		if anyExists {
			log.V(1).Info("Some metalnet load balancers still exist, requeueing")
			return ctrl.Result{Requeue: true}, nil
		}
		log.V(1).Info("Any potential leftover metalnet load balancer is gone")
		return ctrl.Result{}, nil
	}

	return r.reconcileExists(ctx, log, loadBalancerInstance)
}

func (r *LoadBalancerInstanceReconciler) reconcileExists(ctx context.Context, log logr.Logger, loadBalancerInstance *v1alpha1.LoadBalancerInstance) (ctrl.Result, error) {
	if !loadBalancerInstance.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, loadBalancerInstance)
	}
	return r.reconcile(ctx, log, loadBalancerInstance)
}

func (r *LoadBalancerInstanceReconciler) deleteMetalnetLoadBalancersByLoadBalancerInstanceKeyAndAnyExists(ctx context.Context, key client.ObjectKey) (bool, error) {
	return metalnetletclient.DeleteAllOfAndAnyExists(ctx, r.MetalnetClient, &metalnetv1alpha1.LoadBalancer{},
		client.InNamespace(r.MetalnetNamespace),
		client.MatchingLabels{
			loadBalancerInstanceNamespaceLabel: key.Namespace,
			loadBalancerInstanceNameLabel:      key.Name,
		},
	)
}

func (r *LoadBalancerInstanceReconciler) deleteMetalnetLoadBalancersByLoadBalancerInstanceAndAnyExists(ctx context.Context, loadBalancerInstance *v1alpha1.LoadBalancerInstance) (bool, error) {
	return metalnetletclient.DeleteAllOfAndAnyExists(ctx, r.MetalnetClient, &metalnetv1alpha1.LoadBalancer{},
		client.InNamespace(r.MetalnetNamespace),
		client.MatchingLabels{
			loadBalancerInstanceUIDLabel: string(loadBalancerInstance.UID),
		},
	)
}

func (r *LoadBalancerInstanceReconciler) delete(ctx context.Context, log logr.Logger, loadBalancerInstance *v1alpha1.LoadBalancerInstance) (ctrl.Result, error) {
	log.V(1).Info("Delete")
	if !controllerutil.ContainsFinalizer(loadBalancerInstance, PartitionFinalizer(r.PartitionName)) {
		log.V(1).Info("Finalizer not present, nothing to do")
		return ctrl.Result{}, nil
	}
	log.V(1).Info("Finalizer present, doing cleanup")
	anyExists, err := r.deleteMetalnetLoadBalancersByLoadBalancerInstanceAndAnyExists(ctx, loadBalancerInstance)
	if err != nil {
		return ctrl.Result{}, err
	}
	if anyExists {
		log.V(1).Info("Some metalnet load balancers are still present, requeuing")
		return ctrl.Result{Requeue: true}, nil
	}

	log.V(1).Info("All metalnet load balancers gone, removing finalizer")
	if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, loadBalancerInstance, PartitionFinalizer(r.PartitionName)); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}

	log.V(1).Info("Deleted")
	return ctrl.Result{}, nil
}

func (r *LoadBalancerInstanceReconciler) getMetalnetLoadBalancersForLoadBalancerInstance(
	ctx context.Context,
	loadBalancerInstance *v1alpha1.LoadBalancerInstance,
) ([]metalnetv1alpha1.LoadBalancer, error) {
	metalnetLoadBalancerList := &metalnetv1alpha1.LoadBalancerList{}
	if err := r.MetalnetClient.List(ctx, metalnetLoadBalancerList,
		client.InNamespace(r.MetalnetNamespace),
		client.MatchingLabels{
			loadBalancerInstanceUIDLabel: string(loadBalancerInstance.UID),
		},
	); err != nil {
		return nil, fmt.Errorf("error listing metalnet load balancer instances: %w", err)
	}
	return metalnetLoadBalancerList.Items, nil
}

func (r *LoadBalancerInstanceReconciler) getNetworkVNI(ctx context.Context, loadBalancerInstance *v1alpha1.LoadBalancerInstance) (int32, error) {
	network := &v1alpha1.Network{}
	networkKey := client.ObjectKey{Namespace: loadBalancerInstance.Namespace, Name: loadBalancerInstance.Spec.NetworkRef.Name}
	if err := r.Get(ctx, networkKey, network); err != nil {
		if !apierrors.IsNotFound(err) {
			return 0, fmt.Errorf("error getting network %s: %w", networkKey.Name, err)
		}
		return -1, nil
	}

	if !apiutils.IsNetworkAllocated(network) {
		return -1, nil
	}
	return *network.Spec.VNI, nil
}

func (r *LoadBalancerInstanceReconciler) manageMetalnetLoadBalancers(
	ctx context.Context,
	log logr.Logger,
	loadBalancerInstance *v1alpha1.LoadBalancerInstance,
	metalnetNodeName string,
) (bool, error) {
	metalnetLoadBalancerType, err := loadBalancerTypeToMetalnetLoadBalancerType(loadBalancerInstance.Spec.Type)
	if err != nil {
		return false, err
	}

	vni, err := r.getNetworkVNI(ctx, loadBalancerInstance)
	if err != nil {
		return false, err
	}
	if vni < 0 {
		return false, nil
	}

	metalnetLoadBalancers, err := r.getMetalnetLoadBalancersForLoadBalancerInstance(ctx, loadBalancerInstance)
	if err != nil {
		return false, err
	}

	var (
		unsatisfiedIPs = sets.New(loadBalancerInstance.Spec.IPs...)
		errs           []error
	)
	for _, metalnetLoadBalancer := range metalnetLoadBalancers {
		ip := metalnetIPToIP(metalnetLoadBalancer.Spec.IP)
		if unsatisfiedIPs.Has(ip) {
			unsatisfiedIPs.Delete(ip)
			continue
		}

		if err := r.Delete(ctx, &metalnetLoadBalancer); client.IgnoreNotFound(err) != nil {
			errs = append(errs, err)
			continue
		}
	}

	var bumpCollisionCount bool
	for ip := range unsatisfiedIPs {
		metalnetLoadBalancerHash := computeMetalnetLoadBalancerHash(ip, loadBalancerInstance.Status.CollisionCount)
		metalnetLoadBalancerSpec := metalnetv1alpha1.LoadBalancerSpec{
			NetworkRef: corev1.LocalObjectReference{Name: MetalnetNetworkName(vni)},
			LBtype:     metalnetLoadBalancerType,
			IPFamily:   ip.Family(),
			IP:         ipToMetalnetIP(ip),
			Ports:      loadBalancerPortsToMetalnetLoadBalancerPorts(loadBalancerInstance.Spec.Ports),
			NodeName:   &metalnetNodeName,
		}
		metalnetLoadBalancerName := string(loadBalancerInstance.UID) + "-" + metalnetLoadBalancerHash
		metalnetLoadBalancer := &metalnetv1alpha1.LoadBalancer{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: r.MetalnetNamespace,
				Name:      metalnetLoadBalancerName,
				Labels: map[string]string{
					loadBalancerInstanceNamespaceLabel: loadBalancerInstance.Namespace,
					loadBalancerInstanceNameLabel:      loadBalancerInstance.Name,
					loadBalancerInstanceUIDLabel:       string(loadBalancerInstance.UID),
				},
			},
			Spec: metalnetLoadBalancerSpec,
		}
		createMetalnetLoadBalancer := metalnetLoadBalancer.DeepCopy()
		if err := r.Create(ctx, metalnetLoadBalancer); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				errs = append(errs, err)
				continue
			}

			// We may end up hitting this due to a slow cache or a fast resync of the load balancer instance.
			metalnetLoadBalancerKey := client.ObjectKey{Namespace: r.MetalnetNamespace, Name: metalnetLoadBalancerName}
			if err := r.Get(ctx, metalnetLoadBalancerKey, metalnetLoadBalancer); err != nil {
				errs = append(errs, err)
				continue
			}

			if EqualMetalnetLoadBalancers(createMetalnetLoadBalancer, metalnetLoadBalancer) {
				continue
			}

			// Issue collision count bump and return original already exists error.
			bumpCollisionCount = true
			errs = append(errs, err)
		}
	}
	if bumpCollisionCount {
		if err := r.bumpLoadBalancerInstanceCollisionCount(ctx, loadBalancerInstance); err != nil {
			log.Error(err, "Error bumping collision count")
		} else {
			log.V(1).Info("Bumped collision count")
		}
	}
	return true, errors.Join(errs...)
}

func EqualMetalnetLoadBalancers(inst1, inst2 *metalnetv1alpha1.LoadBalancer) bool {
	return inst1.Spec.IP == inst2.Spec.IP &&
		inst1.Spec.IPFamily == inst2.Spec.IPFamily &&
		inst1.Spec.NetworkRef == inst2.Spec.NetworkRef &&
		inst1.Spec.LBtype == inst2.Spec.LBtype &&
		slices.Equal(inst1.Spec.Ports, inst2.Spec.Ports)
}

func (r *LoadBalancerInstanceReconciler) bumpLoadBalancerInstanceCollisionCount(ctx context.Context, loadBalancerInstance *v1alpha1.LoadBalancerInstance) error {
	oldCollisionCount := generic.Deref(loadBalancerInstance.Status.CollisionCount, 0)
	base := loadBalancerInstance.DeepCopy()
	loadBalancerInstance.Status.CollisionCount = generic.Pointer(oldCollisionCount + 1)
	return r.Status().Patch(ctx, loadBalancerInstance, client.MergeFrom(base))
}

func computeMetalnetLoadBalancerHash(ip v1alpha1.IP, collisionCount *int32) string {
	h := fnv.New32a()

	_, _ = h.Write(ip.AsSlice())

	// Add collisionCount in the hash if it exists
	if collisionCount != nil {
		collisionCountBytes := make([]byte, 8)
		binary.LittleEndian.PutUint32(collisionCountBytes, uint32(*collisionCount))
		_, _ = h.Write(collisionCountBytes)
	}

	return rand.SafeEncodeString(fmt.Sprint(h.Sum32()))
}

func (r *LoadBalancerInstanceReconciler) reconcile(ctx context.Context, log logr.Logger, loadBalancerInstance *v1alpha1.LoadBalancerInstance) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	metalnetNode, err := GetMetalnetNode(ctx, r.PartitionName, r.MetalnetClient, loadBalancerInstance.Spec.NodeRef.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	if metalnetNode == nil || !metalnetNode.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(loadBalancerInstance, PartitionFinalizer(r.PartitionName)) {
			log.V(1).Info("Finalizer not present and metalnet node not found / deleting, nothing to do")
			return ctrl.Result{}, nil
		}

		anyExists, err := r.deleteMetalnetLoadBalancersByLoadBalancerInstanceAndAnyExists(ctx, loadBalancerInstance)
		if err != nil {
			return ctrl.Result{}, err
		}
		if anyExists {
			log.V(1).Info("Not yet all metalnet load balancers gone, requeueing")
			return ctrl.Result{Requeue: true}, nil
		}

		log.V(1).Info("All metalnet load balancers gone, removing finalizer")
		if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, loadBalancerInstance, PartitionFinalizer(r.PartitionName)); err != nil {
			return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
		}
		log.V(1).Info("Removed finalizer")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Metalnet node present and not deleting, ensuring finalizer")
	modified, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, loadBalancerInstance, PartitionFinalizer(r.PartitionName))
	if err != nil {
		return ctrl.Result{}, err
	}
	if modified {
		log.V(1).Info("Added finalizer, requeueing")
		return ctrl.Result{Requeue: true}, nil
	}
	log.V(1).Info("Finalizer present")

	ok, err := r.manageMetalnetLoadBalancers(ctx, log, loadBalancerInstance, metalnetNode.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error managing metalnet load balancers: %w", err)
	}
	if !ok {
		log.V(1).Info("Not all load balancer instance dependencies are ready")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Reconciled")
	return ctrl.Result{}, nil
}

func (r *LoadBalancerInstanceReconciler) isPartitionLoadBalancerInstance() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		loadBalancerInstance := obj.(*v1alpha1.LoadBalancerInstance)
		nodeRef := loadBalancerInstance.Spec.NodeRef
		if nodeRef == nil {
			return false
		}

		_, err := ParseNodeName(r.PartitionName, nodeRef.Name)
		return err == nil
	})
}

func (r *LoadBalancerInstanceReconciler) enqueueByMetalnetLoadBalancer() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		metalnetLoadBalancer := obj.(*metalnetv1alpha1.LoadBalancer)
		namespace, ok := metalnetLoadBalancer.Labels[loadBalancerInstanceNamespaceLabel]
		if !ok {
			return nil
		}
		name, ok := metalnetLoadBalancer.Labels[loadBalancerInstanceNameLabel]
		if !ok {
			return nil
		}
		return []ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: namespace, Name: name}}}
	})
}

func (r *LoadBalancerInstanceReconciler) SetupWithManager(mgr ctrl.Manager, metalnetCache cache.Cache) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(
			&v1alpha1.LoadBalancerInstance{},
			builder.WithPredicates(r.isPartitionLoadBalancerInstance()),
		).
		WatchesRawSource(
			source.Kind(metalnetCache, &metalnetv1alpha1.LoadBalancer{}),
			r.enqueueByMetalnetLoadBalancer(),
		).
		Complete(r)
}
