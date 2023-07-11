package controllers

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/onmetal/controller-utils/clientutils"
	metalnetv1alpha1 "github.com/onmetal/metalnet/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/apiutils"
	metalnetletclient "github.com/onmetal/onmetal-api-net/metalnetlet/client"
	"github.com/onmetal/onmetal-api-net/onmetal-api-net/publicip"
	"golang.org/x/exp/slices"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	networkInterfaceNamespaceLabel = "metalnetlet.apinet.onmetal.de/networkinterface-namespace"
	networkInterfaceNameLabel      = "metalnetlet.apinet.onmetal.de/networkinterface-name"
	networkInterfaceUIDLabel       = "metalnetlet.apinet.onmetal.de/networkinterface-uid"
)

type NetworkInterfaceReconciler struct {
	client.Client
	MetalnetClient client.Client

	PartitionName string

	MetalnetNamespace string
}

func (r *NetworkInterfaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	nic := &v1alpha1.NetworkInterface{}
	if err := r.Get(ctx, req.NamespacedName, nic); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		log.V(1).Info("Deleting all matching metalnet network interfaces")
		exists, err := metalnetletclient.DeleteAllOfAndAnyExists(ctx, r.MetalnetClient, &metalnetv1alpha1.NetworkInterface{},
			client.InNamespace(r.MetalnetNamespace),
			client.MatchingLabels{
				networkInterfaceNamespaceLabel: nic.Namespace,
				networkInterfaceNameLabel:      nic.Name,
			},
		)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("error deleting matching metalnet network interfaces: %w", err)
		}
		if exists {
			log.V(1).Info("Matching metalnet network interfaces are still present, requeueing")
			return ctrl.Result{Requeue: true}, nil
		}
		log.V(1).Info("All matching metalnet network interfaces are gone")
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
	if !controllerutil.ContainsFinalizer(nic, PartitionFinalizer(r.PartitionName)) {
		log.V(1).Info("No partition finalizer present, nothing to do")
		return ctrl.Result{}, nil
	}
	log.V(1).Info("Partition finalizer present, doing cleanup")

	log.V(1).Info("Deleting any matching metalnet network interface")
	metalnetNic := &metalnetv1alpha1.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.MetalnetNamespace,
			Name:      string(nic.UID),
		},
	}
	if err := r.MetalnetClient.Delete(ctx, metalnetNic); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("error deleting metalnet network interface %s: %w", metalnetNic.Name, err)
		}
		log.V(1).Info("Any matching metalnet network interface deleted")

		log.V(1).Info("Removing finalizer")
		if err := clientutils.PatchRemoveFinalizer(ctx, r.MetalnetClient, nic, PartitionFinalizer(r.PartitionName)); err != nil {
			return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
		}
		log.V(1).Info("Finalizer removed")
		return ctrl.Result{}, nil
	}
	log.V(1).Info("Issued metalnet network interface deletion, requeueing")
	return ctrl.Result{Requeue: true}, nil
}

func (r *NetworkInterfaceReconciler) getNetworkVNI(ctx context.Context, nic *v1alpha1.NetworkInterface) (int32, error) {
	network := &v1alpha1.Network{}
	networkKey := client.ObjectKey{Namespace: nic.Namespace, Name: nic.Spec.NetworkRef.Name}
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

func (r *NetworkInterfaceReconciler) getPublicIPs(ctx context.Context, nic *v1alpha1.NetworkInterface, previousPublicIPs []v1alpha1.IP) ([]v1alpha1.IP, error) {
	publicIPList := &v1alpha1.PublicIPList{}
	if err := r.List(ctx, publicIPList,
		client.InNamespace(nic.Namespace),
	); err != nil {
		return nil, fmt.Errorf("error listing public IPs: %w", err)
	}

	previousPublicIPByFamily := make(map[corev1.IPFamily]v1alpha1.IP)
	for _, previousPublicIP := range previousPublicIPs {
		previousPublicIPByFamily[previousPublicIP.Family()] = previousPublicIP
	}

	ipByIPFamily := make(map[corev1.IPFamily][]v1alpha1.PublicIP)
	for _, publicIP := range publicIPList.Items {
		ipByIPFamily[publicIP.Spec.IPFamily] = append(ipByIPFamily[publicIP.Spec.IPFamily], publicIP)
	}

	var (
		unhandledIPFamilies = sets.New[corev1.IPFamily](corev1.IPv4Protocol, corev1.IPv6Protocol)
		ips                 []v1alpha1.IP
		errs                []error
	)
	for _, nicPublicIP := range nic.Spec.PublicIPs {
		var ip v1alpha1.IP
		unhandledIPFamilies.Delete(nicPublicIP.IPFamily)

		if publicIPRef := nicPublicIP.PublicIPRef; publicIPRef != nil {
			var err error
			ip, err = r.getAndManagePublicIPForIPFamily(ctx, nic, ipByIPFamily[nicPublicIP.IPFamily], publicIPRef.Name)
			if err != nil {
				errs = append(errs, err)
				continue
			}
		} else {
			var err error
			ip, err = r.getExternallyManagedPublicIPForIPFamily(nic, ipByIPFamily[nicPublicIP.IPFamily], previousPublicIPByFamily[nicPublicIP.IPFamily])
			if err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if ip.IsZero() {
			continue
		}

		ips = append(ips, ip)
	}
	for ipFamily := range unhandledIPFamilies {
		if err := r.releaseAnyPublicIPForIPFamily(ctx, nic, ipByIPFamily[ipFamily]); err != nil {
			errs = append(errs, err)
		}
	}
	return ips, errors.Join(errs...)
}

// getExternallyManagedPublicIPForIPFamily gets all externally managed public IPs for an IP family.
// It is assumed that all passed publicIPs are of that ip family.
func (r *NetworkInterfaceReconciler) getExternallyManagedPublicIPForIPFamily(
	nic *v1alpha1.NetworkInterface,
	publicIPs []v1alpha1.PublicIP,
	lastPublicIP v1alpha1.IP,
) (v1alpha1.IP, error) {
	var potentialPublicIP v1alpha1.IP
	for _, publicIP := range publicIPs {
		if !apiutils.IsPublicIPClaimedBy(&publicIP, nic) {
			continue
		}

		if !apiutils.IsPublicIPAllocated(&publicIP) {
			continue
		}

		if lastPublicIP.IsZero() || lastPublicIP.Addr == publicIP.Spec.IP.Addr {
			// We didn't have a previous public IP or if we had one and it's the same we can immediately
			// return here as this is the 'perfect' choice.
			return *publicIP.Spec.IP, nil
		}

		if !potentialPublicIP.IsZero() {
			// We already recorded a potential match, don't overwrite it.
			continue
		}

		// Store the potential match to use in case no 'perfect' match is encountered.
		potentialPublicIP = *publicIP.Spec.IP
	}
	return potentialPublicIP, nil
}

// getAndManagePublicIPForIPFamily gets and manages public IPs for a certain IP family.
// It is assumed that all given publicIPs are of the same ip family.
func (r *NetworkInterfaceReconciler) getAndManagePublicIPForIPFamily(
	ctx context.Context,
	nic *v1alpha1.NetworkInterface,
	publicIPs []v1alpha1.PublicIP,
	name string,
) (v1alpha1.IP, error) {
	var (
		sel      = func(ip *v1alpha1.PublicIP) bool { return ip.Name == name }
		claimMgr = publicip.NewClaimManager(r.Client, NetworkInterfaceKind, nic, sel)

		ip   v1alpha1.IP
		errs []error
	)

	for _, publicIP := range publicIPs {
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

		ip = *publicIP.Spec.IP
	}
	return ip, errors.Join(errs...)
}

// releaseAnyPublicIPForIPFamily releases any public IPs for a certain IP family.
// It is assumed that all given publicIPs are of the same ip family.
func (r *NetworkInterfaceReconciler) releaseAnyPublicIPForIPFamily(
	ctx context.Context,
	nic *v1alpha1.NetworkInterface,
	publicIPs []v1alpha1.PublicIP,
) error {
	var (
		// We always return false to issue a release on each public IP.
		sel      = func(*v1alpha1.PublicIP) bool { return false }
		claimMgr = publicip.NewClaimManager(r.Client, NetworkInterfaceKind, nic, sel)

		errs []error
	)

	for _, publicIP := range publicIPs {
		if _, err := claimMgr.Claim(ctx, &publicIP); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *NetworkInterfaceReconciler) getLoadBalancerTargets(ctx context.Context, nic *v1alpha1.NetworkInterface) ([]v1alpha1.IP, error) {
	lbRoutingList := &v1alpha1.LoadBalancerRoutingList{}
	if err := r.List(ctx, lbRoutingList,
		client.InNamespace(nic.Namespace),
	); err != nil {
		return nil, fmt.Errorf("error listing load balancer routings: %w", err)
	}

	ipSet := sets.New[v1alpha1.IP]()
	for _, lbRouting := range lbRoutingList.Items {
		hasDst := slices.ContainsFunc(lbRouting.Destinations,
			func(dst v1alpha1.LoadBalancerDestination) bool { return dst.UID == nic.UID },
		)
		if hasDst {
			ipSet.Insert(lbRouting.IPs...)
		}
	}

	ips := ipSet.UnsortedList()
	slices.SortFunc(ips, func(ip1, ip2 v1alpha1.IP) bool { return ip1.Compare(ip2.Addr) < 0 })
	return ips, nil
}

func (r *NetworkInterfaceReconciler) getNATIPs(
	ctx context.Context,
	nic *v1alpha1.NetworkInterface,
	publicIPs []v1alpha1.IP,
	previousNATIPs []v1alpha1.NATIP,
) ([]v1alpha1.NATIP, error) {
	natIPFamilies := sets.New[corev1.IPFamily](ipsIPFamilies(nic.Spec.IPs)...).
		Delete(ipsIPFamilies(publicIPs)...)

	if natIPFamilies.Len() == 0 {
		// Short circuit if no NAT IP should be allocated.
		return nil, nil
	}

	previousNATIPByIPFamily := make(map[corev1.IPFamily]v1alpha1.NATIP)
	for _, previousNATIP := range previousNATIPs {
		previousNATIPByIPFamily[previousNATIP.IP.Family()] = previousNATIP
	}

	natGatewayRoutingList := &v1alpha1.NATGatewayRoutingList{}
	if err := r.List(ctx, natGatewayRoutingList,
		client.InNamespace(nic.Namespace),
	); err != nil {
		return nil, fmt.Errorf("error listing NAT gateway routings: %w", err)
	}

	natGatewayRoutingsByIPFamily := make(map[corev1.IPFamily][]v1alpha1.NATGatewayRouting)
	for _, natGatewayRouting := range natGatewayRoutingList.Items {
		natGatewayRoutingsByIPFamily[natGatewayRouting.IPFamily] = append(
			natGatewayRoutingsByIPFamily[natGatewayRouting.IPFamily],
			natGatewayRouting,
		)
	}

	var (
		newNATIPs []v1alpha1.NATIP
		errs      []error
	)
	for ipFamily := range natIPFamilies {
		natIP, err := r.getNATIPForIPFamily(nic, natGatewayRoutingsByIPFamily[ipFamily], previousNATIPByIPFamily[ipFamily])
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !natIP.IP.IsZero() {
			newNATIPs = append(newNATIPs, natIP)
		}
	}
	return newNATIPs, errors.Join(errs...)
}

func (r *NetworkInterfaceReconciler) getNATIPForIPFamily(
	nic *v1alpha1.NetworkInterface,
	natGatewayRoutings []v1alpha1.NATGatewayRouting,
	previousNATIP v1alpha1.NATIP,
) (v1alpha1.NATIP, error) {
	var potentialNATIP v1alpha1.NATIP
	for _, natGatewayRouting := range natGatewayRoutings {
		for _, dst := range natGatewayRouting.Destinations {
			if dst.UID != nic.UID {
				continue
			}

			if previousNATIP.IP.IsZero() || previousNATIP == dst.NATIP {
				// We didn't have a previous NAT IP or if we had one and it's the same we can immediately
				// return here as this is the 'perfect' choice.
				return dst.NATIP, nil
			}

			if !potentialNATIP.IP.IsZero() {
				// We already recorded a potential match, don't overwrite it.
				continue
			}

			// Store the potential match to use in case no 'perfect' match is encountered.
			potentialNATIP = dst.NATIP
		}
	}
	return potentialNATIP, nil
}

func (r *NetworkInterfaceReconciler) updateStatus(ctx context.Context, nic *v1alpha1.NetworkInterface, metalnetNic *metalnetv1alpha1.NetworkInterface) error {
	var externalIPs []v1alpha1.ExternalIP
	if virtualIP := metalnetNic.Status.VirtualIP; virtualIP != nil {
		ip := metalnetIPToIP(*virtualIP)
		externalIPs = append(externalIPs, v1alpha1.ExternalIP{
			IP: &ip,
		})
	}
	if metalnetNATIP := workaroundMetalnetNoNATIPReportingStatusNATIP(metalnetNic); metalnetNATIP != nil {
		natIP := metalnetNATDetailsToNATIP(*metalnetNATIP)
		externalIPs = append(externalIPs, v1alpha1.ExternalIP{
			NATIP: &natIP,
		})
	}

	base := nic.DeepCopy()
	nic.Status.IPs = metalnetIPsToIPs(metalnetNic.Spec.IPs)
	nic.Status.ExternalIPs = externalIPs
	nic.Status.Prefixes = metalnetIPPrefixesToIPPrefixes(metalnetNic.Spec.Prefixes)
	if err := r.Status().Patch(ctx, nic, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("error patching network interface status: %w", err)
	}
	return nil
}

func (r *NetworkInterfaceReconciler) deleteMatchingMetalnetNetworkInterface(ctx context.Context, nic *v1alpha1.NetworkInterface) (existed bool, err error) {
	metalnetNic := &metalnetv1alpha1.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.MetalnetNamespace,
			Name:      string(nic.UID),
		},
	}
	if err := r.Delete(ctx, metalnetNic); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("error deleting metalnet network interface %s: %w", metalnetNic.Name, err)
		}

		return false, nil
	}
	return true, nil
}

func (r *NetworkInterfaceReconciler) reconcile(ctx context.Context, log logr.Logger, nic *v1alpha1.NetworkInterface) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	metalnetNode, err := GetMetalnetNode(ctx, r.PartitionName, r.MetalnetClient, nic.Spec.NodeRef.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	if metalnetNode == nil || !metalnetNode.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(nic, PartitionFinalizer(r.PartitionName)) {
			log.V(1).Info("Finalizer not present and metalnet node not found / deleting, nothing to do")
			return ctrl.Result{}, nil
		}

		log.V(1).Info("Finalizer present but metalnet node not found / deleting, cleaning up")
		existed, err := r.deleteMatchingMetalnetNetworkInterface(ctx, nic)
		if err != nil {
			return ctrl.Result{}, err
		}
		if existed {
			log.V(1).Info("Issued metalnet network interface deletion, requeueing")
			return ctrl.Result{Requeue: true}, nil
		}

		log.V(1).Info("Metalnet network interface is gone, removing partition finalizer")
		if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, nic, PartitionFinalizer(r.PartitionName)); err != nil {
			return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
		}
		log.V(1).Info("Removed partition finalizer")
		return ctrl.Result{}, nil
	}
	log.V(1).Info("Metalnet node present and not deleting, ensuring finalizer")
	modified, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, nic, PartitionFinalizer(r.PartitionName))
	if err != nil {
		return ctrl.Result{}, err
	}
	if modified {
		log.V(1).Info("Added finalizer, requeueing")
		return ctrl.Result{Requeue: true}, nil
	}
	log.V(1).Info("Finalizer is present")

	metalnetNic := &metalnetv1alpha1.NetworkInterface{}
	metalnetNicKey := client.ObjectKey{Namespace: r.MetalnetNamespace, Name: string(nic.UID)}
	log.V(1).Info("Getting metalnet network interface if exists")
	if err := r.MetalnetClient.Get(ctx, metalnetNicKey, metalnetNic); client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, fmt.Errorf("error getting metalnet network interface %s: %w", nic.UID, err)
	}

	if !metalnetNic.DeletionTimestamp.IsZero() {
		log.V(1).Info("Metalnet network interface is deleting, only updating status")
		if err := r.updateStatus(ctx, nic, metalnetNic); err != nil {
			return ctrl.Result{}, fmt.Errorf("error updating status: %w", err)
		}
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Managing metalnet network interface")
	metalnetNic, ready, err := r.manageMetalnetNetworkInterface(ctx, log, nic, metalnetNode.Name, metalnetNic)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error managing metalnet network interface: %w", err)
	}
	if !ready {
		log.V(1).Info("Metalnet network interface is not yet ready")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Updating status")
	if err := r.updateStatus(ctx, nic, metalnetNic); err != nil {
		return ctrl.Result{}, fmt.Errorf("error updating status: %w", err)
	}

	log.V(1).Info("Reconciled")
	return ctrl.Result{}, nil
}

func (r *NetworkInterfaceReconciler) manageMetalnetNetworkInterface(ctx context.Context, log logr.Logger, nic *v1alpha1.NetworkInterface, metalnetNodeName string, metalnetNic *metalnetv1alpha1.NetworkInterface) (*metalnetv1alpha1.NetworkInterface, bool, error) {
	log.V(1).Info("Getting network vni")
	vni, err := r.getNetworkVNI(ctx, nic)
	if err != nil {
		return nil, false, err
	}
	if vni < 0 {
		log.V(1).Info("Network is not yet ready")
		return nil, false, nil
	}

	log.V(1).Info("Getting public IPs")
	publicIPs, err := r.getPublicIPs(ctx, nic, metalnetIPsToIPs(workaroundMetalnetNoIPv6VirtualIPToIPs(metalnetNic.Spec.VirtualIP)))
	if err != nil {
		return nil, false, fmt.Errorf("error getting public IPs: %w", err)
	}

	log.V(1).Info("Getting load balancer targets")
	targets, err := r.getLoadBalancerTargets(ctx, nic)
	if err != nil {
		return nil, false, fmt.Errorf("error getting load balancer targets: %w", err)
	}

	log.V(1).Info("Getting NAT IPs")
	natIPs, err := r.getNATIPs(ctx, nic, publicIPs, metalnetNATDetailsToNATIPs(workaroundMetalnetNoIPv6NATDetailsPointerToNATDetails(metalnetNic.Spec.NAT)))
	if err != nil {
		return nil, false, fmt.Errorf("error getting NAT IPs: %w", err)
	}

	metalnetNic = &metalnetv1alpha1.NetworkInterface{
		TypeMeta: metav1.TypeMeta{
			APIVersion: metalnetv1alpha1.GroupVersion.String(),
			Kind:       "NetworkInterface",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.MetalnetNamespace,
			Name:      string(nic.UID),
			Labels: map[string]string{
				networkInterfaceNamespaceLabel: nic.Namespace,
				networkInterfaceNameLabel:      nic.Name,
				networkInterfaceUIDLabel:       string(nic.UID),
			},
		},
		Spec: metalnetv1alpha1.NetworkInterfaceSpec{
			NetworkRef: corev1.LocalObjectReference{
				Name: MetalnetNetworkName(vni),
			},
			IPFamilies:          ipsIPFamilies(nic.Spec.IPs),
			IPs:                 ipsToMetalnetIPs(nic.Spec.IPs),
			VirtualIP:           workaroundMetalnetNoIPv6VirtualIPSupportIPsToIP(ipsToMetalnetIPs(publicIPs)),
			Prefixes:            ipPrefixesToMetalnetPrefixes(nic.Spec.Prefixes),
			LoadBalancerTargets: ipsToMetalnetIPPrefixes(targets),
			NAT:                 workaroundMetalnetNoIPv6NATDetailsToNATDetailsPointer(natIPsToMetalnetNATDetails(natIPs)),
			NodeName:            &metalnetNodeName,
		},
	}
	log.V(1).Info("Applying metalnet network interface")
	if err := r.MetalnetClient.Patch(ctx, metalnetNic, client.Apply, MetalnetFieldOwner, client.ForceOwnership); err != nil {
		return nil, false, fmt.Errorf("error applying metalnet network interface: %w", err)
	}
	return metalnetNic, true, nil
}

func (r *NetworkInterfaceReconciler) isPartitionNetworkInterface() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		nic := obj.(*v1alpha1.NetworkInterface)
		_, err := ParseNodeName(r.PartitionName, nic.Spec.NodeRef.Name)
		return err == nil
	})
}

func (r *NetworkInterfaceReconciler) enqueueByMetalnetNetworkInterface() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		metalnetNic := obj.(*metalnetv1alpha1.NetworkInterface)
		namespace, ok := metalnetNic.Labels[networkInterfaceNamespaceLabel]
		if !ok {
			return nil
		}

		name, ok := metalnetNic.Labels[networkInterfaceNameLabel]
		if !ok {
			return nil
		}

		return []ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: namespace, Name: name}}}
	})
}

func (r *NetworkInterfaceReconciler) enqueueByPublicIPClaimer() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		publicIP := obj.(*v1alpha1.PublicIP)
		log := ctrl.LoggerFrom(ctx)

		claimerRef := publicIP.Spec.ClaimerRef
		if claimerRef == nil || claimerRef.Kind != "NetworkInterface" {
			return nil
		}

		nic := &v1alpha1.NetworkInterface{}
		nicKey := client.ObjectKey{Namespace: publicIP.Namespace, Name: claimerRef.Name}
		if err := r.Get(ctx, nicKey, nic); err != nil {
			if !apierrors.IsNotFound(err) {
				log.Error(err, "Error getting network interface", "NetworkInterfaceName", nicKey.Name)
				return nil
			}
			return nil
		}

		if _, err := ParseNodeName(r.PartitionName, nic.Spec.NodeRef.Name); err != nil {
			// Only enqueue network interfaces that are hosted on our nodes.
			return nil
		}

		return []ctrl.Request{{NamespacedName: nicKey}}
	})
}

var publicIPClaimablePredicate = predicate.NewPredicateFuncs(func(obj client.Object) bool {
	publicIP := obj.(*v1alpha1.PublicIP)
	if !publicIP.DeletionTimestamp.IsZero() {
		return false
	}
	return publicIP.Spec.ClaimerRef == nil
})

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
			if _, err := ParseNodeName(r.PartitionName, nic.Spec.NodeRef.Name); err != nil {
				// Don't include network interfaces from different nodes.
				continue
			}

			for _, nicPublicIP := range nic.Spec.PublicIPs {
				if nicPublicIP.IPFamily != publicIP.Spec.IPFamily {
					continue
				}

				publicIPRef := nicPublicIP.PublicIPRef
				if publicIPRef == nil {
					continue
				}

				if publicIPRef.Name == publicIP.Name {
					reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&nic)})
				}
			}
		}
		return reqs
	})
}

func (r *NetworkInterfaceReconciler) enqueueByNATGatewayRouting() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		natGatewayRouting := obj.(*v1alpha1.NATGatewayRouting)

		var reqs []ctrl.Request
		for _, dst := range natGatewayRouting.Destinations {
			if _, err := ParseNodeName(r.PartitionName, dst.NodeRef.Name); err != nil {
				continue
			}

			reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKey{
				Namespace: natGatewayRouting.Namespace,
				Name:      dst.Name,
			}})
		}
		return reqs
	})
}

func (r *NetworkInterfaceReconciler) enqueueByLoadBalancerRouting() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		loadBalancerRouting := obj.(*v1alpha1.LoadBalancerRouting)

		var reqs []ctrl.Request
		for _, dst := range loadBalancerRouting.Destinations {
			if _, err := ParseNodeName(r.PartitionName, dst.NodeRef.Name); err != nil {
				continue
			}

			reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKey{
				Namespace: loadBalancerRouting.Namespace,
				Name:      dst.Name,
			}})
		}
		return reqs
	})
}

func (r *NetworkInterfaceReconciler) enqueueByMetalnetNode() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		metalnetNode := obj.(*corev1.Node)
		log := ctrl.LoggerFrom(ctx)

		nicList := &v1alpha1.NetworkInterfaceList{}
		if err := r.List(ctx, nicList); err != nil {
			log.Error(err, "Error listing network interfaces")
			return nil
		}

		var (
			nodeName = PartitionNodeName(r.PartitionName, metalnetNode.Name)
			reqs     []ctrl.Request
		)
		for _, nic := range nicList.Items {
			if nic.Spec.NodeRef.Name != nodeName {
				continue
			}

			reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&nic)})
		}
		return reqs
	})
}

func (r *NetworkInterfaceReconciler) SetupWithManager(mgr ctrl.Manager, metalnetCache cache.Cache) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(
			&v1alpha1.NetworkInterface{},
			builder.WithPredicates(r.isPartitionNetworkInterface()),
		).
		Watches(
			&v1alpha1.PublicIP{},
			r.enqueueByPublicIPClaimer(),
		).
		Watches(
			&v1alpha1.PublicIP{},
			r.enqueueByPublicIPNetworkInterfaceSelection(),
			builder.WithPredicates(publicIPClaimablePredicate),
		).
		Watches(
			&v1alpha1.NATGatewayRouting{},
			r.enqueueByNATGatewayRouting(),
		).
		Watches(
			&v1alpha1.LoadBalancerRouting{},
			r.enqueueByLoadBalancerRouting(),
		).
		WatchesRawSource(
			source.Kind(metalnetCache, &metalnetv1alpha1.NetworkInterface{}),
			r.enqueueByMetalnetNetworkInterface(),
		).
		WatchesRawSource(
			source.Kind(metalnetCache, &corev1.Node{}),
			r.enqueueByMetalnetNode(),
		).
		Complete(r)
}
