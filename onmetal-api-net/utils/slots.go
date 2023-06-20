package utils

import (
	"github.com/bits-and-blooms/bitset"
	"k8s.io/apimachinery/pkg/util/sets"
)

type Slots[K comparable] struct {
	slotsPerKey uint
	used        uint
	slotsByKey  map[K]*bitset.BitSet
	freeKeys    sets.Set[K]
}

func NewSlots[K comparable](slotsPerKey uint, keys []K) *Slots[K] {
	var (
		slotsByKey = make(map[K]*bitset.BitSet)
		freeKeys   = sets.New[K]()
	)

	for _, key := range keys {
		if _, ok := slotsByKey[key]; ok {
			// don't re-initialize on duplicate ips
			continue
		}

		slotsByKey[key] = bitset.New(slotsPerKey)
		freeKeys.Insert(key)
	}

	return &Slots[K]{
		slotsPerKey: slotsPerKey,
		slotsByKey:  slotsByKey,
		freeKeys:    freeKeys,
	}
}

// Total returns the total number of slots.
func (s *Slots[K]) Total() uint {
	if s == nil {
		return 0
	}
	return uint(len(s.slotsByKey)) * s.slotsPerKey
}

// Used returns the used number of slots.
func (s *Slots[K]) Used() uint {
	if s == nil {
		return 0
	}
	return s.used
}

func (s *Slots[K]) Use(key K, slot uint) bool {
	if s == nil {
		return false
	}
	// Test whether the slot is valid at all.
	if slot >= s.slotsPerKey {
		return false
	}

	slots, ok := s.slotsByKey[key]
	if !ok || slots.Test(slot) {
		return false
	}

	slots.Set(slot)
	s.used++
	if slots.All() {
		s.freeKeys.Delete(key)
	}
	return true
}

func (s *Slots[K]) UseNextFree() (K, uint, bool) {
	if s == nil {
		var zero K
		return zero, 0, false
	}

	// Shortcut if there are no free keys.
	if s.freeKeys.Len() == 0 {
		var zero K
		return zero, 0, false
	}

	for key := range s.freeKeys {
		slot, ok := s.slotsByKey[key].NextClear(0)
		if ok {
			s.Use(key, slot)
			return key, slot, true
		}
	}
	var zero K
	return zero, 0, false
}
