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

		conflict, err := r.deleteNetworkInterfaceConfigNATIPsByNATGatewayKey(ctx, log, req.NamespacedName)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("error deleting network interface config NAT IPs by NAT gateway key: %w", err)
		}
		if conflict {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, nil
	}
	return r.reconcileExists(ctx, log, natGateway)
}

func (r *NATGatewayReconciler) deleteNetworkInterfaceConfigNATIPsByNATGatewayKey(ctx context.Context, log logr.Logger, key client.ObjectKey) (conflict bool, err error) {
	nicCfgList := &v1alpha1.NetworkInterfaceConfigList{}
	if err := r.List(ctx, nicCfgList,
		client.InNamespace(key.Namespace),
	); err != nil {
		return false, fmt.Errorf("error listing network interface configs: %w", err)
	}

	var (
		errs []error
	)
	for _, nicCfg := range nicCfgList.Items {
		idx := externalIPConfigIndexBySourceKindAndName(nicCfg.ExternalIPs, natGatewayKind, key.Name)
		if idx < 0 {
			continue
		}

		log.V(2).Info("Deleting network interface config NAT IP")
		if err := r.deleteNetworkInterfaceConfigNATIP(ctx, &nicCfg, idx); err != nil {
			switch {
			case apierrors.IsNotFound(err):
			case apierrors.IsConflict(err):
				conflict = true
			default:
				errs = append(errs, err)
			}
		}
	}
	return conflict, errors.Join(errs...)
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

func networkInterfaceConfigHasIPFamily(cfg *v1alpha1.NetworkInterfaceConfig, ipFamily corev1.IPFamily) bool {
	for _, ip := range cfg.IPs {
		if ip.Family() == ipFamily {
			return true
		}
	}
	return false
}

func natGatewaySelectsNetworkInterfaceConfig(natGateway *v1alpha1.NATGateway, nicCfg *v1alpha1.NetworkInterfaceConfig) (reportsAllocated, ok bool) {
	if nicCfg.Network.Name != natGateway.Spec.NetworkRef.Name {
		// Don't include network interfaces from different networks.
		return false, false
	}

	if !networkInterfaceConfigHasIPFamily(nicCfg, natGateway.Spec.IPFamily) {
		// Don't include network interfaces that don't share an IP family with the NAT gateway.
		return false, false
	}

	if extIP := findExternalIPConfigByIPFamily(nicCfg.ExternalIPs, natGateway.Spec.IPFamily); extIP != nil {
		switch {
		case extIP.PublicIP != nil:
			// Don't include network interfaces that have a public IP for the IP family.
			return false, false
		case extIP.NATIP != nil:
			if extIP.SourceRef != nil && extIP.SourceRef.UID != natGateway.UID {
				// Don't include network interfaces that have a NAT IP from a different NAT gateway.
				return false, false
			}

			return true, true
		default:
			return false, false
		}
	}

	if !nicCfg.DeletionTimestamp.IsZero() {
		// Don't allocate network interface configs that are already deleting.
		return false, false
	}
	return false, true
}

func (r *NATGatewayReconciler) getNATGatewayTargets(ctx context.Context, natGateway *v1alpha1.NATGateway) (reportBound, newTargets []v1alpha1.NetworkInterfaceConfig, err error) {
	nicCfgList := &v1alpha1.NetworkInterfaceConfigList{}
	if err := r.List(ctx, nicCfgList,
		client.InNamespace(natGateway.Namespace),
	); err != nil {
		return nil, nil, fmt.Errorf("error listing network interfaces: %w", err)
	}

	for _, nicCfg := range nicCfgList.Items {
		alloc, ok := natGatewaySelectsNetworkInterfaceConfig(natGateway, &nicCfg)
		if !ok {
			continue
		}

		if alloc {
			reportBound = append(reportBound, nicCfg)
		} else {
			newTargets = append(newTargets, nicCfg)
		}
	}
	return reportBound, newTargets, nil
}

func (r *NATGatewayReconciler) deleteNetworkInterfaceConfigNATIP(ctx context.Context, nicCfg *v1alpha1.NetworkInterfaceConfig, idx int) error {
	base := nicCfg.DeepCopy()
	nicCfg.ExternalIPs = slices.Delete(nicCfg.ExternalIPs, idx, idx+1)
	if err := r.Patch(ctx, nicCfg, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("error patching network interface config: %w", err)
	}
	return nil
}

func (r *NATGatewayReconciler) addNetworkInterfaceConfigNATIP(ctx context.Context, natGateway *v1alpha1.NATGateway, nicCfg *v1alpha1.NetworkInterfaceConfig, natIP v1alpha1.NATIP) error {
	base := nicCfg.DeepCopy()
	nicCfg.ExternalIPs = append(nicCfg.ExternalIPs, v1alpha1.ExternalIPConfig{
		IPFamily: natGateway.Spec.IPFamily,
		SourceRef: &v1alpha1.SourceRef{
			Kind: natGatewayKind,
			Name: natGateway.Name,
			UID:  natGateway.UID,
		},
		NATIP: &natIP,
	})
	slices.SortFunc(nicCfg.ExternalIPs, func(cfg1, cfg2 v1alpha1.ExternalIPConfig) bool {
		return cfg1.IPFamily < cfg2.IPFamily
	})
	if err := r.Patch(ctx, nicCfg, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("error patching network interface config: %w", err)
	}
	return nil
}

func (r *NATGatewayReconciler) allocateNATGatewayDestinations(
	ctx context.Context,
	log logr.Logger,
	natGateway *v1alpha1.NATGateway,
	ips []v1alpha1.IP,
	reportBound, newTargets []v1alpha1.NetworkInterfaceConfig,
) (int64, error) {
	var (
		allocMgr = natgateway.NewAllocationManager(natGateway.Spec.PortsPerNetworkInterface, ips)
		errs     []error
	)

	log.V(1).Info("Determining NAT IPs to keep")
	for _, nicCfg := range reportBound {
		idx := externalIPConfigIndexBySourceUID(nicCfg.ExternalIPs, natGateway.UID)
		natIP := nicCfg.ExternalIPs[idx].NATIP
		log := log.WithValues("NetworkInterfaceKey", client.ObjectKeyFromObject(&nicCfg), "NATIP", natIP)

		if allocMgr.Use(natIP.IP, natIP.Port, natIP.EndPort) {
			log.V(2).Info("Keeping NAT IP allocation")
			continue
		}

		log.V(2).Info("Deleting NAT IP that could not be kept")
		if err := r.deleteNetworkInterfaceConfigNATIP(ctx, &nicCfg, idx); err != nil {
			if !apierrors.IsNotFound(err) {
				errs = append(errs, err)
			}
			continue
		}

		log.V(2).Info("Adding network interface config for re-allocation")
		newTargets = append(newTargets, nicCfg)
	}

	log.V(1).Info("Computing new NAT IPs")
	for _, nicCfg := range newTargets {
		log := log.WithValues("NetworkInterfaceKey", client.ObjectKeyFromObject(&nicCfg))

		ip, port, endPort, ok := allocMgr.UseNextFree()
		if !ok {
			log.V(2).Info("No free NAT IPs available anymore")
			r.Event(natGateway, corev1.EventTypeWarning, "OutOfIPAddresses", "NAT gateway is out of IP addresses.")
			break
		}

		natIP := v1alpha1.NATIP{IP: ip, Port: port, EndPort: endPort}
		log.V(2).Info("Adding NAT IP", "NATIP", natIP)
		if err := r.addNetworkInterfaceConfigNATIP(ctx, natGateway, &nicCfg, natIP); err != nil {
			errs = append(errs, err)
		}
	}

	return allocMgr.Used(), errors.Join(errs...)
}

func (r *NATGatewayReconciler) deleteNetworkInterfaceConfigNATIPsForNATGateway(ctx context.Context, log logr.Logger, natGateway *v1alpha1.NATGateway) (conflict bool, err error) {
	nicCfgList := &v1alpha1.NetworkInterfaceConfigList{}
	if err := r.List(ctx, nicCfgList,
		client.InNamespace(natGateway.Namespace),
	); err != nil {
		return false, fmt.Errorf("error listing network interface configs: %w", err)
	}

	var (
		errs []error
	)
	for _, nicCfg := range nicCfgList.Items {
		idx := externalIPConfigIndexBySourceUID(nicCfg.ExternalIPs, natGateway.UID)
		if idx < 0 {
			continue
		}

		log.V(2).Info("Deleting network interface config NAT IP")
		if err := r.deleteNetworkInterfaceConfigNATIP(ctx, &nicCfg, idx); err != nil {
			switch {
			case apierrors.IsNotFound(err):
			case apierrors.IsConflict(err):
				conflict = true
			default:
				errs = append(errs, err)
			}
		}
	}
	return conflict, errors.Join(errs...)
}

func (r *NATGatewayReconciler) delete(ctx context.Context, log logr.Logger, natGateway *v1alpha1.NATGateway) (ctrl.Result, error) {
	log.V(1).Info("Delete")

	log.V(1).Info("Deleting network interface configs NAT IPs by NAT gateway")
	conflict, err := r.deleteNetworkInterfaceConfigNATIPsForNATGateway(ctx, log, natGateway)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error deleting network interface config NAT IPs: %w", err)
	}
	if conflict {
		return ctrl.Result{Requeue: true}, nil
	}

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
	reportBound, newTargets, err := r.getNATGatewayTargets(ctx, natGateway)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error getting NAT gateway targets: %w", err)
	}

	log.V(1).Info("Allocating NAT gateway destinations")
	usedNATIPs, err := r.allocateNATGatewayDestinations(ctx, log, natGateway, ips, reportBound, newTargets)
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

func (r *NATGatewayReconciler) enqueueByNetworkInterfaceConfigNATIPSourceRef() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		nicCfg := obj.(*v1alpha1.NetworkInterfaceConfig)

		var reqs []ctrl.Request
		for _, extIP := range nicCfg.ExternalIPs {
			if extIP.NATIP == nil || extIP.SourceRef == nil {
				continue
			}

			reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKey{
				Namespace: nicCfg.Namespace,
				Name:      nicCfg.Name,
			}})
		}
		return reqs
	})
}

func (r *NATGatewayReconciler) enqueueByNetworkInterfaceConfigNATGatewaySelection() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		nicCfg := obj.(*v1alpha1.NetworkInterfaceConfig)
		log := ctrl.LoggerFrom(ctx)

		natGatewayList := &v1alpha1.NATGatewayList{}
		if err := r.List(ctx, natGatewayList,
			client.InNamespace(nicCfg.Namespace),
		); err != nil {
			log.Error(err, "Error listing NAT gateways")
			return nil
		}

		var reqs []ctrl.Request
		for _, natGateway := range natGatewayList.Items {
			if _, ok := natGatewaySelectsNetworkInterfaceConfig(&natGateway, nicCfg); ok {
				reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&natGateway)})
			}
		}
		return reqs
	})
}

func (r *NATGatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.NATGateway{}).
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
			&v1alpha1.NetworkInterfaceConfig{},
			r.enqueueByNetworkInterfaceConfigNATIPSourceRef(),
		).
		Watches(
			&v1alpha1.NetworkInterfaceConfig{},
			r.enqueueByNetworkInterfaceConfigNATGatewaySelection(),
		).
		Complete(r)
}
