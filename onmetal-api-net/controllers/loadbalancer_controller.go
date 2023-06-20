package controllers

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/apiutils"
	"github.com/onmetal/onmetal-api-net/onmetal-api-net/publicip"
	"github.com/onmetal/onmetal-api/utils/generic"
	"golang.org/x/exp/slices"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

type LoadBalancerReconciler struct {
	client.Client
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

func loadBalancerPublicIPSelector(loadBalancer *v1alpha1.LoadBalancer) func(*v1alpha1.PublicIP) bool {
	names := sets.New[string]()
	for _, publicIPRef := range loadBalancer.Spec.PublicIPRefs {
		names.Insert(publicIPRef.Name)
	}
	return func(publicIP *v1alpha1.PublicIP) bool {
		return names.Has(publicIP.Name)
	}
}

func loadBalancerReferencesPublicIP(loadBalancer *v1alpha1.LoadBalancer, publicIP *v1alpha1.PublicIP) bool {
	for _, publicIPRef := range loadBalancer.Spec.PublicIPRefs {
		if publicIPRef.Name == publicIP.Name {
			return true
		}
	}
	return false
}

func (r *LoadBalancerReconciler) getPublicIPsForLoadBalancer(ctx context.Context, loadBalancer *v1alpha1.LoadBalancer) ([]v1alpha1.IP, error) {
	publicIPList := &v1alpha1.PublicIPList{}
	if err := r.List(ctx, publicIPList,
		client.InNamespace(loadBalancer.Namespace),
	); err != nil {
		return nil, fmt.Errorf("error listing public ips: %w", err)
	}

	var (
		sel      = loadBalancerPublicIPSelector(loadBalancer)
		claimMgr = publicip.NewClaimManager(r.Client, loadBalancerKind, loadBalancer, sel)

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
			Name:         nic.Name,
			UID:          nic.UID,
			PartitionRef: generic.Pointer(nic.Spec.PartitionRef),
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

func (r *LoadBalancerReconciler) reconcile(ctx context.Context, log logr.Logger, loadBalancer *v1alpha1.LoadBalancer) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	log.V(1).Info("Getting public IPs for load balancer")
	ips, err := r.getPublicIPsForLoadBalancer(ctx, loadBalancer)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error getting public IPs for load balancer: %w", err)
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

	log.V(1).Info("Reconciled")
	return ctrl.Result{}, nil
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
			if loadBalancerReferencesPublicIP(&loadBalancer, publicIP) {
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

func (r *LoadBalancerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.LoadBalancer{}).
		Owns(&v1alpha1.LoadBalancerRouting{}).
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
		Complete(r)
}
