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

	"github.com/onmetal/controller-utils/metautils"
	onmetalapinetv1alpha1 "github.com/onmetal/onmetal-api-net/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/apiutils"
	commonv1alpha1 "github.com/onmetal/onmetal-api/api/common/v1alpha1"
	utilannotations "github.com/onmetal/onmetal-api/utils/annotations"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const fieldOwner = client.FieldOwner("api.onmetal.de/apinetlet")

func getApiNetPublicIPAllocationChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(event event.UpdateEvent) bool {
			oldAPINetPublicIP, newAPINetPublicIP := event.ObjectOld.(*onmetalapinetv1alpha1.PublicIP), event.ObjectNew.(*onmetalapinetv1alpha1.PublicIP)
			return apiutils.IsPublicIPAllocated(oldAPINetPublicIP) != apiutils.IsPublicIPAllocated(newAPINetPublicIP)
		},
	}
}

type enqueueByAPINetObjectOptions struct {
	filters       []func(client.Object) bool
	labelSelector labels.Selector
}

type enqueueByAPINetObjectOption interface {
	applyToEnqueueByAPINetObject(o *enqueueByAPINetObjectOptions)
}

type enqueueByAPINetObjectOptionFunc func(*enqueueByAPINetObjectOptions)

func (f enqueueByAPINetObjectOptionFunc) applyToEnqueueByAPINetObject(o *enqueueByAPINetObjectOptions) {
	f(o)
}

func (o *enqueueByAPINetObjectOptions) applyOptions(opts []enqueueByAPINetObjectOption) {
	for _, opt := range opts {
		opt.applyToEnqueueByAPINetObject(o)
	}
}

type filters struct {
	filters []func(client.Object) bool
}

func withFilters(filter ...func(client.Object) bool) filters {
	return filters{filters: filter}
}

func (w filters) applyToEnqueueByAPINetObject(o *enqueueByAPINetObjectOptions) {
	o.filters = w.filters
}

type matchingLabelSelector struct {
	Selector labels.Selector
}

func (m matchingLabelSelector) applyToEnqueueByAPINetObject(o *enqueueByAPINetObjectOptions) {
	o.labelSelector = m.Selector
}

func notExternallyManagedFilter() func(obj client.Object) bool {
	return func(obj client.Object) bool {
		return utilannotations.IsExternallyManaged(obj)
	}
}

func watchLabelSelector(watchLabel string) labels.Selector {
	if watchLabel == "" {
		return nil
	}

	return labels.SelectorFromSet(map[string]string{
		commonv1alpha1.WatchLabel: watchLabel,
	})
}

func enqueueByAPINetObjectName(c client.Client, list client.ObjectList, opts ...enqueueByAPINetObjectOption) handler.EventHandler {
	o := &enqueueByAPINetObjectOptions{}
	o.applyOptions(opts)

	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		log := ctrl.LoggerFrom(ctx)

		listOpts := []client.ListOption{
			client.MatchingFields{"metadata.name": obj.GetName()},
		}
		if o.labelSelector != nil {
			listOpts = append(listOpts, client.MatchingLabelsSelector{Selector: o.labelSelector})
		}

		if err := c.List(ctx, list, listOpts...); err != nil {
			log.Error(err, "Error listing objects")
			return nil
		}

		var reqs []ctrl.Request
		if err := metautils.EachListItem(list, func(obj client.Object) error {
			for _, filter := range o.filters {
				if !filter(obj) {
					return nil
				}
			}
			reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)})
			return nil
		}); err != nil {
			log.Error(err, "Error iterating list")
			return nil
		}
		return reqs
	})
}
