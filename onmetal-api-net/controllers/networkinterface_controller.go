package controllers

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/apiutils"
	"github.com/onmetal/onmetal-api-net/onmetal-api-net/publicip"
	"golang.org/x/exp/slices"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

func networkInterfacePublicIPSelector(nic *v1alpha1.NetworkInterface) func(*v1alpha1.PublicIP) bool {
	namesByIPFamily := make(map[corev1.IPFamily]sets.Set[string])
	for _, publicIPRef := range nic.Spec.PublicIPRefs {
		names, ok := namesByIPFamily[publicIPRef.IPFamily]
		if !ok {
			names = sets.New[string]()
			namesByIPFamily[publicIPRef.IPFamily] = names
		}

		names.Insert(publicIPRef.Name)
	}
	return func(publicIP *v1alpha1.PublicIP) bool {
		return namesByIPFamily[publicIP.Spec.IPFamily].Has(publicIP.Name)
	}
}

func networkInterfaceReferencesPublicIP(nic *v1alpha1.NetworkInterface, publicIP *v1alpha1.PublicIP) bool {
	for _, publicIPRef := range nic.Spec.PublicIPRefs {
		if publicIPRef.IPFamily != publicIP.Spec.IPFamily {
			return false
		}

		if publicIPRef.Name == publicIP.Name {
			return true
		}
	}
	return false
}

type NetworkInterfaceReconciler struct {
	client.Client
}

func (r *NetworkInterfaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	nic := &v1alpha1.NetworkInterface{}
	if err := r.Get(ctx, req.NamespacedName, nic); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		// Delete the corresponding config object if the network interface is gone.
		nicConfig := &v1alpha1.NetworkInterfaceConfig{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: req.Namespace,
				Name:      req.Name,
			},
		}
		if err := r.Delete(ctx, nicConfig); client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, fmt.Errorf("error deleting network interface config: %w", err)
		}
		return ctrl.Result{}, nil
	}

	return r.reconcileExists(ctx, log, nic)
}

func (r *NetworkInterfaceReconciler) reconcileExists(ctx context.Context, log logr.Logger, nic *v1alpha1.NetworkInterface) (ctrl.Result, error) {
	if !nic.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, nic)
	}
	return r.reconcile(ctx, log, nic)
}

func (r *NetworkInterfaceReconciler) delete(ctx context.Context, log logr.Logger, nic *v1alpha1.NetworkInterface) (ctrl.Result, error) {
	log.V(1).Info("Delete")
	log.V(1).Info("Deleted")
	return ctrl.Result{}, nil
}

func (r *NetworkInterfaceReconciler) reconcile(ctx context.Context, log logr.Logger, nic *v1alpha1.NetworkInterface) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	log.V(1).Info("Computing network interface config")
	if err := r.manageNetworkInterfaceConfig(ctx, log, nic); err != nil {
		return ctrl.Result{}, fmt.Errorf("error managing network interface config: %w", err)
	}

	log.V(1).Info("Reconciled")
	return ctrl.Result{}, nil
}

func (r *NetworkInterfaceReconciler) getPublicIPsForNetworkInterface(ctx context.Context, nic *v1alpha1.NetworkInterface) ([]v1alpha1.ExternalIPConfig, error) {
	publicIPList := &v1alpha1.PublicIPList{}
	if err := r.List(ctx, publicIPList,
		client.InNamespace(nic.Namespace),
	); err != nil {
		return nil, fmt.Errorf("error listing public IPs: %w", err)
	}

	var (
		sel       = networkInterfacePublicIPSelector(nic)
		claimMgr  = publicip.NewClaimManager(r.Client, networkInterfaceKind, nic, sel)
		extIPCfgs []v1alpha1.ExternalIPConfig
		errs      []error
	)
	for _, publicIP := range publicIPList.Items {
		ok, err := claimMgr.Claim(ctx, &publicIP)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok {
			continue
		}

		if !apiutils.IsPublicIPAllocated(&publicIP) {
			continue
		}

		extIPCfgs = append(extIPCfgs, v1alpha1.ExternalIPConfig{
			IPFamily: publicIP.Spec.IPFamily,
			SourceRef: &v1alpha1.SourceRef{
				Kind: "PublicIP",
				Name: publicIP.Name,
				UID:  publicIP.UID,
			},
			PublicIP: publicIP.Spec.IP,
		})
	}
	return extIPCfgs, errors.Join(errs...)
}

func (r *NetworkInterfaceReconciler) getIPsForNetworkInterface(ctx context.Context, nic *v1alpha1.NetworkInterface) ([]v1alpha1.IP, error) {
	var (
		ips  []v1alpha1.IP
		errs []error
	)
	for _, nicIP := range nic.Spec.IPs {
		switch {
		case nicIP.IP != nil:
			ips = append(ips, *nicIP.IP)
		default:
			errs = append(errs, fmt.Errorf("invalid network interface IP %#v", nicIP))
		}
	}
	return ips, errors.Join(errs...)
}

func (r *NetworkInterfaceReconciler) computeExternalIPConfigs(ctx context.Context, nic *v1alpha1.NetworkInterface) ([]v1alpha1.ExternalIPConfig, error) {
	publicIPExtIPCfgs, err := r.getPublicIPsForNetworkInterface(ctx, nic)
	if err != nil {
		return nil, fmt.Errorf("error getting public ips for network interface: %w", err)
	}

	return publicIPExtIPCfgs, nil
}

func (r *NetworkInterfaceReconciler) getNetworkConfig(ctx context.Context, nic *v1alpha1.NetworkInterface) (*v1alpha1.NetworkConfig, error) {
	network := &v1alpha1.Network{}
	networkKey := client.ObjectKey{Namespace: nic.Namespace, Name: nic.Spec.NetworkRef.Name}
	if err := r.Get(ctx, networkKey, network); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("error getting network %s: %w", networkKey.Name, err)
		}
		return nil, nil
	}

	if !apiutils.IsNetworkAllocated(network) {
		return nil, nil
	}
	return &v1alpha1.NetworkConfig{
		Name: network.Name,
		VNI:  *network.Spec.VNI,
	}, nil
}

func (r *NetworkInterfaceReconciler) manageNetworkInterfaceConfig(ctx context.Context, log logr.Logger, nic *v1alpha1.NetworkInterface) error {
	networkCfg, err := r.getNetworkConfig(ctx, nic)
	if err != nil {
		return fmt.Errorf("error getting network vni: %w", err)
	}
	if networkCfg == nil {
		return nil
	}

	ips, err := r.getIPsForNetworkInterface(ctx, nic)
	if err != nil {
		return fmt.Errorf("error getting ips: %w", err)
	}

	extIPCfgs, err := r.computeExternalIPConfigs(ctx, nic)
	if err != nil {
		return fmt.Errorf("error computing external ip configs: %w", err)
	}

	nicCfg := &v1alpha1.NetworkInterfaceConfig{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: nic.Namespace,
			Name:      nic.Name,
		},
	}
	// We have to do a CreateOrPatch since apply doesn't quite work without apply configurations.
	// We could use a custom client etc., however this deemed to be too complex.
	// For now, let's go with the simple solution and revisit this in the future.
	// TODO: revisit in the future whether refactoring this makes sense, consider support with NAT gateway ctrl.
	if _, err := controllerutil.CreateOrPatch(ctx, r.Client, nicCfg, func() error {
		nicCfg.Network = *networkCfg
		nicCfg.IPs = ips
		r.setNetworkInterfaceConfigExternalIPs(nicCfg, extIPCfgs)
		return ctrl.SetControllerReference(nic, nicCfg, r.Scheme())
	}); err != nil {
		return fmt.Errorf("error creating / patching network interface config: %w", err)
	}
	return nil
}

func (r *NetworkInterfaceReconciler) setNetworkInterfaceConfigExternalIPs(nicCfg *v1alpha1.NetworkInterfaceConfig, extIPs []v1alpha1.ExternalIPConfig) {
	ipFamilies := sets.New[corev1.IPFamily]()
	for _, extIP := range extIPs {
		ipFamilies.Insert(extIP.IPFamily)
	}

	for _, existingExtIP := range nicCfg.ExternalIPs {
		if existingExtIP.NATIP == nil {
			// Only keep NAT IPs, other IPs are managed by the network interface controller.
			continue
		}

		if ipFamilies.Has(existingExtIP.IPFamily) {
			// If we have an IP for the IP family, don't use what's there.
			continue
		}

		ipFamilies.Insert(existingExtIP.IPFamily)
		extIPs = append(extIPs, existingExtIP)
	}

	nicCfg.ExternalIPs = extIPs
	slices.SortFunc(nicCfg.ExternalIPs, func(cfg1, cfg2 v1alpha1.ExternalIPConfig) bool {
		return cfg1.IPFamily < cfg2.IPFamily
	})
}

func (r *NetworkInterfaceReconciler) enqueueByPublicIPNetworkInterfaceSelection() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		publicIP := obj.(*v1alpha1.PublicIP)
		log := ctrl.LoggerFrom(ctx)

		nicList := &v1alpha1.NetworkInterfaceList{}
		if err := r.List(ctx, nicList,
			client.InNamespace(publicIP.Namespace),
		); err != nil {
			log.Error(err, "Error listing network interfaces")
			return nil
		}

		var reqs []ctrl.Request
		for _, nic := range nicList.Items {
			if networkInterfaceReferencesPublicIP(&nic, publicIP) {
				reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&nic)})
			}
		}
		return reqs
	})
}

func (r *NetworkInterfaceReconciler) enqueueByNetworkNetworkInterfaceRefs() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		network := obj.(*v1alpha1.Network)
		log := ctrl.LoggerFrom(ctx)

		nicList := &v1alpha1.NetworkInterfaceList{}
		if err := r.List(ctx, nicList,
			client.InNamespace(network.Namespace),
		); err != nil {
			log.Error(err, "Error listing network interfaces")
			return nil
		}

		var reqs []ctrl.Request
		for _, nic := range nicList.Items {
			if nic.Spec.NetworkRef.Name == network.Name {
				reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&nic)})
			}
		}
		return reqs
	})
}

func (r *NetworkInterfaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.NetworkInterface{}).
		Owns(&v1alpha1.NetworkInterfaceConfig{}).
		Watches(
			&v1alpha1.Network{},
			r.enqueueByNetworkNetworkInterfaceRefs(),
		).
		Watches(
			&v1alpha1.PublicIP{},
			enqueueByPublicIPClaimerRef(networkInterfaceKind),
		).
		Watches(
			&v1alpha1.PublicIP{},
			r.enqueueByPublicIPNetworkInterfaceSelection(),
			builder.WithPredicates(publicIPClaimablePredicate),
		).
		Complete(r)
}
