package controllers

import (
	"context"

	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	fieldOwner = client.FieldOwner("apinet.api.onmetal.de/controller-manager")

	natGatewayKind          = "NATGateway"
	natGatewayRoutingKind   = "NATGatewayRouting"
	networkInterfaceKind    = "NetworkInterface"
	loadBalancerKind        = "LoadBalancer"
	loadBalancerRoutingKind = "LoadBalancerRouting"
)

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
