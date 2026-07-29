package domain

import "fmt"

// SlotRef identifies a single slot inside the mappool.
// This is a lookup key (Mod + 1-based Index), distinct from
// the engine's PoolSlot which tracks per-slot competitive state.
type SlotRef struct {
	Mod   Mod `json:"mod" bson:"mod"`
	Index int `json:"index" bson:"index"` // 1-based within the mod group
}

// String returns a stable identifier such as "NM-1" or "FM-5".
func (s SlotRef) String() string {
	return string(s.Mod) + "-" + itoa(s.Index)
}

// Mappool holds all beatmap slots available for a match.
type Mappool struct {
	Slots map[Mod][]Piece `json:"slots" bson:"slots"`
}

// NewMappool creates an empty mappool.
func NewMappool() Mappool {
	return Mappool{Slots: make(map[Mod][]Piece)}
}

// FindSlot returns the piece matching the slot reference, or nil.
func (m Mappool) FindSlot(slot SlotRef) *Piece {
	group, ok := m.Slots[slot.Mod]
	if !ok || slot.Index < 1 || slot.Index > len(group) {
		return nil
	}
	return &group[slot.Index-1]
}

// ActiveSlots returns all slots that have not been removed.
func (m Mappool) ActiveSlots() []Piece {
	var active []Piece
	for _, group := range m.Slots {
		for i := range group {
			if !group[i].IsRemoved() {
				active = append(active, group[i])
			}
		}
	}
	return active
}

// ActiveSlotsByMod returns active slots for a specific mod group.
func (m Mappool) ActiveSlotsByMod(mod Mod) []Piece {
	var active []Piece
	group, ok := m.Slots[mod]
	if !ok {
		return active
	}
	for i := range group {
		if !group[i].IsRemoved() {
			active = append(active, group[i])
		}
	}
	return active
}

// ParseSlotRef parses a string such as "NM-1" into a SlotRef.
func ParseSlotRef(s string) (SlotRef, bool) {
	var mod Mod
	var idx int
	_, err := fmt.Sscanf(s, "%s-%d", &mod, &idx)
	if err != nil || idx < 1 {
		return SlotRef{}, false
	}
	return SlotRef{Mod: mod, Index: idx}, true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	sign := n
	if sign < 0 {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if sign < 0 {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
