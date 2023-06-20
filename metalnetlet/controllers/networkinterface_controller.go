package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	metalnetv1alpha1 "github.com/onmetal/metalnet/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
)

type NetworkInterfaceReconciler struct {
	client.Client
	MetalnetCluster   cluster.Cluster
	MetalnetNamespace string
}

func (r *NetworkInterfaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	nic := &v1alpha1.NetworkInterface{}
	if err := r.Get(ctx, req.NamespacedName, nic); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
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
	return ctrl.Result{}, nil
}

func (r *NetworkInterfaceReconciler) reconcile(ctx context.Context, log logr.Logger, nic *v1alpha1.NetworkInterface) (ctrl.Result, error) {
	metalnetNic := &metalnetv1alpha1.NetworkInterface{}
	metalnetNicKey := client.ObjectKey{Namespace: r.MetalnetNamespace, Name: nic.Spec.Name}
	if err := r.MetalnetCluster.GetClient().Get(ctx, metalnetNicKey, metalnetNic); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("error getting metalnet nic %s: %w", nic.Spec.Name, err)
		}

		log.V(1).Info("Metalnet network interface not found")
		return ctrl.Result{}, err
	}

	if nic.Spec.UID != metalnetNic.UID {
		log.V(1).Info("Metalnet UID does not match", "Expected", nic.Spec.UID, "Actual", metalnetNic.UID)
		return ctrl.Result{}, nil
	}

	metalnetNic.Spec.Prefixes = ipPrefixesToMetalnetPrefixes(nic.Spec.Prefixes)
	return ctrl.Result{}, nil
}
