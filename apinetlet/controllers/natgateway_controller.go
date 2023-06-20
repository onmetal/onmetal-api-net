package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/onmetal/controller-utils/clientutils"
	apinetv1alpha1 "github.com/onmetal/onmetal-api-net/api/v1alpha1"
	apinetletv1alpha1 "github.com/onmetal/onmetal-api-net/apinetlet/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/apinetlet/provider"
	networkingv1alpha1 "github.com/onmetal/onmetal-api/api/networking/v1alpha1"
	"github.com/onmetal/onmetal-api/utils/generic"
	"github.com/onmetal/onmetal-api/utils/predicates"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	natGatewayFinalizer = "apinet.api.onmetal.de/natgateway"
)

type NATGatewayReconciler struct {
	client.Client
	APINetClient    client.Client
	APINetNamespace string

	WatchFilterValue string
}

func (r *NATGatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	natGateway := &networkingv1alpha1.NATGateway{}
	if err := r.Get(ctx, req.NamespacedName, natGateway); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("error getting nat gateway: %w", err)
		}
		return r.deleteGone(ctx, log, req.NamespacedName)
	}
	return r.reconcileExists(ctx, log, natGateway)
}

func (r *NATGatewayReconciler) deleteGone(ctx context.Context, log logr.Logger, key client.ObjectKey) (ctrl.Result, error) {
	log.V(1).Info("Delete gone")

	log.V(1).Info("Deleting any APINet NAT gateway by key")
	if err := r.APINetClient.DeleteAllOf(ctx, &apinetv1alpha1.NATGateway{},
		client.InNamespace(r.APINetNamespace),
		client.MatchingLabels{
			apinetletv1alpha1.NATGatewayNamespaceLabel: key.Namespace,
			apinetletv1alpha1.NATGatewayNameLabel:      key.Name,
		},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("error deleting apinet nat gateways by key: %w", err)
	}

	log.V(1).Info("Checking if any APINet NAT gateway is still present")
	apiNetNATGatewayList := &apinetv1alpha1.NATGatewayList{}
	if err := r.APINetClient.List(ctx, apiNetNATGatewayList,
		client.InNamespace(r.APINetNamespace),
		client.MatchingLabels{
			apinetletv1alpha1.NATGatewayNamespaceLabel: key.Namespace,
			apinetletv1alpha1.NATGatewayNameLabel:      key.Name,
		},
		client.Limit(1),
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("error listing apinet nat gateways by key: %w", err)
	}

	if len(apiNetNATGatewayList.Items) > 0 {
		log.V(1).Info("Some APINet NAT Gateways are still present, requeueing")
		return ctrl.Result{Requeue: true}, nil
	}
	log.V(1).Info("All APINet NAT Gateways are gone")

	log.V(1).Info("Deleted gone")
	return ctrl.Result{}, nil
}

func (r *NATGatewayReconciler) reconcileExists(ctx context.Context, log logr.Logger, natGateway *networkingv1alpha1.NATGateway) (ctrl.Result, error) {
	if !natGateway.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, natGateway)
	}
	return r.reconcile(ctx, log, natGateway)
}

func (r *NATGatewayReconciler) delete(ctx context.Context, log logr.Logger, natGateway *networkingv1alpha1.NATGateway) (ctrl.Result, error) {
	log.V(1).Info("Delete")

	if !controllerutil.ContainsFinalizer(natGateway, natGatewayFinalizer) {
		log.V(1).Info("No finalizer present, nothing to do")
		return ctrl.Result{}, nil
	}
	log.V(1).Info("Finalizer present, running cleanup")

	log.V(1).Info("Deleting APINet NAT gateway")
	apiNetNATGateway := &apinetv1alpha1.NATGateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.APINetNamespace,
			Name:      string(natGateway.UID),
		},
	}

	err := r.APINetClient.Delete(ctx, apiNetNATGateway)
	if client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, fmt.Errorf("error deleting apinet nat gateway: %w", err)
	}
	if err == nil {
		log.V(1).Info("Issued APINet NAT gateway deletion")
		return ctrl.Result{Requeue: true}, nil
	}

	log.V(1).Info("APINet NAT gateway is gone, removing finalizer")
	if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, natGateway, natGatewayFinalizer); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing nat gateway finalizer: %w", err)
	}
	log.V(1).Info("Removed finalizer")

	log.V(1).Info("Deleted")
	return ctrl.Result{}, nil
}

func (r *NATGatewayReconciler) reconcile(ctx context.Context, log logr.Logger, natGateway *networkingv1alpha1.NATGateway) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	log.V(1).Info("Ensuring finalizer")
	modified, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, natGateway, natGatewayFinalizer)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error ensuring finalizer: %w", err)
	}
	if modified {
		log.V(1).Info("Added finalizer, requeueing")
		return ctrl.Result{Requeue: true}, nil
	}
	log.V(1).Info("Finalizer is present")

	network := &networkingv1alpha1.Network{}
	networkKey := client.ObjectKey{Namespace: natGateway.Namespace, Name: natGateway.Spec.NetworkRef.Name}
	log.V(1).Info("Getting network for NAT Gateway", "NetworkName", networkKey.Name)
	if err := r.Get(ctx, networkKey, network); err != nil {
		return ctrl.Result{}, fmt.Errorf("error getting network %s for nat gateway: %w", networkKey.Name, err)
	}

	networkProviderID := network.Spec.ProviderID
	if networkProviderID == "" {
		log.V(1).Info("Network provider ID is unset")
		return ctrl.Result{}, nil
	}

	networkName, err := provider.ParseNetworkID(networkProviderID)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error parsing network %s id: %w", networkProviderID, err)
	}

	apiNetNATGateway := &apinetv1alpha1.NATGateway{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apinetv1alpha1.GroupVersion.String(),
			Kind:       "NATGateway",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.APINetNamespace,
			Name:      string(natGateway.UID),
			Labels: map[string]string{
				apinetletv1alpha1.NATGatewayNamespaceLabel: natGateway.Namespace,
				apinetletv1alpha1.NATGatewayNameLabel:      natGateway.Name,
				apinetletv1alpha1.NATGatewayUIDLabel:       string(natGateway.UID),
			},
		},
		Spec: apinetv1alpha1.NATGatewaySpec{
			IPFamily:                 natGateway.Spec.IPFamily,
			NetworkRef:               corev1.LocalObjectReference{Name: networkName},
			PortsPerNetworkInterface: generic.Deref(natGateway.Spec.PortsPerNetworkInterface, networkingv1alpha1.DefaultPortsPerNetworkInterface),
			PublicIPRefs:             nil,
		},
	}
	if err := r.APINetClient.Patch(ctx, apiNetNATGateway, client.Apply, client.ForceOwnership, fieldOwner); err != nil {
		return ctrl.Result{}, fmt.Errorf("error applying apinet nat gateway: %w", err)
	}

	log.V(1).Info("Reconciled")
	return ctrl.Result{}, nil
}

func (r *NATGatewayReconciler) SetupWithManager(mgr ctrl.Manager, apiNetCache cache.Cache) error {
	log := ctrl.Log.WithName("natgateway").WithName("setup")

	return ctrl.NewControllerManagedBy(mgr).
		For(
			&networkingv1alpha1.NATGateway{},
			builder.WithPredicates(
				predicates.ResourceHasFilterLabel(log, r.WatchFilterValue),
				predicates.ResourceIsNotExternallyManaged(log),
			),
		).
		WatchesRawSource(
			source.Kind(apiNetCache, &apinetv1alpha1.NATGateway{}),
			enqueueByAPINetObjectName(r.Client,
				&networkingv1alpha1.NATGatewayList{},
				withFilters(notExternallyManagedFilter()),
				matchingLabelSelector{Selector: watchLabelSelector(r.WatchFilterValue)},
			),
		).
		Complete(r)
}
