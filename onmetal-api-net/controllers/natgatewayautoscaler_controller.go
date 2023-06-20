package controllers

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/onmetal-api-net/expectations"
	"github.com/onmetal/onmetal-api-net/onmetal-api-net/natgateway"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

const (
	maxPublicIPNameLength               = validation.DNS1035LabelMaxLength
	noOfPublicIPGenerateNameRandomChars = 10
	maxPublicIPGenerateNamePrefixLength = maxPublicIPNameLength - noOfPublicIPGenerateNameRandomChars - 1 // -1 for the '-'

	natGatewayAutoscalerNameAnnotation = "apinet.api.onmetal.de/natgatewayautoscaler-name"
	natGatewayAutoscalerUIDAnnotation  = "apinet.api.onmetal.de/natgatewayautoscaler-uid"
	natGatewayUIDLabel                 = "apinet.api.onmetal.de/natgateway-uid"
)

type NATGatewayAutoscalerReconciler struct {
	client.Client

	Expectations *expectations.Expectations
	Selectors    *natgateway.AutoscalerSelectors
}

func (r *NATGatewayAutoscalerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	natGatewayAutoscaler := &v1alpha1.NATGatewayAutoscaler{}
	if err := r.Get(ctx, req.NamespacedName, natGatewayAutoscaler); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		r.Expectations.Delete(req.NamespacedName)
		r.Selectors.Delete(req.NamespacedName)
		return ctrl.Result{}, nil
	}

	return r.reconcileExists(ctx, log, natGatewayAutoscaler)
}

func (r *NATGatewayAutoscalerReconciler) reconcileExists(ctx context.Context, log logr.Logger, natGatewayAutoscaler *v1alpha1.NATGatewayAutoscaler) (ctrl.Result, error) {
	natGatewayName := natGatewayAutoscaler.Spec.NATGatewayRef.Name
	log = log.WithValues("NATGatewayName", natGatewayName)

	if !natGatewayAutoscaler.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	return r.reconcile(ctx, log, natGatewayAutoscaler)
}

func (r *NATGatewayAutoscalerReconciler) reconcile(ctx context.Context, log logr.Logger, natGatewayAutoscaler *v1alpha1.NATGatewayAutoscaler) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	needsSync := r.Expectations.Satisfied(client.ObjectKeyFromObject(natGatewayAutoscaler))

	log.V(1).Info("Getting scale target")
	natGateway := &v1alpha1.NATGateway{}
	natGatewayKey := client.ObjectKey{Namespace: natGatewayAutoscaler.Namespace, Name: natGatewayAutoscaler.Spec.NATGatewayRef.Name}
	if err := r.Get(ctx, natGatewayKey, natGateway); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("error getting NAT gateway %s: %w", natGatewayKey.Name, err)
		}
		log.V(1).Info("Scale target not found")
		return ctrl.Result{}, nil
	}

	r.Selectors.Put(client.ObjectKeyFromObject(natGatewayAutoscaler), natgateway.SelectNetwork(natGateway.Spec.NetworkRef.Name))

	usedPublicIPs, err := r.getPublicIPsForNATGateway(ctx, natGatewayAutoscaler, natGateway)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error getting public IPs for NAT gateway: %w", err)
	}

	if needsSync {
		log.V(1).Info("Managing public IPs")
		if err := r.managePublicIPs(ctx, natGatewayAutoscaler, natGateway, usedPublicIPs); err != nil {
			if !apierrors.IsConflict(err) {
				return ctrl.Result{}, fmt.Errorf("error managing public IPs: %w", err)
			}

			log.V(1).Info("Conflict managing public IPs, requeueing")
			return ctrl.Result{Requeue: true}, nil
		}
	}

	log.V(1).Info("Reconciled")
	return ctrl.Result{}, nil
}

func (r *NATGatewayAutoscalerReconciler) makeCreateNames(natGateway *v1alpha1.NATGateway, ct int) []string {
	prefix := natGateway.Name
	if len(prefix) > maxPublicIPGenerateNamePrefixLength {
		prefix = prefix[:maxPublicIPGenerateNamePrefixLength]
	}
	prefix = prefix + "-"

	names := make([]string, ct)
	for i := 0; i < ct; i++ {
		name := prefix + utilrand.String(noOfPublicIPGenerateNameRandomChars)
		names[i] = name
	}
	return names
}

func getPublicIPKeysFromNames(namespace string, names []string) []client.ObjectKey {
	keys := make([]client.ObjectKey, len(names))
	for i, name := range names {
		keys[i] = client.ObjectKey{Namespace: namespace, Name: name}
	}
	return keys
}

func getPublicIPKeysFromPublicIPs(publicIPs []v1alpha1.PublicIP) []client.ObjectKey {
	keys := make([]client.ObjectKey, len(publicIPs))
	for i, publicIP := range publicIPs {
		keys[i] = client.ObjectKeyFromObject(&publicIP)
	}
	return keys
}

func getLocalObjectReferencesFromNames(names []string) []corev1.LocalObjectReference {
	refs := make([]corev1.LocalObjectReference, len(names))
	for i, name := range names {
		refs[i] = corev1.LocalObjectReference{Name: name}
	}
	return refs
}

func getLocalObjectReferencesFromPublicIPs(publicIPs []v1alpha1.PublicIP) []corev1.LocalObjectReference {
	refs := make([]corev1.LocalObjectReference, len(publicIPs))
	for i, publicIP := range publicIPs {
		refs[i] = corev1.LocalObjectReference{Name: publicIP.Name}
	}
	return refs
}

func splitPublicIPsToDelete(publicIPs []v1alpha1.PublicIP, ct int) (keep, delete []v1alpha1.PublicIP) {
	idx := len(publicIPs) - ct
	keep = publicIPs[:idx]
	delete = publicIPs[idx:]
	return keep, delete
}

func (r *NATGatewayAutoscalerReconciler) managePublicIPs(
	ctx context.Context,
	natGatewayAutoscaler *v1alpha1.NATGatewayAutoscaler,
	natGateway *v1alpha1.NATGateway,
	usedPublicIPs []v1alpha1.PublicIP,
) error {
	nicCfgs, err := r.getNetworkInterfaceConfigsForNATGateway(ctx, natGateway)
	if err != nil {
		return err
	}

	ctrlKey := client.ObjectKeyFromObject(natGatewayAutoscaler)
	totalRequests := len(nicCfgs)
	currentNoOfPublicIPs := len(usedPublicIPs)
	desiredNoOfPublicIPs := r.determineDesiredNoOfPublicIPs(natGatewayAutoscaler, natGateway, currentNoOfPublicIPs, totalRequests)
	diff := currentNoOfPublicIPs - desiredNoOfPublicIPs

	if diff < 0 {
		diff *= -1
		createNames := r.makeCreateNames(natGateway, diff)
		newPublicIPRefs := append(getLocalObjectReferencesFromPublicIPs(usedPublicIPs), getLocalObjectReferencesFromNames(createNames)...)
		if err := r.updateNATGatewayPublicIPRefs(ctx, natGateway, newPublicIPRefs); err != nil {
			return fmt.Errorf("error updating public IP refs: %w", err)
		}

		r.Expectations.ExpectCreations(ctrlKey, getPublicIPKeysFromNames(natGateway.Namespace, createNames))

		var errs []error
		for _, name := range createNames {
			if _, err := r.createNATGatewayPublicIP(ctx, natGatewayAutoscaler, natGateway, name); err != nil {
				// Decrement the expected creation as this won't be observed.
				r.Expectations.CreationObserved(ctrlKey, client.ObjectKey{Namespace: natGateway.Namespace, Name: name})
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	} else if diff > 0 {
		keep, del := splitPublicIPsToDelete(usedPublicIPs, diff)
		newPublicIPRefs := getLocalObjectReferencesFromPublicIPs(keep)
		if err := r.updateNATGatewayPublicIPRefs(ctx, natGateway, newPublicIPRefs); err != nil {
			return fmt.Errorf("error updating public IP refs: %w", err)
		}

		r.Expectations.ExpectDeletions(ctrlKey, getPublicIPKeysFromPublicIPs(del))
		var errs []error
		for _, publicIP := range del {
			if err := r.Delete(ctx, &publicIP); err != nil {
				r.Expectations.DeletionObserved(ctrlKey, client.ObjectKeyFromObject(&publicIP))
				if !apierrors.IsNotFound(err) {
					errs = append(errs, err)
				}
			}
		}
		return errors.Join(errs...)
	}
	return nil
}

func (r *NATGatewayAutoscalerReconciler) getNetworkInterfaceConfigsForNATGateway(ctx context.Context, natGateway *v1alpha1.NATGateway) ([]v1alpha1.NetworkInterfaceConfig, error) {
	nicCfgList := &v1alpha1.NetworkInterfaceConfigList{}
	if err := r.List(ctx, nicCfgList,
		client.InNamespace(natGateway.Namespace),
	); err != nil {
		return nil, fmt.Errorf("error listing network interfaces: %w", err)
	}

	var nicCfgs []v1alpha1.NetworkInterfaceConfig
	for _, nicCfg := range nicCfgList.Items {
		if _, ok := natGatewaySelectsNetworkInterfaceConfig(natGateway, &nicCfg); ok {
			nicCfgs = append(nicCfgs, nicCfg)
		}
	}
	return nicCfgs, nil
}

func (r *NATGatewayAutoscalerReconciler) getPublicIPsForNATGateway(
	ctx context.Context,
	natGatewayAutoscaler *v1alpha1.NATGatewayAutoscaler,
	natGateway *v1alpha1.NATGateway,
) (used []v1alpha1.PublicIP, err error) {
	publicIPList := &v1alpha1.PublicIPList{}
	if err := r.List(ctx, publicIPList,
		client.InNamespace(natGateway.Namespace),
		client.MatchingLabels{
			natGatewayUIDLabel: string(natGateway.UID),
		},
	); err != nil {
		return nil, fmt.Errorf("error listing public IPs: %w", err)
	}

	var (
		names   = natGatewayPublicIPNames(natGateway)
		isInUse = func(publicIP *v1alpha1.PublicIP) bool {
			if !metav1.IsControlledBy(publicIP, natGateway) || publicIP.Spec.IPFamily != natGateway.Spec.IPFamily {
				// We only care about correctly managed objects.
				return false
			}

			claimerRef := publicIP.Spec.ClaimerRef
			if claimerRef == nil {
				// Don't care about public IPs without claimer set.
				return false
			}

			if claimerRef.UID != natGateway.UID {
				// Different claimer - skip.
				return false
			}

			if !names.Has(publicIP.Name) {
				// We're not referencing this public IP - skip.
				return false
			}

			return true
		}
		errs []error
	)
	for _, publicIP := range publicIPList.Items {
		if !isInUse(&publicIP) {
			if err := r.Delete(ctx, &publicIP); client.IgnoreNotFound(err) != nil {
				errs = append(errs, err)
			}
			continue
		}
		used = append(used, publicIP)
	}

	return used, errors.Join(errs...)
}

func (r *NATGatewayAutoscalerReconciler) determineDesiredNoOfPublicIPs(
	natGatewayAutoscaler *v1alpha1.NATGatewayAutoscaler,
	natGateway *v1alpha1.NATGateway,
	totalNoOfPublicIPs int,
	totalRequests int,
) int {
	slotsPerIP := int64(natgateway.SlotsPerIP(natGateway.Spec.PortsPerNetworkInterface))
	totalSlots := slotsPerIP * int64(totalNoOfPublicIPs)
	slotsDiff := totalSlots - int64(totalRequests)
	ipsDiff := int(slotsDiff / slotsPerIP)
	desiredNoOfPublicIPs := totalNoOfPublicIPs - ipsDiff

	if minPublicIPs := natGatewayAutoscaler.Spec.MinPublicIPs; minPublicIPs != nil {
		desiredNoOfPublicIPs = maxInt(int(*minPublicIPs), desiredNoOfPublicIPs)
	}
	if maxPublicIPs := natGatewayAutoscaler.Spec.MaxPublicIPs; maxPublicIPs != nil {
		desiredNoOfPublicIPs = minInt(int(*maxPublicIPs), desiredNoOfPublicIPs)
	}
	return desiredNoOfPublicIPs
}

func (r *NATGatewayAutoscalerReconciler) createNATGatewayPublicIP(
	ctx context.Context,
	natGatewayAutoscaler *v1alpha1.NATGatewayAutoscaler,
	natGateway *v1alpha1.NATGateway,
	name string,
) (*v1alpha1.PublicIP, error) {
	publicIP := &v1alpha1.PublicIP{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: natGateway.Namespace,
			Name:      name,
			Labels: map[string]string{
				natGatewayUIDLabel: string(natGateway.UID),
			},
			Annotations: map[string]string{
				natGatewayAutoscalerNameAnnotation: natGatewayAutoscaler.Name,
				natGatewayAutoscalerUIDAnnotation:  string(natGatewayAutoscaler.UID),
			},
		},
		Spec: v1alpha1.PublicIPSpec{
			IPFamily: natGateway.Spec.IPFamily,
			ClaimerRef: &v1alpha1.PublicIPClaimerRef{
				Kind: natGatewayKind,
				Name: natGateway.Name,
				UID:  natGateway.UID,
			},
		},
	}
	_ = ctrl.SetControllerReference(natGateway, publicIP, r.Scheme())
	if err := r.Create(ctx, publicIP); err != nil {
		return nil, fmt.Errorf("error creating public IP: %w", err)
	}
	return publicIP, nil
}

func (r *NATGatewayAutoscalerReconciler) updateNATGatewayPublicIPRefs(
	ctx context.Context,
	natGateway *v1alpha1.NATGateway,
	publicIPRefs []corev1.LocalObjectReference,
) error {
	base := natGateway.DeepCopy()
	natGateway.Spec.PublicIPRefs = publicIPRefs
	if err := r.Patch(ctx, natGateway, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("error patching NAT gateway: %w", err)
	}
	return nil
}

func (r *NATGatewayAutoscalerReconciler) getRequestsByNATGatewayKey(ctx context.Context, natGatewayKey client.ObjectKey) ([]ctrl.Request, error) {
	natGatewayAutoscalerList := &v1alpha1.NATGatewayAutoscalerList{}
	if err := r.List(ctx, natGatewayAutoscalerList,
		client.InNamespace(natGatewayKey.Namespace),
	); err != nil {
		return nil, err
	}

	var reqs []ctrl.Request
	for _, natGatewayAutoscaler := range natGatewayAutoscalerList.Items {
		if natGatewayAutoscaler.Spec.NATGatewayRef.Name == natGatewayKey.Name {
			reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&natGatewayAutoscaler)})
		}
	}
	return reqs, nil
}

func getNATGatewayAutoscalerAnnotationsFrom(obj client.Object) (name string, uid types.UID, ok bool) {
	name, ok = obj.GetAnnotations()[natGatewayAutoscalerNameAnnotation]
	if !ok {
		return "", "", false
	}
	uidString, ok := obj.GetAnnotations()[natGatewayAutoscalerUIDAnnotation]
	if !ok {
		return "", "", false
	}
	uid = types.UID(uidString)
	return name, uid, true
}

func (r *NATGatewayAutoscalerReconciler) resolveNATGatewayAutoscaler(ctx context.Context, key client.ObjectKey, uid types.UID) (*v1alpha1.NATGatewayAutoscaler, error) {
	natGatewayAutoscaler := &v1alpha1.NATGatewayAutoscaler{}
	if err := r.Get(ctx, key, natGatewayAutoscaler); err != nil {
		return nil, err
	}
	if uid != natGatewayAutoscaler.UID {
		return nil, nil
	}
	return natGatewayAutoscaler, nil
}

func (r *NATGatewayAutoscalerReconciler) enqueueByNATGatewayOwnedPublicIPs() handler.EventHandler {
	deletePublicIP := func(ctx context.Context, obj client.Object, queue workqueue.RateLimitingInterface) {
		publicIP := obj.(*v1alpha1.PublicIP)
		log := ctrl.LoggerFrom(ctx)

		name, uid, ok := getNATGatewayAutoscalerAnnotationsFrom(publicIP)
		if !ok {
			// No autoscaler should care about non-managed objects being deleted.
			return
		}

		natGatewayAutoscalerKey := client.ObjectKey{Namespace: publicIP.Namespace, Name: name}
		natGatewayAutoscaler, err := r.resolveNATGatewayAutoscaler(ctx, natGatewayAutoscalerKey, uid)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				log.Error(err, "Error getting NAT gateway autoscaler")
				return
			}
			return
		}
		if natGatewayAutoscaler == nil {
			return
		}

		r.Expectations.DeletionObserved(natGatewayAutoscalerKey, client.ObjectKeyFromObject(publicIP))
		queue.Add(ctrl.Request{NamespacedName: natGatewayAutoscalerKey})
	}

	addPublicIP := func(ctx context.Context, obj client.Object, queue workqueue.RateLimitingInterface) {
		publicIP := obj.(*v1alpha1.PublicIP)
		log := ctrl.LoggerFrom(ctx)

		if !publicIP.DeletionTimestamp.IsZero() {
			deletePublicIP(ctx, obj, queue)
			return
		}

		name, uid, ok := getNATGatewayAutoscalerAnnotationsFrom(publicIP)
		if !ok {
			// No autoscaler should care about non-managed objects being created.
			return
		}

		natGatewayAutoscalerKey := client.ObjectKey{Namespace: publicIP.Namespace, Name: name}
		natGatewayAutoscaler, err := r.resolveNATGatewayAutoscaler(ctx, natGatewayAutoscalerKey, uid)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				log.Error(err, "Error getting NAT gateway autoscaler")
				return
			}
			return
		}
		if natGatewayAutoscaler == nil {
			return
		}

		r.Expectations.CreationObserved(natGatewayAutoscalerKey, client.ObjectKeyFromObject(publicIP))
		queue.Add(ctrl.Request{NamespacedName: natGatewayAutoscalerKey})
	}

	return handler.Funcs{
		CreateFunc: func(ctx context.Context, evt event.CreateEvent, queue workqueue.RateLimitingInterface) {
			addPublicIP(ctx, evt.Object, queue)
		},
		DeleteFunc: func(ctx context.Context, evt event.DeleteEvent, queue workqueue.RateLimitingInterface) {
			deletePublicIP(ctx, evt.Object, queue)
		},
		GenericFunc: func(ctx context.Context, evt event.GenericEvent, queue workqueue.RateLimitingInterface) {
			if !evt.Object.GetDeletionTimestamp().IsZero() {
				deletePublicIP(ctx, evt.Object, queue)
			} else {
				addPublicIP(ctx, evt.Object, queue)
			}
		},
	}
}

func (r *NATGatewayAutoscalerReconciler) enqueueByNATGateway() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		natGateway := obj.(*v1alpha1.NATGateway)
		log := ctrl.LoggerFrom(ctx)

		reqs, err := r.getRequestsByNATGatewayKey(ctx, client.ObjectKeyFromObject(natGateway))
		if err != nil {
			log.Error(err, "Error getting requests by NAT gateway key")
			return nil
		}
		return reqs
	})
}

func (r *NATGatewayAutoscalerReconciler) enqueueByNetworkInterfaceConfig() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		nicCfg := obj.(*v1alpha1.NetworkInterfaceConfig)

		var reqs []ctrl.Request
		for key := range r.Selectors.ReverseSelect(nicCfg.Network.Name) {
			if key.Namespace != nicCfg.Namespace {
				continue
			}

			reqs = append(reqs, ctrl.Request{NamespacedName: key})
		}
		return reqs
	})
}

func (r *NATGatewayAutoscalerReconciler) enqueueByNATGatewayAutoscaler() handler.EventHandler {
	addNATGatewayAutoscaler := func(obj client.Object, queue workqueue.RateLimitingInterface) {
		natGatewayAutoscaler := obj.(*v1alpha1.NATGatewayAutoscaler)
		key := client.ObjectKeyFromObject(natGatewayAutoscaler)
		queue.AddRateLimited(ctrl.Request{NamespacedName: key})
		r.Selectors.PutIfNotPresent(key, natgateway.NoVNISelector())
	}

	deleteNATGatewayAutoscaler := func(obj client.Object, queue workqueue.RateLimitingInterface) {
		natGatewayAutoscaler := obj.(*v1alpha1.NATGatewayAutoscaler)
		key := client.ObjectKeyFromObject(natGatewayAutoscaler)
		queue.Forget(ctrl.Request{NamespacedName: key})
		r.Selectors.Delete(key)
	}

	return handler.Funcs{
		CreateFunc: func(ctx context.Context, evt event.CreateEvent, queue workqueue.RateLimitingInterface) {
			addNATGatewayAutoscaler(evt.Object, queue)
		},
		UpdateFunc: func(ctx context.Context, evt event.UpdateEvent, queue workqueue.RateLimitingInterface) {
			addNATGatewayAutoscaler(evt.ObjectNew, queue)
		},
		DeleteFunc: func(ctx context.Context, evt event.DeleteEvent, queue workqueue.RateLimitingInterface) {
			deleteNATGatewayAutoscaler(evt.Object, queue)
		},
		GenericFunc: func(ctx context.Context, evt event.GenericEvent, queue workqueue.RateLimitingInterface) {
			if !evt.Object.GetDeletionTimestamp().IsZero() {
				deleteNATGatewayAutoscaler(evt.Object, queue)
			} else {
				addNATGatewayAutoscaler(evt.Object, queue)
			}
		},
	}
}

func (r *NATGatewayAutoscalerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("natgatewayautoscaler").
		Watches(
			&v1alpha1.NATGatewayAutoscaler{},
			r.enqueueByNATGatewayAutoscaler(),
		).
		Watches(
			&v1alpha1.NATGateway{},
			r.enqueueByNATGateway(),
		).
		Watches(
			&v1alpha1.PublicIP{},
			r.enqueueByNATGatewayOwnedPublicIPs(),
		).
		Watches(
			&v1alpha1.NetworkInterfaceConfig{},
			r.enqueueByNetworkInterfaceConfig(),
		).
		Complete(r)
}
