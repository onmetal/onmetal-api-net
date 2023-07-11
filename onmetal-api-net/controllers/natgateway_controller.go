package controllers

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/apiutils"
	"github.com/onmetal/onmetal-api-net/onmetal-api-net/natgateway"
	"github.com/onmetal/onmetal-api-net/onmetal-api-net/publicip"
	"golang.org/x/exp/slices"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

type NATGatewayReconciler struct {
	client.Client
	record.EventRecorder
}

func (r *NATGatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	natGateway := &v1alpha1.NATGateway{}
	if err := r.Get(ctx, req.NamespacedName, natGateway); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		natGatewayRouting := &v1alpha1.NATGatewayRouting{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: req.Namespace,
				Name:      req.Name,
			},
		}
		if err := r.Delete(ctx, natGatewayRouting); client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, fmt.Errorf("error deleting NAT gateway routing: %w", err)
		}
		return ctrl.Result{}, nil
	}
	return r.reconcileExists(ctx, log, natGateway)
}

func (r *NATGatewayReconciler) reconcileExists(ctx context.Context, log logr.Logger, natGateway *v1alpha1.NATGateway) (ctrl.Result, error) {
	if !natGateway.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, natGateway)
	}
	return r.reconcile(ctx, log, natGateway)
}

func (r *NATGatewayReconciler) getExternallyManagedPublicIPsForNATGateway(ctx context.Context, natGateway *v1alpha1.NATGateway) ([]v1alpha1.IP, error) {
	publicIPList := &v1alpha1.PublicIPList{}
	if err := r.List(ctx, publicIPList,
		client.InNamespace(natGateway.Namespace),
	); err != nil {
		return nil, fmt.Errorf("error listing public ips: %w", err)
	}

	var (
		ips  []v1alpha1.IP
		errs []error
	)
	for i := range publicIPList.Items {
		publicIP := &publicIPList.Items[i]
		if publicIP.Spec.IPFamily != natGateway.Spec.IPFamily {
			// Don't include public IPs with a different IP family.
			continue
		}

		if !apiutils.IsPublicIPClaimedBy(publicIP, natGateway) {
			// Don't include public IPs that are not claimed.
			continue
		}

		if !apiutils.IsPublicIPAllocated(publicIP) {
			continue
		}

		ips = append(ips, *publicIP.Spec.IP)
	}
	return ips, errors.Join(errs...)
}

func (r *NATGatewayReconciler) getAndManagePublicIPsForNATGateway(ctx context.Context, natGateway *v1alpha1.NATGateway) ([]v1alpha1.IP, error) {
	sel, err := metav1.LabelSelectorAsSelector(natGateway.Spec.IPSelector)
	if err != nil {
		return nil, err
	}

	publicIPList := &v1alpha1.PublicIPList{}
	if err := r.List(ctx, publicIPList,
		client.InNamespace(natGateway.Namespace),
	); err != nil {
		return nil, fmt.Errorf("error listing public ips: %w", err)
	}

	var (
		claimMgr = publicip.NewClaimManager(r.Client, natGatewayKind, natGateway, func(publicIP *v1alpha1.PublicIP) bool {
			return natGateway.Spec.IPFamily == publicIP.Spec.IPFamily && sel.Matches(labels.Set(publicIP.Labels))
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

func natGatewaySelectsNetworkInterface(natGateway *v1alpha1.NATGateway, nic *v1alpha1.NetworkInterface, ips []v1alpha1.IP) bool {
	if natGateway.Spec.NetworkRef != nic.Spec.NetworkRef {
		// Don't include network interfaces from different networks.
		return false
	}

	hasIPFamily := slices.ContainsFunc(nic.Spec.IPs, func(ip v1alpha1.IP) bool {
		return ip.Family() == natGateway.Spec.IPFamily
	})
	if !hasIPFamily {
		// Don't include network interfaces that don't share an IP family with the NAT gateway.
		return false
	}

	for _, publicIP := range nic.Spec.PublicIPs {
		if publicIP.IPFamily == natGateway.Spec.IPFamily {
			// Don't include network interfaces that want to allocate a public IP for the IP family.
			return false
		}
	}

	for _, extIP := range nic.Status.ExternalIPs {
		if natIP := extIP.NATIP; natIP != nil {
			if natIP.IP.Family() == natGateway.Spec.IPFamily {
				if slices.Contains(ips, natIP.IP) {
					// If the NAT IP is already allocated, it's a target regardless of deletion or not.
					return true
				}
				// Different NAT IP, don't include.
				return false
			}
		}
	}

	if !nic.DeletionTimestamp.IsZero() {
		// Don't allocate new network interfaces that are already deleting.
		return false
	}
	return true
}

func (r *NATGatewayReconciler) getNATGatewayTargets(ctx context.Context, natGateway *v1alpha1.NATGateway, ips []v1alpha1.IP) (map[types.UID]*v1alpha1.NetworkInterface, error) {
	nicList := &v1alpha1.NetworkInterfaceList{}
	if err := r.List(ctx, nicList,
		client.InNamespace(natGateway.Namespace),
	); err != nil {
		return nil, fmt.Errorf("error listing network interfaces: %w", err)
	}

	nicNameByUID := make(map[types.UID]*v1alpha1.NetworkInterface)
	for i := range nicList.Items {
		nic := &nicList.Items[i]
		if natGatewaySelectsNetworkInterface(natGateway, nic, ips) {
			nicNameByUID[nic.UID] = nic
		}
	}
	return nicNameByUID, nil
}

func (r *NATGatewayReconciler) allocateNATGatewayDestinations(
	ctx context.Context,
	log logr.Logger,
	natGateway *v1alpha1.NATGateway,
	ips []v1alpha1.IP,
	nicByUID map[types.UID]*v1alpha1.NetworkInterface,
) (int64, error) {
	natGatewayRouting := &v1alpha1.NATGatewayRouting{}
	natGatewayRoutingKey := client.ObjectKeyFromObject(natGateway)
	if err := r.Get(ctx, natGatewayRoutingKey, natGatewayRouting); client.IgnoreNotFound(err) != nil {
		return 0, fmt.Errorf("error getting NAT gateway routing: %w", err)
	}

	var (
		allocMgr        = natgateway.NewAllocationManager(natGateway.Spec.PortsPerNetworkInterface, ips)
		newDestinations []v1alpha1.NATGatewayDestination
	)

	log.V(1).Info("Determining NAT IPs to keep")
	for _, dst := range natGatewayRouting.Destinations {
		log := log.WithValues("Destination", dst)
		_, ok := nicByUID[dst.UID]
		if !ok {
			log.V(2).Info("Dropping non-selected destination")
			continue
		}

		if !allocMgr.Use(dst.NATIP.IP, dst.NATIP.Port, dst.NATIP.EndPort) {
			log.V(2).Info("Dropping non-available destination")
			continue
		}

		log.V(2).Info("Re-using destination")
		newDestinations = append(newDestinations, dst)
		delete(nicByUID, dst.UID)
	}

	log.V(1).Info("Determining new NAT IPs")
	for uid, nic := range nicByUID {
		log := log.WithValues("NetworkInterface", nic)
		ip, port, endPort, ok := allocMgr.UseNextFree()
		if !ok {
			log.V(2).Info("No free NAT IPs available anymore")
			r.Event(natGateway, corev1.EventTypeWarning, "OutOfIPAddresses", "NAT gateway is out of IP addresses.")
			break
		}

		newDestinations = append(newDestinations, v1alpha1.NATGatewayDestination{
			Name: nic.Name,
			UID:  uid,
			NATIP: v1alpha1.NATIP{
				IP:      ip,
				Port:    port,
				EndPort: endPort,
			},
			NodeRef: nic.Spec.NodeRef,
		})
	}

	slices.SortFunc(newDestinations, func(a, b v1alpha1.NATGatewayDestination) bool {
		return a.UID < b.UID
	})

	log.V(1).Info("Applying NAT gateway routing")
	natGatewayRouting = &v1alpha1.NATGatewayRouting{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       natGatewayRoutingKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: natGateway.Namespace,
			Name:      natGateway.Name,
		},
		Destinations: newDestinations,
	}
	_ = ctrl.SetControllerReference(natGateway, natGatewayRouting, r.Scheme())
	if err := r.Patch(ctx, natGatewayRouting, client.Apply, fieldOwner, client.ForceOwnership); err != nil {
		return 0, fmt.Errorf("error applying NAT gateway routing: %w", err)
	}
	return allocMgr.Used(), nil
}

func (r *NATGatewayReconciler) delete(ctx context.Context, log logr.Logger, natGateway *v1alpha1.NATGateway) (ctrl.Result, error) {
	log.V(1).Info("Delete")
	log.V(1).Info("Deleted")
	return ctrl.Result{}, nil
}

func (r *NATGatewayReconciler) updateNATGatewayIPs(ctx context.Context, natGateway *v1alpha1.NATGateway, ips []v1alpha1.IP) error {
	base := natGateway.DeepCopy()
	natGateway.Status.IPs = ips
	if err := r.Status().Patch(ctx, natGateway, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("error patching nat gateway status: %w", err)
	}
	return nil
}

func (r *NATGatewayReconciler) updateNATGatewayUsedNATIPs(ctx context.Context, natGateway *v1alpha1.NATGateway, usedNATIPs int64) error {
	base := natGateway.DeepCopy()
	natGateway.Status.UsedNATIPs = usedNATIPs
	if err := r.Status().Patch(ctx, natGateway, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("error patching nat gateway status: %w", err)
	}
	return nil
}

func (r *NATGatewayReconciler) reconcile(ctx context.Context, log logr.Logger, natGateway *v1alpha1.NATGateway) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	var ips []v1alpha1.IP

	if natGateway.Spec.IPSelector == nil {
		log.V(1).Info("Getting externally managed public IPs for NAT gateway")
		externallyManagedPublicIPs, err := r.getExternallyManagedPublicIPsForNATGateway(ctx, natGateway)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("error getting externally managed public IPs for NAT gateway: %w", err)
		}

		ips = externallyManagedPublicIPs
	} else {
		log.V(1).Info("Getting and managing public IPs for NAT gateway")
		managedPublicIPs, err := r.getAndManagePublicIPsForNATGateway(ctx, natGateway)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("error getting public ips for nat gateway: %w", err)
		}

		ips = managedPublicIPs
	}
	log = log.WithValues("IPs", ips)

	if !slices.Equal(natGateway.Status.IPs, ips) {
		log.V(1).Info("Updating NAT gateway status IPs")
		if err := r.updateNATGatewayIPs(ctx, natGateway, ips); err != nil {
			return ctrl.Result{}, fmt.Errorf("error updating nat gateway status ips: %w", err)
		}
		log.V(1).Info("Updated NAT gateway status IPs")
		return ctrl.Result{Requeue: true}, nil
	}

	log.V(1).Info("Getting network interfaces for NAT gateway")
	nicByUID, err := r.getNATGatewayTargets(ctx, natGateway, ips)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error getting NAT gateway targets: %w", err)
	}

	log.V(1).Info("Allocating NAT gateway destinations")
	usedNATIPs, err := r.allocateNATGatewayDestinations(ctx, log, natGateway, ips, nicByUID)
	if err != nil {
		if !apierrors.IsConflict(err) {
			return ctrl.Result{}, fmt.Errorf("error allocating network interfaces: %w", err)
		}
		log.V(1).Info("Requeuing due to conflict")
		return ctrl.Result{Requeue: true}, nil
	}

	if natGateway.Status.UsedNATIPs != usedNATIPs {
		log.V(1).Info("Updating NAT gateway status used NAT IPs")
		if err := r.updateNATGatewayUsedNATIPs(ctx, natGateway, usedNATIPs); err != nil {
			return ctrl.Result{}, fmt.Errorf("error updating NAT gateway status used NAT IPs: %w", err)
		}
	}

	log.V(1).Info("Reconciled")
	return ctrl.Result{}, nil
}

func (r *NATGatewayReconciler) enqueueByPublicIPNATGatewaySelection() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		publicIP := obj.(*v1alpha1.PublicIP)
		log := ctrl.LoggerFrom(ctx)

		natGatewayList := &v1alpha1.NATGatewayList{}
		if err := r.List(ctx, natGatewayList,
			client.InNamespace(publicIP.Namespace),
		); err != nil {
			log.Error(err, "Error listing NAT gateways")
			return nil
		}

		var reqs []ctrl.Request
		for _, natGateway := range natGatewayList.Items {
			if natGateway.Spec.IPFamily != publicIP.Spec.IPFamily {
				// Don't include NAT gateways with different IP families.
				continue
			}

			sel, err := metav1.LabelSelectorAsSelector(natGateway.Spec.IPSelector)
			if err != nil {
				continue
			}

			if sel.Matches(labels.Set(publicIP.Labels)) {
				reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&natGateway)})
			}
		}
		return reqs
	})
}

func (r *NATGatewayReconciler) enqueueByNetworkInterfaceNATGatewaySelection() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		nic := obj.(*v1alpha1.NetworkInterface)
		log := ctrl.LoggerFrom(ctx)

		natGatewayList := &v1alpha1.NATGatewayList{}
		if err := r.List(ctx, natGatewayList,
			client.InNamespace(nic.Namespace),
		); err != nil {
			log.Error(err, "Error listing NAT gateways")
			return nil
		}

		var reqs []ctrl.Request
		for _, natGateway := range natGatewayList.Items {
			if natGatewaySelectsNetworkInterface(&natGateway, nic, natGateway.Status.IPs) {
				reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&natGateway)})
			}
		}
		return reqs
	})
}

func (r *NATGatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.NATGateway{}).
		Owns(&v1alpha1.NATGatewayRouting{}).
		Watches(
			&v1alpha1.PublicIP{},
			enqueueByPublicIPClaimerRef(natGatewayKind),
		).
		Watches(
			&v1alpha1.PublicIP{},
			r.enqueueByPublicIPNATGatewaySelection(),
			builder.WithPredicates(publicIPClaimablePredicate),
		).
		Watches(
			&v1alpha1.NetworkInterface{},
			r.enqueueByNetworkInterfaceNATGatewaySelection(),
		).
		Complete(r)
}
