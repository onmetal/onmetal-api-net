/*
 * Copyright (c) 2022 by the OnMetal authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
// Code generated by lister-gen. DO NOT EDIT.

package v1alpha1

import (
	v1alpha1 "github.com/onmetal/onmetal-api-net/api/core/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// LoadBalancerRoutingLister helps list LoadBalancerRoutings.
// All objects returned here must be treated as read-only.
type LoadBalancerRoutingLister interface {
	// List lists all LoadBalancerRoutings in the indexer.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1alpha1.LoadBalancerRouting, err error)
	// LoadBalancerRoutings returns an object that can list and get LoadBalancerRoutings.
	LoadBalancerRoutings(namespace string) LoadBalancerRoutingNamespaceLister
	LoadBalancerRoutingListerExpansion
}

// loadBalancerRoutingLister implements the LoadBalancerRoutingLister interface.
type loadBalancerRoutingLister struct {
	indexer cache.Indexer
}

// NewLoadBalancerRoutingLister returns a new LoadBalancerRoutingLister.
func NewLoadBalancerRoutingLister(indexer cache.Indexer) LoadBalancerRoutingLister {
	return &loadBalancerRoutingLister{indexer: indexer}
}

// List lists all LoadBalancerRoutings in the indexer.
func (s *loadBalancerRoutingLister) List(selector labels.Selector) (ret []*v1alpha1.LoadBalancerRouting, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.LoadBalancerRouting))
	})
	return ret, err
}

// LoadBalancerRoutings returns an object that can list and get LoadBalancerRoutings.
func (s *loadBalancerRoutingLister) LoadBalancerRoutings(namespace string) LoadBalancerRoutingNamespaceLister {
	return loadBalancerRoutingNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// LoadBalancerRoutingNamespaceLister helps list and get LoadBalancerRoutings.
// All objects returned here must be treated as read-only.
type LoadBalancerRoutingNamespaceLister interface {
	// List lists all LoadBalancerRoutings in the indexer for a given namespace.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1alpha1.LoadBalancerRouting, err error)
	// Get retrieves the LoadBalancerRouting from the indexer for a given namespace and name.
	// Objects returned here must be treated as read-only.
	Get(name string) (*v1alpha1.LoadBalancerRouting, error)
	LoadBalancerRoutingNamespaceListerExpansion
}

// loadBalancerRoutingNamespaceLister implements the LoadBalancerRoutingNamespaceLister
// interface.
type loadBalancerRoutingNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all LoadBalancerRoutings in the indexer for a given namespace.
func (s loadBalancerRoutingNamespaceLister) List(selector labels.Selector) (ret []*v1alpha1.LoadBalancerRouting, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.LoadBalancerRouting))
	})
	return ret, err
}

// Get retrieves the LoadBalancerRouting from the indexer for a given namespace and name.
func (s loadBalancerRoutingNamespaceLister) Get(name string) (*v1alpha1.LoadBalancerRouting, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1alpha1.Resource("loadbalancerrouting"), name)
	}
	return obj.(*v1alpha1.LoadBalancerRouting), nil
}
