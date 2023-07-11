package controllers

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type PartitionReconciler struct {
	client.Client
	PartitionName string

	initOnce     sync.Once
	initErr      error
	initDoneOnce sync.Once
	initDone     chan struct{}
}

func (r *PartitionReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	partition := &v1alpha1.Partition{}
	partitionKey := client.ObjectKey{Name: r.PartitionName}
	if err := r.Get(ctx, partitionKey, partition); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, partition)
}

func (r *PartitionReconciler) reconcileExists(ctx context.Context, log logr.Logger, partition *v1alpha1.Partition) (ctrl.Result, error) {
	if !partition.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, partition)
	}
	return r.reconcile(ctx, log, partition)
}

func (r *PartitionReconciler) delete(ctx context.Context, log logr.Logger, partition *v1alpha1.Partition) (ctrl.Result, error) {
	log.V(1).Info("Delete")
	log.V(1).Info("Deleted")
	return ctrl.Result{}, nil
}

func (r *PartitionReconciler) reconcile(ctx context.Context, log logr.Logger, partition *v1alpha1.Partition) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")
	log.V(1).Info("Reconciled")
	return ctrl.Result{}, nil
}

func (r *PartitionReconciler) init(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx).WithName("partition").WithValues("PartitionName", r.PartitionName)

	log.V(1).Info("Applying partition")
	partition := &v1alpha1.Partition{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       "Partition",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: r.PartitionName,
		},
	}
	if err := r.Patch(ctx, partition, client.Apply, PartitionFieldOwner(r.PartitionName), client.ForceOwnership); err != nil {
		return fmt.Errorf("error applying partition: %w", err)
	}

	log.V(1).Info("Applied partition")
	return nil
}

func (r *PartitionReconciler) initDoneChan() {
	r.initDoneOnce.Do(func() {
		r.initDone = make(chan struct{})
	})
}

func (r *PartitionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		r.initDoneChan()
		r.initOnce.Do(func() {
			defer close(r.initDone)
			r.initErr = r.init(ctx)
		})
		if r.initErr != nil {
			return fmt.Errorf("error initializing: %w", r.initErr)
		}

		return ctrl.NewControllerManagedBy(mgr).
			For(&v1alpha1.Partition{}).
			Complete(r)
	}))
}

func (r *PartitionReconciler) Wait(ctx context.Context) error {
	r.initDoneChan()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.initDone:
		return r.initErr
	}
}
