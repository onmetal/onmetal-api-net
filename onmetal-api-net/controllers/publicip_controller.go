// Copyright 2022 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"context"
	"fmt"
	"net/netip"
	"sync"

	"github.com/go-logr/logr"
	"github.com/onmetal/controller-utils/clientutils"
	onmetalapinetv1alpha1 "github.com/onmetal/onmetal-api-net/api/v1alpha1"
	"go4.org/netipx"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	publicIPFinalizer = "apinet.api.onmetal.de/publicip"
)

type publicIPAllocation struct {
	UID types.UID
	IP  netip.Addr
}

type publicIPAllocations struct {
	availableIPs     *netipx.IPSet
	allocationsByKey map[client.ObjectKey]publicIPAllocation
}

func newPublicIPAllocations(availableIPs *netipx.IPSet) *publicIPAllocations {
	return &publicIPAllocations{
		availableIPs:     availableIPs,
		allocationsByKey: make(map[client.ObjectKey]publicIPAllocation),
	}
}

func (p *publicIPAllocations) get(key client.ObjectKey) *publicIPAllocation {
	allocation, ok := p.allocationsByKey[key]
	if ok {
		return &allocation
	}
	return nil
}

func (p *publicIPAllocations) delete(key client.ObjectKey) *publicIPAllocation {
	deleted, ok := p.allocationsByKey[key]
	if !ok {
		return nil
	}

	var sb netipx.IPSetBuilder
	sb.AddSet(p.availableIPs)
	sb.Add(deleted.IP)

	p.availableIPs, _ = sb.IPSet()
	delete(p.allocationsByKey, key)

	return &deleted
}

type publicIPAllocationRequest struct {
	ipFamily corev1.IPFamily
	ip       netip.Addr
}

func (p *publicIPAllocations) canFit(req publicIPAllocationRequest) bool {
	if req.ip.IsValid() {
		return p.availableIPs.Contains(req.ip)
	}

	_, _, ok := p.availableIPs.RemoveFreePrefix(IPFamilyBitLen(req.ipFamily))
	return ok
}

func (p *publicIPAllocations) allocate(key client.ObjectKey, uid types.UID, req publicIPAllocationRequest) (netip.Addr, error) {
	if _, ok := p.allocationsByKey[key]; ok {
		return netip.Addr{}, fmt.Errorf("allocation for %s already exists", key)
	}

	if req.ip.IsValid() {
		ip := req.ip
		if !p.availableIPs.Contains(ip) {
			return netip.Addr{}, fmt.Errorf("ip %s is not available for allocation", ip)
		}

		var sb netipx.IPSetBuilder
		sb.AddSet(p.availableIPs)
		sb.Remove(ip)
		set, _ := sb.IPSet()

		p.availableIPs = set
		p.allocationsByKey[key] = publicIPAllocation{
			UID: uid,
			IP:  ip,
		}
		return ip, nil
	} else {
		prefix, set, ok := p.availableIPs.RemoveFreePrefix(IPFamilyBitLen(req.ipFamily))
		if !ok {
			return netip.Addr{}, fmt.Errorf("no free prefix available for ip family %s", req.ipFamily)
		}

		ip := prefix.Addr()

		p.availableIPs = set
		p.allocationsByKey[key] = publicIPAllocation{
			UID: uid,
			IP:  ip,
		}
		return ip, nil
	}
}

type PublicIPReconciler struct {
	mu sync.RWMutex

	record.EventRecorder
	client.Client
	APIReader           client.Reader
	InitialAvailableIPs *netipx.IPSet

	allocations *publicIPAllocations
	released    chan struct{}
}

//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=apinet.api.onmetal.de,resources=publicips,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=apinet.api.onmetal.de,resources=publicips/finalizers,verbs=update
//+kubebuilder:rbac:groups=apinet.api.onmetal.de,resources=publicips/status,verbs=get;update;patch

func (r *PublicIPReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	publicIP := &onmetalapinetv1alpha1.PublicIP{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, publicIP); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("error getting public ip %s: %w", req.NamespacedName, err)
		}

		r.release(ctx, req.NamespacedName)
		return ctrl.Result{}, nil
	}

	return r.reconcileExists(ctx, log, publicIP)
}

func (r *PublicIPReconciler) reconcileExists(ctx context.Context, log logr.Logger, publicIP *onmetalapinetv1alpha1.PublicIP) (ctrl.Result, error) {
	if !publicIP.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, publicIP)
	}
	return r.reconcile(ctx, log, publicIP)
}

func (r *PublicIPReconciler) delete(ctx context.Context, log logr.Logger, publicIP *onmetalapinetv1alpha1.PublicIP) (ctrl.Result, error) {
	log.V(1).Info("Delete")

	log.V(1).Info("Releasing any associated ips")
	r.release(ctx, client.ObjectKeyFromObject(publicIP))

	log.V(1).Info("Ensuring finalizer is not present anymore")
	if _, err := clientutils.PatchEnsureNoFinalizer(ctx, r.Client, publicIP, publicIPFinalizer); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}

	log.V(1).Info("Deleted")
	return ctrl.Result{}, nil
}

func (r *PublicIPReconciler) emitReleased(ctx context.Context) {
	select {
	case <-ctx.Done():
	case r.released <- struct{}{}:
	}
}

func (r *PublicIPReconciler) release(ctx context.Context, key client.ObjectKey) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if deleted := r.allocations.delete(key); deleted != nil {
		r.emitReleased(ctx)
	}
}

func (r *PublicIPReconciler) allocate(ctx context.Context, log logr.Logger, publicIP *onmetalapinetv1alpha1.PublicIP) (netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := client.ObjectKeyFromObject(publicIP)
	if allocation := r.allocations.get(key); allocation != nil {
		if allocation.UID == publicIP.UID {
			log.V(1).Info("Retrieved existing allocation", "UID", allocation.UID, "IP", allocation.IP)
			return allocation.IP, nil
		}

		log.V(1).Info("Current allocation is outdated, releasing it", "UID", allocation.UID, "IP", allocation.IP)
		r.allocations.delete(key)
		r.emitReleased(ctx)
	}

	req := publicIPAllocationRequestFromPublicIP(publicIP)
	log.V(1).Info("Requesting new allocation", "Request", req)
	return r.allocations.allocate(key, publicIP.UID, req)
}

func publicIPAllocationRequestFromPublicIP(publicIP *onmetalapinetv1alpha1.PublicIP) publicIPAllocationRequest {
	var ip netip.Addr
	if reqIP := publicIP.Spec.IP; reqIP != nil {
		ip = reqIP.Addr
	}
	req := publicIPAllocationRequest{
		ipFamily: publicIP.Spec.IPFamily,
		ip:       ip,
	}
	return req
}

func (r *PublicIPReconciler) reconcile(ctx context.Context, log logr.Logger, publicIP *onmetalapinetv1alpha1.PublicIP) (ctrl.Result, error) {
	log.V(1).Info("Reconcile")

	log.V(1).Info("Ensuring finalizer")
	modified, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, publicIP, publicIPFinalizer)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error ensuring finalizer: %w", err)
	}
	if modified {
		log.V(1).Info("Added finalizer, requeueing")
		return ctrl.Result{Requeue: true}, nil
	}
	log.V(1).Info("Finalizer is present")

	if _, ok := publicIP.Annotations[onmetalapinetv1alpha1.ReconcileRequestAnnotation]; ok {
		log.V(1).Info("Removing reconcile annotation")
		if err := PatchRemoveReconcileAnnotation(ctx, r.Client, publicIP); err != nil {
			return ctrl.Result{}, fmt.Errorf("error removing reconcile annotation: %w", err)
		}
		log.V(1).Info("Removed reconcile annotation, requeueing")
		return ctrl.Result{Requeue: true}, nil
	}

	log.V(1).Info("Allocating")
	ip, err := r.allocate(ctx, log, publicIP)
	if err != nil {
		log.V(1).Info("Error allocating, patching public ip as pending", "Error", err)
		r.Eventf(publicIP, corev1.EventTypeNormal, FailedAllocatingPublicIP, "Failed allocating: %w", err)
		if err := r.patchStatusPending(ctx, publicIP); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			log.V(1).Info("Public IP is already gone")
			return ctrl.Result{}, nil
		}
		log.V(1).Info("Successfully marked public ip as pending")
		return ctrl.Result{}, nil
	}

	log = log.WithValues("IP", ip)
	log.V(1).Info("Successfully allocated")

	if publicIP.Spec.IP == nil {
		log.V(1).Info("Patching allocated ip into spec")
		if err := r.patchPublicIPSpecIP(ctx, publicIP, ip); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}

			log.V(1).Info("Public IP is already gone, releasing it")
			r.release(ctx, client.ObjectKeyFromObject(publicIP))
			return ctrl.Result{}, nil
		}
		log.V(1).Info("Patched allocated ip into spec, requeueing")
		return ctrl.Result{Requeue: true}, nil
	}

	log.V(1).Info("Patching public ip as allocated")
	if err := r.patchStatusAllocated(ctx, publicIP); err != nil {
		return ctrl.Result{}, err
	}
	log.V(1).Info("Patched public ip status")

	log.V(1).Info("Reconciled")
	return ctrl.Result{}, nil
}

func (r *PublicIPReconciler) patchPublicIPSpecIP(ctx context.Context, publicIP *onmetalapinetv1alpha1.PublicIP, ip netip.Addr) error {
	base := publicIP.DeepCopy()
	publicIP.Spec.IP = &onmetalapinetv1alpha1.IP{Addr: ip}
	if err := r.Patch(ctx, publicIP, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("error patching spec ips: %w", err)
	}
	return nil
}

func (r *PublicIPReconciler) patchStatusPending(ctx context.Context, publicIP *onmetalapinetv1alpha1.PublicIP) error {
	base := publicIP.DeepCopy()
	onmetalapinetv1alpha1.SetPublicIPCondition(&publicIP.Status.Conditions, onmetalapinetv1alpha1.PublicIPCondition{
		Type:    onmetalapinetv1alpha1.PublicIPAllocated,
		Reason:  "Pending",
		Status:  corev1.ConditionFalse,
		Message: "The public ip could not yet be allocated.",
	})
	if err := r.Status().Patch(ctx, publicIP, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("error patching status: %w", err)
	}
	return nil
}

func (r *PublicIPReconciler) patchStatusAllocated(ctx context.Context, publicIP *onmetalapinetv1alpha1.PublicIP) error {
	base := publicIP.DeepCopy()
	onmetalapinetv1alpha1.SetPublicIPCondition(&publicIP.Status.Conditions, onmetalapinetv1alpha1.PublicIPCondition{
		Type:    onmetalapinetv1alpha1.PublicIPAllocated,
		Reason:  "Allocated",
		Status:  corev1.ConditionTrue,
		Message: "The public ip was successfully allocated.",
	})
	if err := r.Status().Patch(ctx, publicIP, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("error patching status: %w", err)
	}
	return nil
}

func (r *PublicIPReconciler) isAllocated(publicIP *onmetalapinetv1alpha1.PublicIP) bool {
	idx := onmetalapinetv1alpha1.PublicIPConditionIndex(publicIP.Status.Conditions, onmetalapinetv1alpha1.PublicIPAllocated)
	return idx != -1 && publicIP.Status.Conditions[idx].Status == corev1.ConditionTrue
}

func (r *PublicIPReconciler) initialize(ctx context.Context) error {
	r.allocations = newPublicIPAllocations(r.InitialAvailableIPs)
	r.released = make(chan struct{}, 1024)
	publicIPList := &onmetalapinetv1alpha1.PublicIPList{}
	if err := r.APIReader.List(ctx, publicIPList); err != nil {
		return fmt.Errorf("error listing public ips: %w", err)
	}

	var sb netipx.IPSetBuilder
	sb.AddSet(r.InitialAvailableIPs)

	for _, publicIP := range publicIPList.Items {
		publicIPKey := client.ObjectKeyFromObject(&publicIP)
		if !r.isAllocated(&publicIP) {
			continue
		}

		req := publicIPAllocationRequestFromPublicIP(&publicIP)
		if _, err := r.allocations.allocate(publicIPKey, publicIP.UID, req); err != nil {
			return fmt.Errorf("[public ip %s] cannot allocate: %w", publicIPKey, err)
		}
	}

	return nil
}

func (r *PublicIPReconciler) determineReconciliationCandidates(publicIPs []onmetalapinetv1alpha1.PublicIP) []onmetalapinetv1alpha1.PublicIP {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var candidates []onmetalapinetv1alpha1.PublicIP
	for _, publicIP := range publicIPs {
		if !publicIP.DeletionTimestamp.IsZero() {
			continue
		}

		if r.isAllocated(&publicIP) {
			continue
		}

		req := publicIPAllocationRequestFromPublicIP(&publicIP)
		if r.allocations.canFit(req) {
			candidates = append(candidates, publicIP)
		}
	}
	return candidates
}

func (r *PublicIPReconciler) requeuePublicIPCandidates(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx).WithName("publicip").WithName("requeue-candidates")
	for {
		select {
		case <-ctx.Done():
			log.V(1).Info("Shutting down candidate requeuing")
			return nil
		case <-r.released:
			if err := func() error {
				log.V(1).Info("IP released")

				log.V(1).Info("Listing public ips")
				publicIPList := &onmetalapinetv1alpha1.PublicIPList{}
				if err := r.List(ctx, publicIPList); err != nil {
					return fmt.Errorf("error listing public ips: %w", err)
				}

				log.V(1).Info("Determining candidates")
				candidates := r.determineReconciliationCandidates(publicIPList.Items)

				var errs []error
				for _, candidate := range candidates {
					log.V(1).Info("Requesting reconciliation", "CandidateKey", client.ObjectKeyFromObject(&candidate))
					if err := PatchAddReconcileAnnotation(ctx, r.Client, &candidate); client.IgnoreNotFound(err) != nil {
						errs = append(errs, err)
					}
				}

				if len(errs) > 0 {
					return fmt.Errorf("error(s) requesting candidate reconciliation(s): %v", errs)
				}
				log.V(1).Info("Handled ip release")
				return nil
			}(); err != nil {
				log.Error(err, "Error requeuing public ip candidates")
			}
		}
	}
}

func (r *PublicIPReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		setupLog := ctrl.LoggerFrom(ctx).WithName("publicip").WithName("setup")
		setupLog.V(1).Info("Initializing")
		if err := r.initialize(ctx); err != nil {
			return fmt.Errorf("error initializing: %w", err)
		}
		setupLog.V(1).Info("Initialized")

		if err := mgr.Add(manager.RunnableFunc(r.requeuePublicIPCandidates)); err != nil {
			return fmt.Errorf("error adding requeue public ip candidates runnable: %w", err)
		}

		return ctrl.NewControllerManagedBy(mgr).
			For(&onmetalapinetv1alpha1.PublicIP{}).
			Complete(r)
	}))
}
