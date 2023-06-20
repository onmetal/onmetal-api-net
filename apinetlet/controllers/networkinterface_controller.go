package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	apinetv1alpha1 "github.com/onmetal/onmetal-api-net/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/apinetlet/provider"
	networkingv1alpha1 "github.com/onmetal/onmetal-api/api/networking/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	networkInterfaceFinalizer = "apinet.api.onmetal.de/networkinterface"
)

type NetworkInterfaceReconciler struct {
	client.Client
	APINetClient    client.Client
	APINetNamespace string
}

func (r *NetworkInterfaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	nic := &networkingv1alpha1.NetworkInterface{}
	if err := r.Get(ctx, req.NamespacedName, nic); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("error getting network interface: %w", err)
		}
		return r.deleteGone(ctx, log, req.NamespacedName)
	}
	return r.reconcileExists(ctx, log, nic)
}

func (r *NetworkInterfaceReconciler) deleteGone(ctx context.Context, log logr.Logger, key client.ObjectKey) (ctrl.Result, error) {
	log.V(1).Info("Delete gone")

	log.V(1).Info("Deleting APINet network interfaces by key")
	if err := r.deleteManagedAPINetNetworkInterfaceByKey(ctx, key); err != nil {
		return ctrl.Result{}, fmt.Errorf("error deleting managed apinet network interfaces by key: %w", err)
	}

	log.V(1).Info("Checking if any APINet network interface exists by key")
	anyExists, err := r.anyAPINetNetworkInterfaceExistsByKey(ctx, key)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error checking whether any apinet network interface exists by key: %w", err)
	}
	if anyExists {
		log.V(1).Info("APINet network interfaces are still present by key, requeueing")
		return ctrl.Result{Requeue: true}, nil
	}
	log.V(1).Info("No APINet network interface present anymore")

	log.V(1).Info("Deleted gone")
	return ctrl.Result{}, nil
}

func (r *NetworkInterfaceReconciler) reconcileExists(
	ctx context.Context,
	log logr.Logger,
	nic *networkingv1alpha1.NetworkInterface,
) (ctrl.Result, error) {
	if !nic.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, nic)
	}
	return r.reconcile(ctx, log, nic)
}

func (r *NetworkInterfaceReconciler) delete(
	ctx context.Context,
	log logr.Logger,
	nic *networkingv1alpha1.NetworkInterface,
) (ctrl.Result, error) {
	log.V(1).Info("Delete")
	log.V(1).Info("Deleted")
	return ctrl.Result{}, nil
}

func (r *NetworkInterfaceReconciler) reconcile(
	ctx context.Context,
	log logr.Logger,
	nic *networkingv1alpha1.NetworkInterface,
) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")
	log.V(1).Info("Reconciled")
	return ctrl.Result{}, nil
}

func (r *NetworkInterfaceReconciler) SetupWithManager(mgr ctrl.Manager, apiNetCache cache.Cache) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("networkinterface").
		For(&networkingv1alpha1.NetworkInterface{}).
		WatchesRawSource(
			source.Kind(apiNetCache, &apinetv1alpha1.NetworkInterface{}),
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
				apiNetNic := obj.(*apinetv1alpha1.NetworkInterface)
				log := ctrl.LoggerFrom(ctx)

				if apiNetNic.Namespace != r.APINetNamespace {
					return nil
				}

				nicList := &networkingv1alpha1.NetworkInterfaceList{}
				if err := r.List(ctx, nicList); err != nil {
					log.Error(err, "Error listing network interfaces")
					return nil
				}

				expectedProviderID := provider.GetNetworkInterfaceID(apiNetNic.Name, apiNetNic.UID)

				var reqs []ctrl.Request
				for _, nic := range nicList.Items {
					if nic.Status.ProviderID == expectedProviderID {
						reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&nic)})
					}
				}
				return reqs
			}),
		).
		Complete(r)
}
