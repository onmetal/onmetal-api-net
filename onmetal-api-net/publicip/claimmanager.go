package publicip

import (
	"context"
	"fmt"

	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ClaimManager struct {
	client      client.Client
	claimerKind string
	claimer     client.Object
	match       func(*v1alpha1.PublicIP) bool
}

func NewClaimManager(c client.Client, claimerKind string, claimer client.Object, match func(*v1alpha1.PublicIP) bool) *ClaimManager {
	return &ClaimManager{
		client:      c,
		claimerKind: claimerKind,
		claimer:     claimer,
		match:       match,
	}
}

func (r *ClaimManager) Claim(
	ctx context.Context,
	publicIP *v1alpha1.PublicIP,
) (bool, error) {
	claimRef := publicIP.Spec.ClaimerRef
	if claimRef != nil {
		if claimRef.UID != r.claimer.GetUID() {
			// Claimed by someone else, ignore.
			return false, nil
		}
		if r.match(publicIP) {
			// We own it and selector matches.
			// Even if we're deleting, we're allowed to own it.
			return true, nil
		}

		if !r.claimer.GetDeletionTimestamp().IsZero() {
			// We're already being deleted, don't try to release.
			return false, nil
		}

		// We own it but don't need it and are not being deleted - release it.
		if err := r.releasePublicIP(ctx, publicIP); err != nil {
			if !apierrors.IsNotFound(err) {
				return false, err
			}
			// Ignore release error if it doesn't exist anymore.
			return false, nil
		}
		// Successfully released.
		return false, nil
	}

	// It's not being claimed at the moment.

	if !r.claimer.GetDeletionTimestamp().IsZero() || !r.match(publicIP) {
		// We are being deleted / don't want to claim it - skip it.
		return false, nil
	}
	if !publicIP.DeletionTimestamp.IsZero() {
		// Ignore it if it's being deleted
		return false, nil
	}

	if err := r.adoptPublicIP(ctx, publicIP); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, err
		}
		// Ignore claim attempt if it wasn't found.
		return false, nil
	}

	// Successfully adopted.
	return true, nil
}

func (r *ClaimManager) releasePublicIP(ctx context.Context, publicIP *v1alpha1.PublicIP) error {
	basePublicIP := publicIP.DeepCopy()
	publicIP.Spec.ClaimerRef = nil
	if err := r.client.Patch(ctx, publicIP, client.MergeFrom(basePublicIP)); err != nil {
		return fmt.Errorf("error patching public ip: %w", err)
	}
	return nil
}

func (r *ClaimManager) adoptPublicIP(ctx context.Context, publicIP *v1alpha1.PublicIP) error {
	basePublicIP := publicIP.DeepCopy()
	publicIP.Spec.ClaimerRef = &v1alpha1.PublicIPClaimerRef{
		Kind: r.claimerKind,
		Name: r.claimer.GetName(),
		UID:  r.claimer.GetUID(),
	}
	if err := r.client.Patch(ctx, publicIP, client.MergeFrom(basePublicIP)); err != nil {
		return fmt.Errorf("error patching public ip: %w", err)
	}
	return nil
}
