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

package apiutils

import (
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

func IsNetworkAllocated(network *v1alpha1.Network) bool {
	for _, condition := range network.Status.Conditions {
		if condition.Type == v1alpha1.NetworkAllocated {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func IsPublicIPAllocated(ip *v1alpha1.PublicIP) bool {
	for _, condition := range ip.Status.Conditions {
		if condition.Type == v1alpha1.PublicIPAllocated {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}
