package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/metalnetlet/scheduler"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	outOfCapacity = "OutOfCapacity"
)

type LoadBalancerInstanceSchedulerReconciler struct {
	client.Client
	record.EventRecorder
	MetalnetClient client.Client
	Cache          *scheduler.Cache

	PartitionName string

	snapshot *scheduler.Snapshot
}

func (r *LoadBalancerInstanceSchedulerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	instance := &v1alpha1.LoadBalancerInstance{}
	if err := r.Get(ctx, req.NamespacedName, instance); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if r.skipSchedule(log, instance) {
		log.V(1).Info("Skipping scheduling for instance")
		return ctrl.Result{}, nil
	}

	return r.reconcileExists(ctx, log, instance)
}

func (r *LoadBalancerInstanceSchedulerReconciler) skipSchedule(log logr.Logger, instance *v1alpha1.LoadBalancerInstance) bool {
	if !instance.DeletionTimestamp.IsZero() {
		return true
	}

	isAssumed, err := r.Cache.IsAssumedInstance(instance)
	if err != nil {
		log.Error(err, "Error checking whether instance has been assumed")
		return false
	}
	return isAssumed
}

func (r *LoadBalancerInstanceSchedulerReconciler) updateSnapshot() {
	if r.snapshot == nil {
		r.snapshot = r.Cache.Snapshot()
	} else {
		r.snapshot.Update()
	}
}

func (r *LoadBalancerInstanceSchedulerReconciler) reconcileExists(
	ctx context.Context,
	log logr.Logger,
	instance *v1alpha1.LoadBalancerInstance,
) (ctrl.Result, error) {
	r.updateSnapshot()

	nodes := r.snapshot.ListNodes()
	if len(nodes) == 0 {
		r.EventRecorder.Event(instance, corev1.EventTypeNormal, outOfCapacity, "No nodes available to schedule load balancer on")
		return ctrl.Result{}, nil
	}

	minUsedNode := nodes[0]
	for _, node := range nodes[1:] {
		if node.NumInstances() < minUsedNode.NumInstances() {
			minUsedNode = node
		}
	}
	log.V(1).Info("Determined node to schedule on", "NodeName", minUsedNode.Node().Name, "Usage", minUsedNode.NumInstances())

	log.V(1).Info("Assuming instance to be on node")
	if err := r.assume(instance, minUsedNode.Node().Name); err != nil {
		return ctrl.Result{}, err
	}

	log.V(1).Info("Running binding asynchronously")
	go func() {
		if err := r.bindingCycle(ctx, log, instance); err != nil {
			if err := r.Cache.ForgetInstance(instance); err != nil {
				log.Error(err, "Error forgetting instance")
			}
		}
	}()
	return ctrl.Result{}, nil
}

func (r *LoadBalancerInstanceSchedulerReconciler) assume(assumed *v1alpha1.LoadBalancerInstance, nodeName string) error {
	assumed.Spec.NodeRef = &corev1.LocalObjectReference{Name: nodeName}
	if err := r.Cache.AssumeInstance(assumed.DeepCopy()); err != nil {
		return err
	}
	return nil
}

func (r *LoadBalancerInstanceSchedulerReconciler) bindingCycle(ctx context.Context, log logr.Logger, assumedInstance *v1alpha1.LoadBalancerInstance) error {
	if err := r.bind(ctx, log, assumedInstance); err != nil {
		return fmt.Errorf("error binding: %w", err)
	}
	return nil
}

func (r *LoadBalancerInstanceSchedulerReconciler) bind(ctx context.Context, log logr.Logger, assumed *v1alpha1.LoadBalancerInstance) error {
	defer func() {
		if err := r.Cache.FinishBinding(assumed); err != nil {
			log.Error(err, "Error finishing cache binding")
		}
	}()

	nonAssumed := assumed.DeepCopy()
	nonAssumed.Spec.NodeRef = nil

	if err := r.Patch(ctx, assumed, client.MergeFrom(nonAssumed)); err != nil {
		return fmt.Errorf("error patching instance: %w", err)
	}
	return nil
}

func (r *LoadBalancerInstanceSchedulerReconciler) instanceOnPartitionPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		instance := obj.(*v1alpha1.LoadBalancerInstance)
		return instance.Spec.PartitionRef.Name == r.PartitionName
	})
}

func (r *LoadBalancerInstanceSchedulerReconciler) instanceNotAssignedPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		instance := obj.(*v1alpha1.LoadBalancerInstance)
		return instance.Spec.NodeRef == nil
	})
}

func (r *LoadBalancerInstanceSchedulerReconciler) nodeFromPartitionPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		node := obj.(*v1alpha1.Node)
		return node.Spec.PartitionRef.Name == r.PartitionName
	})
}

func (r *LoadBalancerInstanceSchedulerReconciler) handleNode() handler.EventHandler {
	return handler.Funcs{
		CreateFunc: func(ctx context.Context, evt event.CreateEvent, queue workqueue.RateLimitingInterface) {
			node := evt.Object.(*v1alpha1.Node)
			log := ctrl.LoggerFrom(ctx)

			r.Cache.AddContainer(node)

			// TODO: Setup an index for listing unscheduled load balancer instances for the target partition.
			instanceList := &v1alpha1.LoadBalancerInstanceList{}
			if err := r.List(ctx, instanceList); err != nil {
				log.Error(err, "Error listing load balancer instances")
				return
			}

			for _, instance := range instanceList.Items {
				if !instance.DeletionTimestamp.IsZero() {
					continue
				}
				if instance.Spec.NodeRef != nil {
					continue
				}
				if instance.Spec.PartitionRef.Name != r.PartitionName {
					continue
				}

				queue.Add(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&instance)})
			}
		},
		UpdateFunc: func(ctx context.Context, evt event.UpdateEvent, queue workqueue.RateLimitingInterface) {
			oldNode := evt.ObjectOld.(*v1alpha1.Node)
			newNode := evt.ObjectNew.(*v1alpha1.Node)
			r.Cache.UpdateContainer(oldNode, newNode)
		},
		DeleteFunc: func(ctx context.Context, evt event.DeleteEvent, queue workqueue.RateLimitingInterface) {
			node := evt.Object.(*v1alpha1.Node)
			log := ctrl.LoggerFrom(ctx)

			if err := r.Cache.RemoveContainer(node); err != nil {
				log.Error(err, "Error removing container from cache")
			}
		},
	}
}

func (r *LoadBalancerInstanceSchedulerReconciler) isInstanceAssigned() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		instance := obj.(*v1alpha1.LoadBalancerInstance)
		return instance.Spec.NodeRef != nil
	})
}

func (r *LoadBalancerInstanceSchedulerReconciler) handleAssignedInstances() handler.EventHandler {
	return handler.Funcs{
		CreateFunc: func(ctx context.Context, evt event.CreateEvent, queue workqueue.RateLimitingInterface) {
			instance := evt.Object.(*v1alpha1.LoadBalancerInstance)
			log := ctrl.LoggerFrom(ctx)

			if err := r.Cache.AddInstance(instance); err != nil {
				log.Error(err, "Error adding instance to cache")
			}
		},
		UpdateFunc: func(ctx context.Context, evt event.UpdateEvent, queue workqueue.RateLimitingInterface) {
			oldInstance := evt.ObjectOld.(*v1alpha1.LoadBalancerInstance)
			newInstance := evt.ObjectNew.(*v1alpha1.LoadBalancerInstance)
			log := ctrl.LoggerFrom(ctx)

			// Only add or update are possible - node updates are not allowed by admission.
			oldInstanceAssigned := oldInstance.Spec.NodeRef != nil
			newInstanceAssigned := newInstance.Spec.NodeRef != nil
			if oldInstanceAssigned && !newInstanceAssigned {
				if err := r.Cache.AddInstance(newInstance); err != nil {
					log.Error(err, "Error adding instance to cache")
				}
			} else {
				if err := r.Cache.UpdateInstance(oldInstance, newInstance); err != nil {
					log.Error(err, "Error updating instance in cache")
				}
			}
		},
		DeleteFunc: func(ctx context.Context, evt event.DeleteEvent, queue workqueue.RateLimitingInterface) {
			instance := evt.Object.(*v1alpha1.LoadBalancerInstance)
			log := ctrl.LoggerFrom(ctx)

			if err := r.Cache.RemoveInstance(instance); err != nil {
				log.Error(err, "Error adding instance to cache")
			}
		},
	}
}

func (r *LoadBalancerInstanceSchedulerReconciler) handleUnassignedInstance() handler.EventHandler {
	return handler.Funcs{
		CreateFunc: func(ctx context.Context, evt event.CreateEvent, queue workqueue.RateLimitingInterface) {
			instance := evt.Object.(*v1alpha1.LoadBalancerInstance)
			queue.Add(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(instance)})
		},
		UpdateFunc: func(ctx context.Context, evt event.UpdateEvent, queue workqueue.RateLimitingInterface) {
			oldInstance := evt.ObjectOld.(*v1alpha1.LoadBalancerInstance)
			newInstance := evt.ObjectNew.(*v1alpha1.LoadBalancerInstance)
			log := ctrl.LoggerFrom(ctx)

			if oldInstance.ResourceVersion == newInstance.ResourceVersion {
				return
			}

			isAssumed, err := r.Cache.IsAssumedInstance(newInstance)
			if err != nil {
				log.Error(err, "Error checking whether instance is assumed", "Instance", klog.KObj(newInstance))
			}
			if isAssumed {
				return
			}

			queue.Add(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(newInstance)})
		},
	}
}

func (r *LoadBalancerInstanceSchedulerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{
			// Only a single concurrent reconcile since it is serialized on the scheduling algorithm's node fitting.
			MaxConcurrentReconciles: 1,
		}).
		Named("loadbalancerinstance-scheduler").
		Watches(
			&v1alpha1.LoadBalancerInstance{},
			r.handleUnassignedInstance(),
			builder.WithPredicates(
				r.instanceOnPartitionPredicate(),
				r.instanceNotAssignedPredicate(),
			),
		).
		Watches(
			&v1alpha1.LoadBalancerInstance{},
			r.handleAssignedInstances(),
			builder.WithPredicates(
				r.instanceOnPartitionPredicate(),
				r.isInstanceAssigned(),
			),
		).
		Watches(
			&v1alpha1.Node{},
			r.handleNode(),
			builder.WithPredicates(r.nodeFromPartitionPredicate()),
		).
		Complete(r)
}
