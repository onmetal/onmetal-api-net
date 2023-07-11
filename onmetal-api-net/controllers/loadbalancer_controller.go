package controllers

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/go-logr/logr"
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/apiutils"
	"github.com/onmetal/onmetal-api-net/onmetal-api-net/controller"
	"github.com/onmetal/onmetal-api-net/onmetal-api-net/expectations"
	"github.com/onmetal/onmetal-api-net/onmetal-api-net/publicip"
	"github.com/onmetal/onmetal-api/utils/generic"
	"golang.org/x/exp/slices"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

const (
	loadBalancerUIDLabel = "apinet.api.onmetal.de/loadbalancer-uid"
)

type LoadBalancerReconciler struct {
	client.Client
	Expectations *expectations.Expectations
}

func (r *LoadBalancerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	loadBalancer := &v1alpha1.LoadBalancer{}
	if err := r.Get(ctx, req.NamespacedName, loadBalancer); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		// Delete the corresponding routing object if the load balancer is gone.
		loadBalancerRouting := &v1alpha1.LoadBalancerRouting{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: req.Namespace,
				Name:      req.Name,
			},
		}
		if err := r.Delete(ctx, loadBalancerRouting); client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, fmt.Errorf("error deleting load balancer routing: %w", err)
		}
		return ctrl.Result{}, nil
	}

	return r.reconcileExists(ctx, log, loadBalancer)
}

func (r *LoadBalancerReconciler) reconcileExists(ctx context.Context, log logr.Logger, loadBalancer *v1alpha1.LoadBalancer) (ctrl.Result, error) {
	if !loadBalancer.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, loadBalancer)
	}
	return r.reconcile(ctx, log, loadBalancer)
}

func (r *LoadBalancerReconciler) delete(ctx context.Context, log logr.Logger, loadBalancer *v1alpha1.LoadBalancer) (ctrl.Result, error) {
	log.V(1).Info("Delete")
	log.V(1).Info("Deleted")
	return ctrl.Result{}, nil
}

func (r *LoadBalancerReconciler) getExternallyManagedPublicIPsForLoadBalancer(ctx context.Context, loadBalancer *v1alpha1.LoadBalancer) ([]v1alpha1.IP, error) {
	publicIPList := &v1alpha1.PublicIPList{}
	if err := r.List(ctx, publicIPList,
		client.InNamespace(loadBalancer.Namespace),
	); err != nil {
		return nil, fmt.Errorf("error listing public IPs: %w", err)
	}

	var ips []v1alpha1.IP
	for _, publicIP := range publicIPList.Items {
		if !apiutils.IsPublicIPClaimedBy(&publicIP, loadBalancer) {
			// Don't include public IPs that are not claimed.
			continue
		}

		if !apiutils.IsPublicIPAllocated(&publicIP) {
			// Don't include public IPs that are not allocated.
			continue
		}

		ips = append(ips, *publicIP.Spec.IP)
	}
	return ips, nil
}

func (r *LoadBalancerReconciler) getAndManagePublicIPsForLoadBalancer(ctx context.Context, loadBalancer *v1alpha1.LoadBalancer) ([]v1alpha1.IP, error) {
	sel, err := metav1.LabelSelectorAsSelector(loadBalancer.Spec.IPSelector)
	if err != nil {
		return nil, err
	}

	publicIPList := &v1alpha1.PublicIPList{}
	if err := r.List(ctx, publicIPList,
		client.InNamespace(loadBalancer.Namespace),
	); err != nil {
		return nil, fmt.Errorf("error listing public ips: %w", err)
	}

	var (
		claimMgr = publicip.NewClaimManager(r.Client, loadBalancerKind, loadBalancer, func(ip *v1alpha1.PublicIP) bool {
			return sel.Matches(labels.Set(ip.Labels))
		})
		ips  []v1alpha1.IP
		errs []error
	)
	for i := range publicIPList.Items {
		publicIP := &publicIPList.Items[i]
		ok, err := claimMgr.Claim(ctx, publicIP)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok {
			continue
		}

		if !apiutils.IsPublicIPAllocated(publicIP) {
			continue
		}

		ips = append(ips, *publicIP.Spec.IP)
	}
	return ips, errors.Join(errs...)
}

func (r *LoadBalancerReconciler) updateLoadBalancerStatusIPs(ctx context.Context, loadBalancer *v1alpha1.LoadBalancer, ips []v1alpha1.IP) error {
	base := loadBalancer.DeepCopy()
	loadBalancer.Status.IPs = ips
	if err := r.Status().Patch(ctx, loadBalancer, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("error patching load balancer status: %w", err)
	}
	return nil
}

func (r *LoadBalancerReconciler) manageLoadBalancerRouting(ctx context.Context, log logr.Logger, loadBalancer *v1alpha1.LoadBalancer) error {
	sel, err := metav1.LabelSelectorAsSelector(loadBalancer.Spec.NetworkInterfaceSelector)
	if err != nil {
		return err
	}

	nicList := &v1alpha1.NetworkInterfaceList{}
	if err := r.List(ctx, nicList,
		client.InNamespace(loadBalancer.Namespace),
		client.MatchingLabelsSelector{Selector: sel},
	); err != nil {
		return fmt.Errorf("error listing network interfaces for load balancer: %w", err)
	}

	destinations := make([]v1alpha1.LoadBalancerDestination, 0, len(nicList.Items))
	for _, nic := range nicList.Items {
		destinations = append(destinations, v1alpha1.LoadBalancerDestination{
			Name:    nic.Name,
			UID:     nic.UID,
			NodeRef: nic.Spec.NodeRef,
		})
	}
	slices.SortFunc(destinations, func(a, b v1alpha1.LoadBalancerDestination) bool {
		return a.UID < b.UID
	})

	loadBalancerRouting := &v1alpha1.LoadBalancerRouting{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       loadBalancerRoutingKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: loadBalancer.Namespace,
			Name:      loadBalancer.Name,
		},
		Destinations: destinations,
	}
	_ = ctrl.SetControllerReference(loadBalancer, loadBalancerRouting, r.Scheme())
	if err := r.Patch(ctx, loadBalancerRouting, client.Apply, fieldOwner, client.ForceOwnership); err != nil {
		return fmt.Errorf("error applying load balancer routing: %w", err)
	}
	return nil
}

func buildLoadBalancerSelector(loadBalancer *v1alpha1.LoadBalancer) func(*v1alpha1.LoadBalancerInstance) bool {
	networkRef := loadBalancer.Spec.NetworkRef
	uid := loadBalancer.UID
	return func(loadBalancerInstance *v1alpha1.LoadBalancerInstance) bool {
		return loadBalancerInstance.Labels[loadBalancerUIDLabel] == string(uid) &&
			loadBalancerInstance.Spec.NetworkRef == networkRef &&
			loadBalancerInstance.Spec.Type == loadBalancer.Spec.Type
	}
}

func (r *LoadBalancerReconciler) ensureInstanceIPs(ctx context.Context, instance *v1alpha1.LoadBalancerInstance, ips []v1alpha1.IP) error {
	if slices.Equal(ips, instance.Spec.IPs) {
		// Nothing to do.
		return nil
	}

	base := instance.DeepCopy()
	instance.Spec.IPs = ips
	return r.Patch(ctx, instance, client.MergeFrom(base))
}

func (r *LoadBalancerReconciler) getLoadBalancerInstancesForLoadBalancer(ctx context.Context, loadBalancer *v1alpha1.LoadBalancer, ips []v1alpha1.IP) ([]v1alpha1.LoadBalancerInstance, error) {
	loadBalancerInstanceList := &v1alpha1.LoadBalancerInstanceList{}
	if err := r.List(ctx, loadBalancerInstanceList,
		client.InNamespace(loadBalancer.Namespace),
		client.MatchingLabels{
			loadBalancerUIDLabel: string(loadBalancer.UID),
		},
	); err != nil {
		return nil, fmt.Errorf("error listing load balancer instances: %w", err)
	}

	var (
		sel      = buildLoadBalancerSelector(loadBalancer)
		claimMgr = controller.NewRefManager(r.Client, loadBalancer, sel)

		claimed []v1alpha1.LoadBalancerInstance
		errs    []error
	)
	for _, loadBalancerInstance := range loadBalancerInstanceList.Items {
		ok, err := claimMgr.ClaimObject(ctx, &loadBalancerInstance)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ok {
			if err := r.ensureInstanceIPs(ctx, &loadBalancerInstance, ips); err != nil {
				errs = append(errs, err)
				continue
			}
			claimed = append(claimed, loadBalancerInstance)
		}
	}
	return claimed, errors.Join(errs...)
}

func (r *LoadBalancerReconciler) reconcile(ctx context.Context, log logr.Logger, loadBalancer *v1alpha1.LoadBalancer) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	needsSync := r.Expectations.Satisfied(client.ObjectKeyFromObject(loadBalancer))

	var ips []v1alpha1.IP

	switch loadBalancer.Spec.Type {
	case v1alpha1.LoadBalancerTypePublic:
		if loadBalancer.Spec.IPSelector == nil {
			log.V(1).Info("Getting externally managed public IPs for load balancer")
			externallyManagedIPs, err := r.getExternallyManagedPublicIPsForLoadBalancer(ctx, loadBalancer)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("error getting externally managed IPs for load balancer: %w", err)
			}

			ips = externallyManagedIPs
		} else {
			log.V(1).Info("Getting and managing public IPs for load balancer")
			managedIPs, err := r.getAndManagePublicIPsForLoadBalancer(ctx, loadBalancer)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("error getting and managing IPs for load balancer: %w", err)
			}

			ips = managedIPs
		}
	case v1alpha1.LoadBalancerTypeInternal:
		log.V(1).Info("Using internal load balancer IPs")
		ips = loadBalancer.Spec.IPs
	}

	if !slices.Equal(loadBalancer.Status.IPs, ips) {
		log.V(1).Info("Updating load balancer status IPs")
		if err := r.updateLoadBalancerStatusIPs(ctx, loadBalancer, ips); err != nil {
			return ctrl.Result{}, fmt.Errorf("error updating load balancer status ips: %w", err)
		}
	}

	if loadBalancer.Spec.NetworkInterfaceSelector != nil {
		log.V(1).Info("Managing load balancer routing")
		if err := r.manageLoadBalancerRouting(ctx, log, loadBalancer); err != nil {
			return ctrl.Result{}, fmt.Errorf("error managing load balancer routing: %w", err)
		}
	}

	loadBalancerInstances, err := r.getLoadBalancerInstancesForLoadBalancer(ctx, loadBalancer, ips)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error getting load balancer instances: %w", err)
	}

	if needsSync {
		if err := r.manageInstances(ctx, log, loadBalancer, ips, loadBalancerInstances); err != nil {
			return ctrl.Result{}, fmt.Errorf("error managing load balancer instances: %w", err)
		}
	}

	log.V(1).Info("Reconciled")
	return ctrl.Result{}, nil
}

type PartitionIP struct {
	Partition string
	IP        v1alpha1.IP
}

type PartitionAffinityGroup struct {
	Partition     string
	AffinityGroup string
}

func (r *LoadBalancerReconciler) manageInstances(
	ctx context.Context,
	log logr.Logger,
	loadBalancer *v1alpha1.LoadBalancer,
	ips []v1alpha1.IP,
	allInstances []v1alpha1.LoadBalancerInstance,
) error {
	replicasPerPartition := int(generic.Deref(loadBalancer.Spec.ReplicasPerPartition, 1))

	partitionList := &v1alpha1.PartitionList{}
	if err := r.List(ctx, partitionList); err != nil {
		return fmt.Errorf("error listing partitions: %w", err)
	}

	instancesByPartition := make(map[string][]v1alpha1.LoadBalancerInstance)
	for _, partition := range partitionList.Items {
		if !partition.DeletionTimestamp.IsZero() {
			// Don't schedule on deleting partitions.
			continue
		}

		// Initialize so the key is present (required for GCing unwanted instances)
		instancesByPartition[partition.Name] = nil
	}

	var (
		deleteInstances []v1alpha1.LoadBalancerInstance
	)
	for _, instance := range allInstances {
		partition := instance.Spec.PartitionRef.Name
		instances, ok := instancesByPartition[partition]
		if !ok {
			// Don't include instances from unwanted partitions.
			deleteInstances = append(deleteInstances, instance)
			continue
		}

		instancesByPartition[partition] = append(instances, instance)
	}

	totalNoOfReplicasToCreate := 0
	noOfReplicasToCreateByPartition := make(map[string]int)
	for partition, instances := range instancesByPartition {
		diff := len(instances) - replicasPerPartition
		if diff < 0 {
			diff *= -1
			totalNoOfReplicasToCreate += diff
			noOfReplicasToCreateByPartition[partition] = diff
		} else {
			deleteInstances = append(deleteInstances, getLoadBalancerInstancesToDelete(instances, diff)...)
		}
	}

	if totalNoOfReplicasToCreate == 0 && len(deleteInstances) == 0 {
		log.V(1).Info("No replicas to create or delete")
		return nil
	}

	ctrlKey := client.ObjectKeyFromObject(loadBalancer)
	createNames := r.makeCreateNames(loadBalancer, totalNoOfReplicasToCreate)
	createKeys := getLoadBalancerInstanceKeysFromNames(loadBalancer.Namespace, createNames)
	deleteKeys := getLoadBalancerInstanceKeysFromLoadBalancerInstances(deleteInstances)
	r.Expectations.ExpectCreationsAndDeletions(ctrlKey, createKeys, deleteKeys)
	log.V(1).Info("Planning to create / delete instances", "CreatePerPartition", noOfReplicasToCreateByPartition, "Delete", len(deleteKeys))

	var (
		errs          []error
		createNameIdx int
	)

	for partition, ct := range noOfReplicasToCreateByPartition {
		for i := 0; i < ct; i++ {
			name := createNames[createNameIdx]
			createNameIdx++
			if _, err := r.createInstance(ctx, loadBalancer, name, partition, ips); err != nil {
				// Decrement the expected creation as this won't be observed.
				r.Expectations.CreationObserved(ctrlKey, client.ObjectKey{Namespace: loadBalancer.Namespace, Name: name})
				errs = append(errs, err)
			}
		}
	}

	for _, deleteInstance := range deleteInstances {
		if err := r.Delete(ctx, &deleteInstance); err != nil {
			r.Expectations.DeletionObserved(ctrlKey, client.ObjectKeyFromObject(&deleteInstance))
			if !apierrors.IsNotFound(err) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func getLoadBalancerInstancesToDelete(instances []v1alpha1.LoadBalancerInstance, ct int) []v1alpha1.LoadBalancerInstance {
	idx := len(instances) - ct
	del := instances[idx:]
	return del
}

func (r *LoadBalancerReconciler) createInstance(
	ctx context.Context,
	loadBalancer *v1alpha1.LoadBalancer,
	name string,
	partition string,
	ips []v1alpha1.IP,
) (*v1alpha1.LoadBalancerInstance, error) {
	loadBalancerInstance := &v1alpha1.LoadBalancerInstance{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: loadBalancer.Namespace,
			Name:      name,
			Labels: map[string]string{
				loadBalancerUIDLabel: string(loadBalancer.UID),
			},
		},
		Spec: v1alpha1.LoadBalancerInstanceSpec{
			Type:         loadBalancer.Spec.Type,
			NetworkRef:   loadBalancer.Spec.NetworkRef,
			PartitionRef: corev1.LocalObjectReference{Name: partition},
			IPs:          ips,
			Ports:        loadBalancer.Spec.Ports,
		},
	}
	_ = ctrl.SetControllerReference(loadBalancer, loadBalancerInstance, r.Scheme())
	if err := r.Create(ctx, loadBalancerInstance); err != nil {
		return nil, err
	}
	return loadBalancerInstance, nil
}

func getLoadBalancerInstanceKeysFromNames(namespace string, names []string) []client.ObjectKey {
	keys := make([]client.ObjectKey, len(names))
	for i, name := range names {
		keys[i] = client.ObjectKey{Namespace: namespace, Name: name}
	}
	return keys
}

func getLoadBalancerInstanceKeysFromLoadBalancerInstances(instances []v1alpha1.LoadBalancerInstance) []client.ObjectKey {
	keys := make([]client.ObjectKey, len(instances))
	for i, instance := range instances {
		keys[i] = client.ObjectKeyFromObject(&instance)
	}
	return keys
}

func (r *LoadBalancerReconciler) makeCreateNames(loadBalancer *v1alpha1.LoadBalancer, ct int) []string {
	prefix := loadBalancer.Name
	if len(prefix) > maxPublicIPGenerateNamePrefixLength {
		prefix = prefix[:maxPublicIPGenerateNamePrefixLength]
	}
	prefix = prefix + "-"

	names := sets.New[string]()
	for names.Len() < ct {
		name := prefix + utilrand.String(noOfPublicIPGenerateNameRandomChars)
		names.Insert(name)
	}
	return names.UnsortedList()
}

func (r *LoadBalancerReconciler) enqueueByPublicIPLoadBalancerSelection() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		publicIP := obj.(*v1alpha1.PublicIP)
		log := ctrl.LoggerFrom(ctx)

		loadBalancerList := &v1alpha1.LoadBalancerList{}
		if err := r.List(ctx, loadBalancerList,
			client.InNamespace(publicIP.Namespace),
		); err != nil {
			log.Error(err, "Error listing load balancers")
			return nil
		}

		var reqs []ctrl.Request
		for _, loadBalancer := range loadBalancerList.Items {
			sel, err := metav1.LabelSelectorAsSelector(loadBalancer.Spec.IPSelector)
			if err != nil {
				continue
			}

			if sel.Matches(labels.Set(publicIP.Labels)) {
				reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&loadBalancer)})
			}
		}
		return reqs
	})
}

func (r *LoadBalancerReconciler) enqueueByNetworkInterfaceLoadBalancerSelection() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		nic := obj.(*v1alpha1.NetworkInterface)
		log := ctrl.LoggerFrom(ctx)

		loadBalancerList := &v1alpha1.LoadBalancerList{}
		if err := r.List(ctx, loadBalancerList,
			client.InNamespace(nic.Namespace),
		); err != nil {
			log.Error(err, "Error listing load balancers")
			return nil
		}

		var reqs []ctrl.Request
		for _, loadBalancer := range loadBalancerList.Items {
			loadBalancerKey := client.ObjectKeyFromObject(&loadBalancer)
			sel, err := metav1.LabelSelectorAsSelector(loadBalancer.Spec.NetworkInterfaceSelector)
			if err != nil {
				log.Error(err, "Invalid load balancer network interface selector",
					"LoadBalancerKey", loadBalancerKey,
				)
				continue
			}

			if sel.Matches(labels.Set(nic.Labels)) {
				reqs = append(reqs, ctrl.Request{NamespacedName: loadBalancerKey})
			}
		}
		return reqs
	})
}

func (r *LoadBalancerReconciler) enqueueByPartition() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		log := ctrl.LoggerFrom(ctx)

		loadBalancerList := &v1alpha1.LoadBalancerList{}
		if err := r.List(ctx, loadBalancerList); err != nil {
			log.Error(err, "Error listing load balancers")
			return nil
		}

		reqs := make([]ctrl.Request, len(loadBalancerList.Items))
		for i, loadBalancer := range loadBalancerList.Items {
			reqs[i] = ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&loadBalancer)}
		}
		return reqs
	})
}

func getLoadBalancerControllerOf(obj client.Object) *metav1.OwnerReference {
	ctrlRef := metav1.GetControllerOf(obj)
	if ctrlRef == nil || ctrlRef.APIVersion != v1alpha1.GroupVersion.String() || ctrlRef.Kind != loadBalancerKind {
		return nil
	}
	return ctrlRef
}

func (r *LoadBalancerReconciler) enqueueByOwnedLoadBalancerInstance() handler.EventHandler {
	deleteInstance := func(ctx context.Context, obj client.Object, queue workqueue.RateLimitingInterface) {
		instance := obj.(*v1alpha1.LoadBalancerInstance)
		ctrlRef := getLoadBalancerControllerOf(instance)
		if ctrlRef == nil {
			return
		}

		ctrlKey := client.ObjectKey{Namespace: instance.Namespace, Name: ctrlRef.Name}
		r.Expectations.DeletionObserved(ctrlKey, client.ObjectKeyFromObject(instance))
		queue.Add(ctrl.Request{NamespacedName: ctrlKey})
	}

	addInstance := func(ctx context.Context, obj client.Object, queue workqueue.RateLimitingInterface) {
		instance := obj.(*v1alpha1.LoadBalancerInstance)
		ctrlRef := getLoadBalancerControllerOf(instance)
		if ctrlRef == nil {
			return
		}

		if !instance.DeletionTimestamp.IsZero() {
			deleteInstance(ctx, obj, queue)
			return
		}

		ctrlKey := client.ObjectKey{Namespace: instance.Namespace, Name: ctrlRef.Name}
		r.Expectations.CreationObserved(ctrlKey, client.ObjectKeyFromObject(instance))
		queue.Add(ctrl.Request{NamespacedName: ctrlKey})
	}

	updateInstance := func(ctx context.Context, oldObj, newObj client.Object, queue workqueue.RateLimitingInterface) {
		oldInstance := oldObj.(*v1alpha1.LoadBalancerInstance)
		newInstance := newObj.(*v1alpha1.LoadBalancerInstance)

		oldCtrlRef := getLoadBalancerControllerOf(oldInstance)
		newCtrlRef := getLoadBalancerControllerOf(newInstance)
		ctrlRefChanged := reflect.DeepEqual(oldCtrlRef, newCtrlRef)

		if ctrlRefChanged && oldCtrlRef != nil {
			ctrlKey := client.ObjectKey{Namespace: oldInstance.Namespace, Name: oldCtrlRef.Name}
			queue.Add(ctrl.Request{NamespacedName: ctrlKey})
		}

		if newCtrlRef != nil {
			ctrlKey := client.ObjectKey{Namespace: newInstance.Namespace, Name: newCtrlRef.Name}
			queue.Add(ctrl.Request{NamespacedName: ctrlKey})
		}
	}

	return handler.Funcs{
		CreateFunc: func(ctx context.Context, evt event.CreateEvent, queue workqueue.RateLimitingInterface) {
			addInstance(ctx, evt.Object, queue)
		},
		UpdateFunc: func(ctx context.Context, evt event.UpdateEvent, queue workqueue.RateLimitingInterface) {
			updateInstance(ctx, evt.ObjectOld, evt.ObjectNew, queue)
		},
		DeleteFunc: func(ctx context.Context, evt event.DeleteEvent, queue workqueue.RateLimitingInterface) {
			deleteInstance(ctx, evt.Object, queue)
		},
		GenericFunc: func(ctx context.Context, evt event.GenericEvent, queue workqueue.RateLimitingInterface) {
			addInstance(ctx, evt.Object, queue)
		},
	}
}

func (r *LoadBalancerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.LoadBalancer{}).
		Owns(&v1alpha1.LoadBalancerRouting{}).
		Watches(
			&v1alpha1.LoadBalancerInstance{},
			r.enqueueByOwnedLoadBalancerInstance(),
		).
		Watches(
			&v1alpha1.PublicIP{},
			enqueueByPublicIPClaimerRef(loadBalancerKind),
		).
		Watches(
			&v1alpha1.PublicIP{},
			r.enqueueByPublicIPLoadBalancerSelection(),
			builder.WithPredicates(publicIPClaimablePredicate),
		).
		Watches(
			&v1alpha1.NetworkInterface{},
			r.enqueueByNetworkInterfaceLoadBalancerSelection(),
		).
		Watches(
			&v1alpha1.Partition{},
			r.enqueueByPartition(),
		).
		Complete(r)
}
