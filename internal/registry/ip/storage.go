// Copyright 2023 OnMetal authors
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

package ip

import (
	"context"
	"fmt"

	"github.com/onmetal/onmetal-api-net/api/core/v1alpha1"
	"github.com/onmetal/onmetal-api-net/apimachinery/api/net"
	"github.com/onmetal/onmetal-api-net/internal/apis/core"
	"github.com/onmetal/onmetal-api-net/internal/registry/ip/ipaddressallocator"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/generic"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	apisrvstorage "k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/util/dryrun"
	"k8s.io/klog/v2"
)

type IPStorage struct {
	IP *REST
}

type REST struct {
	*genericregistry.Store
	allocatorByFamily map[corev1.IPFamily]ipaddressallocator.Interface
}

func (r *REST) beginCreate(ctx context.Context, obj runtime.Object, opts *metav1.CreateOptions) (genericregistry.FinishFunc, error) {
	ip := obj.(*core.IP)

	alloc, ok := r.allocatorByFamily[ip.Spec.IPFamily]
	if !ok {
		return nil, fmt.Errorf("cannot allocate IPs of family %s", ip.Spec.IPFamily)
	}
	if dryrun.IsDryRun(opts.DryRun) {
		alloc = alloc.DryRun()
	}

	claimRef := v1alpha1.IPAddressClaimRef{
		Group:     v1alpha1.GroupName,
		Resource:  "ips",
		Namespace: ip.Namespace,
		Name:      ip.Name,
		UID:       ip.UID,
	}

	addr := ip.Spec.IP
	if addr.IsValid() {
		if err := alloc.Allocate(claimRef, addr.Addr); err != nil {
			return nil, err
		}
	} else {
		newAddr, err := alloc.AllocateNext(claimRef)
		if err != nil {
			return nil, err
		}

		addr = net.IP{Addr: newAddr}
		ip.Spec.IP = addr
	}
	metav1.SetMetaDataLabel(&ip.ObjectMeta, v1alpha1.IPFamilyLabel, string(alloc.IPFamily()))
	metav1.SetMetaDataLabel(&ip.ObjectMeta, v1alpha1.IPIPLabel, addr.String())

	return func(ctx context.Context, success bool) {
		if success {
			klog.InfoS("allocated IP", "IP", addr)
			return
		}

		if err := alloc.Release(addr.Addr); err != nil {
			klog.InfoS("error releasing IP", "IP", addr, "err", err)
		}
	}, nil
}

func (r *REST) afterDelete(obj runtime.Object, opts *metav1.DeleteOptions) {
	ip := obj.(*core.IP)

	if !dryrun.IsDryRun(opts.DryRun) {
		alloc, ok := r.allocatorByFamily[ip.Spec.IPFamily]
		if !ok {
			return
		}

		addr := ip.Spec.IP.Addr
		if err := alloc.Release(addr); err != nil {
			klog.InfoS("error releasing IP", "IP", addr, "err", err)
		}
	}
}

func NewStorage(
	scheme *runtime.Scheme,
	optsGetter generic.RESTOptionsGetter,
	allocatorByFamily map[corev1.IPFamily]ipaddressallocator.Interface,
) (IPStorage, error) {
	strategy := NewStrategy(scheme)

	store := &genericregistry.Store{
		NewFunc: func() runtime.Object {
			return &core.IP{}
		},
		NewListFunc: func() runtime.Object {
			return &core.IPList{}
		},
		PredicateFunc:             MatchIP,
		DefaultQualifiedResource:  core.Resource("ips"),
		SingularQualifiedResource: core.Resource("ip"),

		CreateStrategy: strategy,
		UpdateStrategy: strategy,
		DeleteStrategy: strategy,

		TableConvertor: newTableConvertor(),
	}

	options := &generic.StoreOptions{
		RESTOptions: optsGetter,
		AttrFunc:    GetAttrs,
		TriggerFunc: map[string]apisrvstorage.IndexerFunc{"spec.ip": IPTriggerFunc},
		Indexers:    Indexers(),
	}
	if err := store.CompleteWithOptions(options); err != nil {
		return IPStorage{}, err
	}

	genericStore := &REST{
		Store:             store,
		allocatorByFamily: allocatorByFamily,
	}

	store.BeginCreate = genericStore.beginCreate
	store.AfterDelete = genericStore.afterDelete

	return IPStorage{
		IP: genericStore,
	}, nil
}
