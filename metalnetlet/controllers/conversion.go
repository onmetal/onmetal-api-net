package controllers

import (
	"fmt"
	"net/netip"

	metalnetv1alpha1 "github.com/onmetal/metalnet/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	"github.com/onmetal/onmetal-api/utils/generic"
	utilslices "github.com/onmetal/onmetal-api/utils/slices"
	corev1 "k8s.io/api/core/v1"
)

func ipPrefixToMetalnetPrefix(p v1alpha1.IPPrefix) metalnetv1alpha1.IPPrefix {
	return metalnetv1alpha1.IPPrefix{Prefix: p.Prefix}
}

func ipPrefixesToMetalnetPrefixes(ps []v1alpha1.IPPrefix) []metalnetv1alpha1.IPPrefix {
	return utilslices.Map(ps, ipPrefixToMetalnetPrefix)
}

func ipToMetalnetIP(ip v1alpha1.IP) metalnetv1alpha1.IP {
	return metalnetv1alpha1.IP{Addr: ip.Addr}
}

func ipsToMetalnetIPs(ips []v1alpha1.IP) []metalnetv1alpha1.IP {
	return utilslices.Map(ips, ipToMetalnetIP)
}

func ipToMetalnetIPPrefix(ip v1alpha1.IP) metalnetv1alpha1.IPPrefix {
	return metalnetv1alpha1.IPPrefix{Prefix: netip.PrefixFrom(ip.Addr, ip.BitLen())}
}

func ipsToMetalnetIPPrefixes(ips []v1alpha1.IP) []metalnetv1alpha1.IPPrefix {
	return utilslices.Map(ips, ipToMetalnetIPPrefix)
}

func ipsIPFamilies(ips []v1alpha1.IP) []corev1.IPFamily {
	return utilslices.Map(ips, v1alpha1.IP.Family)
}

func metalnetIPToIP(ip metalnetv1alpha1.IP) v1alpha1.IP {
	return v1alpha1.IP{Addr: ip.Addr}
}

func natIPToMetalnetNATDetails(natIP v1alpha1.NATIP) metalnetv1alpha1.NATDetails {
	ip := ipToMetalnetIP(natIP.IP)
	return metalnetv1alpha1.NATDetails{
		IP:      &ip,
		Port:    natIP.Port,
		EndPort: natIP.EndPort,
	}
}

func natIPsToMetalnetNATDetails(natIPs []v1alpha1.NATIP) []metalnetv1alpha1.NATDetails {
	return utilslices.Map(natIPs, natIPToMetalnetNATDetails)
}

func metalnetIPsToIPs(ips []metalnetv1alpha1.IP) []v1alpha1.IP {
	return utilslices.Map(ips, metalnetIPToIP)
}

func metalnetIPPrefixToIPPrefix(prefix metalnetv1alpha1.IPPrefix) v1alpha1.IPPrefix {
	return v1alpha1.IPPrefix{Prefix: prefix.Prefix}
}

func metalnetIPPrefixesToIPPrefixes(prefixes []metalnetv1alpha1.IPPrefix) []v1alpha1.IPPrefix {
	return utilslices.Map(prefixes, metalnetIPPrefixToIPPrefix)
}

func metalnetNATDetailsToNATIP(natDetails metalnetv1alpha1.NATDetails) v1alpha1.NATIP {
	return v1alpha1.NATIP{
		IP:      metalnetIPToIP(*natDetails.IP),
		Port:    natDetails.Port,
		EndPort: natDetails.EndPort,
	}
}

func metalnetNATDetailsToNATIPs(natDetails []metalnetv1alpha1.NATDetails) []v1alpha1.NATIP {
	return utilslices.Map(natDetails, metalnetNATDetailsToNATIP)
}

func loadBalancerTypeToMetalnetLoadBalancerType(loadBalancerType v1alpha1.LoadBalancerType) (metalnetv1alpha1.LoadBalancerType, error) {
	switch loadBalancerType {
	case v1alpha1.LoadBalancerTypePublic:
		return metalnetv1alpha1.LoadBalancerTypePublic, nil
	case v1alpha1.LoadBalancerTypeInternal:
		return metalnetv1alpha1.LoadBalancerTypeInternal, nil
	default:
		return "", fmt.Errorf("unknown load balancer type %q", loadBalancerType)
	}
}

func loadBalancerPortToMetalnetLoadBalancerPort(port v1alpha1.LoadBalancerPort) metalnetv1alpha1.LBPort {
	protocol := generic.Deref(port.Protocol, corev1.ProtocolTCP)

	return metalnetv1alpha1.LBPort{
		Protocol: string(protocol),
		Port:     port.Port,
	}
}

func loadBalancerPortsToMetalnetLoadBalancerPorts(ports []v1alpha1.LoadBalancerPort) []metalnetv1alpha1.LBPort {
	return utilslices.Map(ports, loadBalancerPortToMetalnetLoadBalancerPort)
}

// workaroundMetalnetNoIPv6VirtualIPSupportIPsToIP works around the missing public IPv6 support in metalnet
// by propagating only IPv4 addresses to metalnet.
// TODO: Remove this as soon as https://github.com/onmetal/metalnet/issues/53 is resolved.
func workaroundMetalnetNoIPv6VirtualIPSupportIPsToIP(metalnetVirtualIPs []metalnetv1alpha1.IP) *metalnetv1alpha1.IP {
	for _, metalnetVirtualIP := range metalnetVirtualIPs {
		if metalnetVirtualIP.Is4() {
			ip := metalnetVirtualIP
			return &ip
		}
	}
	return nil
}

// workaroundMetalnetNoIPv6VirtualIPToIPs works around the missing public IPv6 support in metalnet by
// making a slice of the single virtual IP.
func workaroundMetalnetNoIPv6VirtualIPToIPs(metalnetVirtualIP *metalnetv1alpha1.IP) []metalnetv1alpha1.IP {
	if metalnetVirtualIP.IsZero() {
		return nil
	}
	return []metalnetv1alpha1.IP{*metalnetVirtualIP}
}

// workaroundMetalnetNoIPv6NATDetailsPointerToNATDetails works around the missing NAT IPv6 support in metalnet by
// making a slice of the single NAT details.
func workaroundMetalnetNoIPv6NATDetailsPointerToNATDetails(natDetails *metalnetv1alpha1.NATDetails) []metalnetv1alpha1.NATDetails {
	if natDetails == nil {
		return nil
	}
	return []metalnetv1alpha1.NATDetails{*natDetails}
}

// workaroundMetalnetNoIPv6NATDetailsToNATDetailsPointer works around the missing NAT IPv6 support in metalnet by
// returning only the IPv4 NAT details.
func workaroundMetalnetNoIPv6NATDetailsToNATDetailsPointer(natDetails []metalnetv1alpha1.NATDetails) *metalnetv1alpha1.NATDetails {
	for _, natDetails := range natDetails {
		if natDetails.IP.Is4() {
			details := natDetails
			return &details
		}
	}
	return nil
}

// workaroundMetalnetNoNATIPReportingStatusNATIP works around the missing port / endPort reporting in metalnet
// by returning the metalnetv1alpha1.NetworkInterface.Spec.NAT if the IP is the same as the one in the status.
// This is a hack, will produce wrong output and should be removed as soon as https://github.com/onmetal/metalnet/issues/54
// is implemented.
func workaroundMetalnetNoNATIPReportingStatusNATIP(nic *metalnetv1alpha1.NetworkInterface) *metalnetv1alpha1.NATDetails {
	if natIP := nic.Status.NatIP; natIP != nil {
		if nat := nic.Spec.NAT; nat != nil && *nat.IP == *natIP {
			return nat
		}
	}
	return nil
}
