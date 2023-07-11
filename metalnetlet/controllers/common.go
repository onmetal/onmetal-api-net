package controllers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	MetalnetFieldOwner = client.FieldOwner("metalnetlet.apinet.onmetal.de/controller-manager")

	PartitionFieldOwnerPrefix = "partition.metalnetlet.apinet.onmetal.de/"

	PartitionFinalizerPrefix = "partition.metalnetlet.apinet.onmetal.de/"

	NetworkInterfaceKind = "NetworkInterface"
)

func PartitionFieldOwner(partitionName string) client.FieldOwner {
	return client.FieldOwner(PartitionFieldOwnerPrefix + partitionName)
}

func PartitionFinalizer(partitionName string) string {
	return PartitionFinalizerPrefix + partitionName
}

func PartitionNodeName(partitionName, metalnetNodeName string) string {
	return fmt.Sprintf("%s-%s", string(partitionName), metalnetNodeName)
}

func ParseNodeName(partitionName, nodeName string) (string, error) {
	prefix := partitionName + "-"
	if !strings.HasPrefix(nodeName, prefix) {
		return "", fmt.Errorf("node name %q does not belong to partition %s", nodeName, partitionName)
	}
	return strings.TrimPrefix(nodeName, prefix), nil
}

func IsNodeOnPartitionPredicate(partitionName string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		node := obj.(*v1alpha1.Node)
		return node.Spec.PartitionRef.Name == partitionName
	})
}

func MetalnetNetworkName(vni int32) string {
	return fmt.Sprintf("n%d", vni)
}

type WaitFor interface {
	Wait(ctx context.Context) error
}

type RunAfterOptions struct {
	Timeout time.Duration
}

func (o *RunAfterOptions) ApplyToRunAfter(o2 *RunAfterOptions) {
	if o.Timeout > 0 {
		o2.Timeout = o.Timeout
	}
}

func (o *RunAfterOptions) ApplyOptions(opts []RunAfterOption) {
	for _, opt := range opts {
		opt.ApplyToRunAfter(o)
	}
}

type RunAfterOption interface {
	ApplyToRunAfter(o *RunAfterOptions)
}

type WithTimeout time.Duration

func (w WithTimeout) ApplyToRunAfter(o *RunAfterOptions) {
	o.Timeout = time.Duration(w)
}

func SetupRunAfter(mgr ctrl.Manager, f func(ctx context.Context) error, waiters []WaitFor, opts ...RunAfterOption) error {
	o := &RunAfterOptions{}
	o.ApplyOptions(opts)

	return mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		if len(waiters) == 0 {
			// short-circuit if there are no waiters.
			return f(ctx)
		}

		if o.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, o.Timeout)
			defer cancel()
		}

		waitCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		var (
			wg      sync.WaitGroup
			errChan = make(chan error)
		)
		wg.Add(len(waiters))
		go func() {
			defer close(errChan)
			wg.Wait()
		}()

		for _, waiter := range waiters {
			go func(waiter WaitFor) {
				defer wg.Done()
				errChan <- waiter.Wait(waitCtx)
			}(waiter)
		}

		err := <-errChan
		for range errChan { // consume all remaining errors
		}

		if err != nil {
			return err
		}
		return f(ctx)
	}))
}

func GetMetalnetNode(ctx context.Context, partitionName string, metalnetClient client.Client, nodeName string) (*corev1.Node, error) {
	metalnetNodeName, err := ParseNodeName(partitionName, nodeName)
	if err != nil {
		// Ignore any parsing error, what we know is that the node does not exist on our side.
		return nil, nil
	}

	metalnetNode := &corev1.Node{}
	metalnetNodeKey := client.ObjectKey{Name: metalnetNodeName}
	if err := metalnetClient.Get(ctx, metalnetNodeKey, metalnetNode); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, nil
	}
	return metalnetNode, nil
}
