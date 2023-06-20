package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/lru"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

type PublicIPGarbageCollectorReconciler struct {
	client.Client
	APIReader client.Reader

	absenceCache *lru.Cache
}

func (r *PublicIPGarbageCollectorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	publicIP := &v1alpha1.PublicIP{}
	if err := r.Get(ctx, req.NamespacedName, publicIP); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, publicIP)
}

func (r *PublicIPGarbageCollectorReconciler) reconcileExists(ctx context.Context, log logr.Logger, publicIP *v1alpha1.PublicIP) (ctrl.Result, error) {
	if !publicIP.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, publicIP)
	}
	return r.reconcile(ctx, log, publicIP)
}

func (r *PublicIPGarbageCollectorReconciler) delete(ctx context.Context, log logr.Logger, publicIP *v1alpha1.PublicIP) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

func (r *PublicIPGarbageCollectorReconciler) reconcile(ctx context.Context, log logr.Logger, publicIP *v1alpha1.PublicIP) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	if publicIP.Spec.ClaimerRef == nil {
		log.V(1).Info("Public IP is not claimed, nothing to do")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Checking whether public IP claimer exists")
	ok, err := r.publicIPClaimerExists(ctx, publicIP)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error checking whether public IP claimer exists: %w", err)
	}
	if ok {
		log.V(1).Info("Public IP claimer is still present")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Public IP claimer does not exist, releasing public IP")
	if err := r.releasePublicIP(ctx, publicIP); err != nil {
		switch {
		case apierrors.IsNotFound(err):
			log.V(1).Info("Public IP is already gone")
		case apierrors.IsConflict(err):
			log.V(1).Info("Public IP was updated, requeueing")
			return ctrl.Result{Requeue: true}, nil
		default:
			return ctrl.Result{}, fmt.Errorf("error releasing public IP: %w", err)
		}
	}

	log.V(1).Info("Reconciled")
	return ctrl.Result{}, nil
}

func (r *PublicIPGarbageCollectorReconciler) publicIPClaimerExists(ctx context.Context, publicIP *v1alpha1.PublicIP) (bool, error) {
	claimerRef := publicIP.Spec.ClaimerRef
	if _, ok := r.absenceCache.Get(claimerRef.UID); ok {
		return false, nil
	}

	var (
		claimer    client.Object
		claimerKey = client.ObjectKey{Namespace: publicIP.Namespace, Name: claimerRef.Name}
	)
	switch claimerRef.Kind {
	case networkInterfaceKind, natGatewayKind, loadBalancerKind:
		claimer = &metav1.PartialObjectMetadata{
			TypeMeta: metav1.TypeMeta{
				APIVersion: v1alpha1.GroupVersion.String(),
				Kind:       claimerRef.Kind,
			},
		}
	default:
		return false, fmt.Errorf("invalid claimer kind %q", claimerRef.Kind)
	}
	if err := r.APIReader.Get(ctx, claimerKey, claimer); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("error getting claiming %s %s: %w", claimerRef.Kind, claimerRef.Name, err)
		}

		r.absenceCache.Add(claimerRef.UID, nil)
		return false, nil
	}
	return true, nil
}

func (r *PublicIPGarbageCollectorReconciler) releasePublicIP(ctx context.Context, publicIP *v1alpha1.PublicIP) error {
	base := publicIP.DeepCopy()
	publicIP.Spec.ClaimerRef = nil
	if err := r.Patch(ctx, publicIP, client.MergeFromWithOptions(base, &client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("error releasing public IP: %w", err)
	}
	return nil
}

func (r *PublicIPGarbageCollectorReconciler) enqueueByNATGateway() handler.EventHandler {
	mapAndEnqueue := func(natGateway *v1alpha1.NATGateway, queue workqueue.RateLimitingInterface) {
		for _, publicIPRef := range natGateway.Spec.PublicIPRefs {
			queue.Add(ctrl.Request{NamespacedName: client.ObjectKey{
				Namespace: natGateway.Namespace,
				Name:      publicIPRef.Name,
			}})
		}
	}

	return &handler.Funcs{
		DeleteFunc: func(ctx context.Context, event event.DeleteEvent, queue workqueue.RateLimitingInterface) {
			natGateway := event.Object.(*v1alpha1.NATGateway)
			mapAndEnqueue(natGateway, queue)
		},
		GenericFunc: func(ctx context.Context, event event.GenericEvent, queue workqueue.RateLimitingInterface) {
			natGateway := event.Object.(*v1alpha1.NATGateway)
			if !natGateway.DeletionTimestamp.IsZero() {
				mapAndEnqueue(natGateway, queue)
			}
		},
	}
}

func (r *PublicIPGarbageCollectorReconciler) enqueueByLoadBalancer() handler.EventHandler {
	mapAndEnqueue := func(loadBalancer *v1alpha1.LoadBalancer, queue workqueue.RateLimitingInterface) {
		for _, publicIPRef := range loadBalancer.Spec.PublicIPRefs {
			queue.Add(ctrl.Request{NamespacedName: client.ObjectKey{
				Namespace: loadBalancer.Namespace,
				Name:      publicIPRef.Name,
			}})
		}
	}

	return &handler.Funcs{
		DeleteFunc: func(ctx context.Context, event event.DeleteEvent, queue workqueue.RateLimitingInterface) {
			loadBalancer := event.Object.(*v1alpha1.LoadBalancer)
			mapAndEnqueue(loadBalancer, queue)
		},
		GenericFunc: func(ctx context.Context, event event.GenericEvent, queue workqueue.RateLimitingInterface) {
			loadBalancer := event.Object.(*v1alpha1.LoadBalancer)
			if !loadBalancer.DeletionTimestamp.IsZero() {
				mapAndEnqueue(loadBalancer, queue)
			}
		},
	}
}

func (r *PublicIPGarbageCollectorReconciler) enqueueByNetworkInterface() handler.EventHandler {
	mapAndEnqueue := func(nic *v1alpha1.NetworkInterface, queue workqueue.RateLimitingInterface) {
		for _, publicIPRef := range nic.Spec.PublicIPRefs {
			queue.Add(ctrl.Request{NamespacedName: client.ObjectKey{
				Namespace: nic.Namespace,
				Name:      publicIPRef.Name,
			}})
		}
	}

	return &handler.Funcs{
		DeleteFunc: func(ctx context.Context, event event.DeleteEvent, queue workqueue.RateLimitingInterface) {
			nic := event.Object.(*v1alpha1.NetworkInterface)
			mapAndEnqueue(nic, queue)
		},
		GenericFunc: func(ctx context.Context, event event.GenericEvent, queue workqueue.RateLimitingInterface) {
			nic := event.Object.(*v1alpha1.NetworkInterface)
			if !nic.DeletionTimestamp.IsZero() {
				mapAndEnqueue(nic, queue)
			}
		},
	}
}

func (r *PublicIPGarbageCollectorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("publicipgc").
		For(
			&v1alpha1.PublicIP{},
			builder.WithPredicates(publicIPClaimedPredicate),
		).
		Watches(
			&v1alpha1.NATGateway{},
			r.enqueueByNATGateway(),
		).
		Watches(
			&v1alpha1.NetworkInterface{},
			r.enqueueByNetworkInterface(),
		).
		Watches(
			&v1alpha1.LoadBalancer{},
			r.enqueueByLoadBalancer(),
		).
		Complete(r)
}
