package controllers

import (
	"context"

	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	fieldOwner = client.FieldOwner("apinet.api.onmetal.de/controller-manager")

	natGatewayKind             = "NATGateway"
	networkInterfaceKind       = "NetworkInterface"
	networkInterfaceConfigKind = "NetworkInterfaceConfig"
	loadBalancerKind           = "LoadBalancer"
	loadBalancerRoutingKind    = "LoadBalancerRouting"
)

func externalIPConfigIndexBySourceKindAndName(externalIPs []v1alpha1.ExternalIPConfig, sourceKind, sourceName string) int {
	for i, externalIP := range externalIPs {
		sourceRef := externalIP.SourceRef
		if sourceRef == nil {
			continue
		}

		if sourceRef.Kind == sourceKind && sourceRef.Name == sourceName {
			return i
		}
	}
	return -1
}

func externalIPConfigIndexBySourceUID(externalIPs []v1alpha1.ExternalIPConfig, sourceUID types.UID) int {
	for i, externalIP := range externalIPs {
		sourceRef := externalIP.SourceRef
		if sourceRef == nil {
			continue
		}

		if sourceRef.UID == sourceUID {
			return i
		}
	}
	return -1
}

func externalIPConfigIndexByIPFamily(externalIPs []v1alpha1.ExternalIPConfig, ipFamily corev1.IPFamily) int {
	for i, externalIP := range externalIPs {
		if externalIP.IPFamily == ipFamily {
			return i
		}
	}
	return -1
}

func findExternalIPConfigByIPFamily(externalIPs []v1alpha1.ExternalIPConfig, ipFamily corev1.IPFamily) *v1alpha1.ExternalIPConfig {
	if idx := externalIPConfigIndexByIPFamily(externalIPs, ipFamily); idx >= 0 {
		return &externalIPs[idx]
	}
	return nil
}

func enqueueByPublicIPClaimerRef(claimerKind string) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		publicIP := obj.(*v1alpha1.PublicIP)
		claimRef := publicIP.Spec.ClaimerRef
		if claimRef == nil || claimRef.Kind != claimerKind {
			return nil
		}
		return []ctrl.Request{{NamespacedName: client.ObjectKey{
			Namespace: publicIP.Namespace,
			Name:      claimRef.Name,
		}}}
	})
}

var publicIPClaimablePredicate = predicate.NewPredicateFuncs(func(obj client.Object) bool {
	publicIP := obj.(*v1alpha1.PublicIP)
	if !publicIP.DeletionTimestamp.IsZero() {
		// Don't try to claim anything that is deleting.
		return false
	}
	return publicIP.Spec.ClaimerRef == nil
})

var publicIPClaimedPredicate = predicate.NewPredicateFuncs(func(obj client.Object) bool {
	publicIP := obj.(*v1alpha1.PublicIP)
	return publicIP.Spec.ClaimerRef != nil
})

func minInt32(n1, n2 int32) int32 {
	if n1 < n2 {
		return n1
	}
	return n2
}

func minInt(n1, n2 int) int {
	if n1 < n2 {
		return n1
	}
	return n2
}

func maxInt(n1, n2 int) int {
	if n1 > n2 {
		return n1
	}
	return n2
}

func maxInt32(n1, n2 int32) int32 {
	if n1 > n2 {
		return n1
	}
	return n2
}
