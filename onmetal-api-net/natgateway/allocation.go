package natgateway

import (
	"github.com/onmetal/onmetal-api-net/api/v1alpha1"
	"github.com/onmetal/onmetal-api-net/onmetal-api-net/utils"
)

const (
	minEphemeralPort   int32 = 1024
	maxEphemeralPort   int32 = 65535
	noOfEphemeralPorts       = maxEphemeralPort + 1 - minEphemeralPort
)

type AllocationManager struct {
	portsPerNetworkInterface int32
	slots                    *utils.Slots[v1alpha1.IP]
}

func SlotsPerIP(portsPerNetworkInterface int32) int32 {
	return noOfEphemeralPorts / portsPerNetworkInterface
}

func NewAllocationManager(portsPerNetworkInterface int32, ips []v1alpha1.IP) *AllocationManager {
	slotsPerIP := uint(noOfEphemeralPorts / portsPerNetworkInterface)
	slots := utils.NewSlots(slotsPerIP, ips)

	return &AllocationManager{
		portsPerNetworkInterface: portsPerNetworkInterface,
		slots:                    slots,
	}
}

func (m *AllocationManager) endPort(port int32) int32 {
	return port + m.portsPerNetworkInterface - 1
}

func (m *AllocationManager) slotForPorts(port, endPort int32) (uint, bool) {
	if port < minEphemeralPort || port >= endPort || endPort > maxEphemeralPort {
		return 0, false
	}
	if m.endPort(port) != endPort {
		return 0, false
	}
	return uint((port - minEphemeralPort) / m.portsPerNetworkInterface), true
}

func (m *AllocationManager) portsForSlot(slot uint) (port, endPort int32) {
	port = int32(slot)*m.portsPerNetworkInterface + minEphemeralPort
	endPort = m.endPort(port)
	return port, endPort
}

func (m *AllocationManager) Use(ip v1alpha1.IP, port, endPort int32) bool {
	slot, ok := m.slotForPorts(port, endPort)
	if !ok {
		return false
	}

	return m.slots.Use(ip, slot)
}

func (m *AllocationManager) UseNextFree() (ip v1alpha1.IP, port, endPort int32, ok bool) {
	ip, slot, ok := m.slots.UseNextFree()
	if !ok {
		return v1alpha1.IP{}, 0, 0, false
	}

	port, endPort = m.portsForSlot(slot)
	return ip, port, endPort, true
}

func (m *AllocationManager) Total() int64 {
	return int64(m.slots.Total())
}

func (m *AllocationManager) Used() int64 {
	return int64(m.slots.Used())
}
