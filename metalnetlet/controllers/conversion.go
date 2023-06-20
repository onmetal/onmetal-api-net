package controllers

import (
	metalnetv1alpha1 "github.com/onmetal/metalnet/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	utilslices "github.com/onmetal/onmetal-api/utils/slices"
)

func ipPrefixToMetalnetPrefix(p v1alpha1.IPPrefix) metalnetv1alpha1.IPPrefix {
	return metalnetv1alpha1.IPPrefix{Prefix: p.Prefix}
}

func ipPrefixesToMetalnetPrefixes(ps []v1alpha1.IPPrefix) []metalnetv1alpha1.IPPrefix {
	return utilslices.Map(ps, ipPrefixToMetalnetPrefix)
}
