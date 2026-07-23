package domain

// PoolSlot identifies a single slot inside the mappool.
type PoolSlot struct {
	Mod   PieceMod `json:"mod" bson:"mod"`
	Index int      `json:"index" bson:"index"` // 1-based within the mod group
}

// String returns a stable identifier such as "NM-1" or "FM-5".
func (s PoolSlot) String() string {
	return string(s.Mod) + "-" + itoa(s.Index)
}

// Mappool holds all beatmap slots available for a match.
// The number of pieces in each mod group is controlled by the frontend.
type Mappool struct {
	Slots map[PieceMod][]Piece `json:"slots" bson:"slots"`
}

// NewMappool creates an empty mappool.
func NewMappool() Mappool {
	return Mappool{Slots: make(map[PieceMod][]Piece)}
}

// FindSlot returns the piece matching the slot, or nil if not found.
func (m Mappool) FindSlot(slot PoolSlot) *Piece {
	group, ok := m.Slots[slot.Mod]
	if !ok || slot.Index < 1 || slot.Index > len(group) {
		return nil
	}
	return &group[slot.Index-1]
}

// ActiveSlots returns all slots that have not been removed (beatmap_id != -1).
// A slot with beatmap_id == nil (Shiro) or beatmap_id == 0 is still active
// but has no beatmap metadata.
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
func (m Mappool) ActiveSlotsByMod(mod PieceMod) []Piece {
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
