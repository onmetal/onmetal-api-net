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
// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1alpha1

import (
	corev1alpha1 "github.com/onmetal/onmetal-api-net/api/core/v1alpha1"
	internal "github.com/onmetal/onmetal-api-net/client-go/applyconfigurations/internal"
	v1 "github.com/onmetal/onmetal-api-net/client-go/applyconfigurations/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	managedfields "k8s.io/apimachinery/pkg/util/managedfields"
)

// NetworkPolicyTargetsApplyConfiguration represents an declarative configuration of the NetworkPolicyTargets type for use
// with apply.
type NetworkPolicyTargetsApplyConfiguration struct {
	v1.TypeMetaApplyConfiguration    `json:",inline"`
	*v1.ObjectMetaApplyConfiguration `json:"metadata,omitempty"`
	Targets                          []NetworkPolicyTargetApplyConfiguration `json:"targets,omitempty"`
}

// NetworkPolicyTargets constructs an declarative configuration of the NetworkPolicyTargets type for use with
// apply.
func NetworkPolicyTargets(name, namespace string) *NetworkPolicyTargetsApplyConfiguration {
	b := &NetworkPolicyTargetsApplyConfiguration{}
	b.WithName(name)
	b.WithNamespace(namespace)
	b.WithKind("NetworkPolicyTargets")
	b.WithAPIVersion("core.apinet.api.onmetal.de/v1alpha1")
	return b
}

// ExtractNetworkPolicyTargets extracts the applied configuration owned by fieldManager from
// networkPolicyTargets. If no managedFields are found in networkPolicyTargets for fieldManager, a
// NetworkPolicyTargetsApplyConfiguration is returned with only the Name, Namespace (if applicable),
// APIVersion and Kind populated. It is possible that no managed fields were found for because other
// field managers have taken ownership of all the fields previously owned by fieldManager, or because
// the fieldManager never owned fields any fields.
// networkPolicyTargets must be a unmodified NetworkPolicyTargets API object that was retrieved from the Kubernetes API.
// ExtractNetworkPolicyTargets provides a way to perform a extract/modify-in-place/apply workflow.
// Note that an extracted apply configuration will contain fewer fields than what the fieldManager previously
// applied if another fieldManager has updated or force applied any of the previously applied fields.
// Experimental!
func ExtractNetworkPolicyTargets(networkPolicyTargets *corev1alpha1.NetworkPolicyTargets, fieldManager string) (*NetworkPolicyTargetsApplyConfiguration, error) {
	return extractNetworkPolicyTargets(networkPolicyTargets, fieldManager, "")
}

// ExtractNetworkPolicyTargetsStatus is the same as ExtractNetworkPolicyTargets except
// that it extracts the status subresource applied configuration.
// Experimental!
func ExtractNetworkPolicyTargetsStatus(networkPolicyTargets *corev1alpha1.NetworkPolicyTargets, fieldManager string) (*NetworkPolicyTargetsApplyConfiguration, error) {
	return extractNetworkPolicyTargets(networkPolicyTargets, fieldManager, "status")
}

func extractNetworkPolicyTargets(networkPolicyTargets *corev1alpha1.NetworkPolicyTargets, fieldManager string, subresource string) (*NetworkPolicyTargetsApplyConfiguration, error) {
	b := &NetworkPolicyTargetsApplyConfiguration{}
	err := managedfields.ExtractInto(networkPolicyTargets, internal.Parser().Type("com.github.onmetal.onmetal-api-net.api.core.v1alpha1.NetworkPolicyTargets"), fieldManager, b, subresource)
	if err != nil {
		return nil, err
	}
	b.WithName(networkPolicyTargets.Name)
	b.WithNamespace(networkPolicyTargets.Namespace)

	b.WithKind("NetworkPolicyTargets")
	b.WithAPIVersion("core.apinet.api.onmetal.de/v1alpha1")
	return b, nil
}

// WithKind sets the Kind field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Kind field is set to the value of the last call.
func (b *NetworkPolicyTargetsApplyConfiguration) WithKind(value string) *NetworkPolicyTargetsApplyConfiguration {
	b.Kind = &value
	return b
}

// WithAPIVersion sets the APIVersion field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the APIVersion field is set to the value of the last call.
func (b *NetworkPolicyTargetsApplyConfiguration) WithAPIVersion(value string) *NetworkPolicyTargetsApplyConfiguration {
	b.APIVersion = &value
	return b
}

// WithName sets the Name field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Name field is set to the value of the last call.
func (b *NetworkPolicyTargetsApplyConfiguration) WithName(value string) *NetworkPolicyTargetsApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.Name = &value
	return b
}

// WithGenerateName sets the GenerateName field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the GenerateName field is set to the value of the last call.
func (b *NetworkPolicyTargetsApplyConfiguration) WithGenerateName(value string) *NetworkPolicyTargetsApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.GenerateName = &value
	return b
}

// WithNamespace sets the Namespace field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Namespace field is set to the value of the last call.
func (b *NetworkPolicyTargetsApplyConfiguration) WithNamespace(value string) *NetworkPolicyTargetsApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.Namespace = &value
	return b
}

// WithUID sets the UID field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the UID field is set to the value of the last call.
func (b *NetworkPolicyTargetsApplyConfiguration) WithUID(value types.UID) *NetworkPolicyTargetsApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.UID = &value
	return b
}

// WithResourceVersion sets the ResourceVersion field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ResourceVersion field is set to the value of the last call.
func (b *NetworkPolicyTargetsApplyConfiguration) WithResourceVersion(value string) *NetworkPolicyTargetsApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.ResourceVersion = &value
	return b
}

// WithGeneration sets the Generation field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Generation field is set to the value of the last call.
func (b *NetworkPolicyTargetsApplyConfiguration) WithGeneration(value int64) *NetworkPolicyTargetsApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.Generation = &value
	return b
}

// WithCreationTimestamp sets the CreationTimestamp field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the CreationTimestamp field is set to the value of the last call.
func (b *NetworkPolicyTargetsApplyConfiguration) WithCreationTimestamp(value metav1.Time) *NetworkPolicyTargetsApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.CreationTimestamp = &value
	return b
}

// WithDeletionTimestamp sets the DeletionTimestamp field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the DeletionTimestamp field is set to the value of the last call.
func (b *NetworkPolicyTargetsApplyConfiguration) WithDeletionTimestamp(value metav1.Time) *NetworkPolicyTargetsApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.DeletionTimestamp = &value
	return b
}

// WithDeletionGracePeriodSeconds sets the DeletionGracePeriodSeconds field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the DeletionGracePeriodSeconds field is set to the value of the last call.
func (b *NetworkPolicyTargetsApplyConfiguration) WithDeletionGracePeriodSeconds(value int64) *NetworkPolicyTargetsApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.DeletionGracePeriodSeconds = &value
	return b
}

// WithLabels puts the entries into the Labels field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, the entries provided by each call will be put on the Labels field,
// overwriting an existing map entries in Labels field with the same key.
func (b *NetworkPolicyTargetsApplyConfiguration) WithLabels(entries map[string]string) *NetworkPolicyTargetsApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	if b.Labels == nil && len(entries) > 0 {
		b.Labels = make(map[string]string, len(entries))
	}
	for k, v := range entries {
		b.Labels[k] = v
	}
	return b
}

// WithAnnotations puts the entries into the Annotations field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, the entries provided by each call will be put on the Annotations field,
// overwriting an existing map entries in Annotations field with the same key.
func (b *NetworkPolicyTargetsApplyConfiguration) WithAnnotations(entries map[string]string) *NetworkPolicyTargetsApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	if b.Annotations == nil && len(entries) > 0 {
		b.Annotations = make(map[string]string, len(entries))
	}
	for k, v := range entries {
		b.Annotations[k] = v
	}
	return b
}

// WithOwnerReferences adds the given value to the OwnerReferences field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the OwnerReferences field.
func (b *NetworkPolicyTargetsApplyConfiguration) WithOwnerReferences(values ...*v1.OwnerReferenceApplyConfiguration) *NetworkPolicyTargetsApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithOwnerReferences")
		}
		b.OwnerReferences = append(b.OwnerReferences, *values[i])
	}
	return b
}

// WithFinalizers adds the given value to the Finalizers field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Finalizers field.
func (b *NetworkPolicyTargetsApplyConfiguration) WithFinalizers(values ...string) *NetworkPolicyTargetsApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	for i := range values {
		b.Finalizers = append(b.Finalizers, values[i])
	}
	return b
}

func (b *NetworkPolicyTargetsApplyConfiguration) ensureObjectMetaApplyConfigurationExists() {
	if b.ObjectMetaApplyConfiguration == nil {
		b.ObjectMetaApplyConfiguration = &v1.ObjectMetaApplyConfiguration{}
	}
}

// WithTargets adds the given value to the Targets field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Targets field.
func (b *NetworkPolicyTargetsApplyConfiguration) WithTargets(values ...*NetworkPolicyTargetApplyConfiguration) *NetworkPolicyTargetsApplyConfiguration {
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithTargets")
		}
		b.Targets = append(b.Targets, *values[i])
	}
	return b
}