package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Mappool is the manageable mappool entity referenced by rooms. Entries are
// stored inline as an array: they are always replaced wholesale together with
// the mappool document, which keeps updates atomic and prevents orphans.
type Mappool struct {
	ID          bson.ObjectID  `json:"id" bson:"_id,omitempty"`
	Name        string         `json:"name" bson:"name"` // required
	Description *string        `json:"description,omitempty" bson:"description,omitempty"`
	Entries     []MappoolEntry `json:"entries" bson:"entries"`
	CreatedAt   time.Time      `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at" bson:"updated_at"`
}

// MappoolEntry is a single slot inside a mappool. Mod and Index are required
// (index is 1-based within the mod group).
//
// Beatmap binding rules by mod:
//
//   - PieceModShiro is a placeholder. Shiro carries no beatmap (BeatmapID ==
//     nil) and is materialized automatically by the match factory; any
//     Shiro entry declared in this collection is ignored at conversion time.
//   - PieceModTB is mandatory — every mappool must declare at least one TB
//     entry, and that entry must reference a real osu! beatmap id. The engine
//     treats the TB slot as the winner-deciding track and requires exactly
//     one such slot per configuration.
//   - All other mods (NM, HD, HR, DT, FM) must reference an osu! beatmap id.
type MappoolEntry struct {
	BeatmapID  *int64   `json:"beatmap_id,omitempty" bson:"beatmap_id,omitempty"` // nil for SHIRO
	Mod        PieceMod `json:"mod" bson:"mod"`                                   // required
	Index      int      `json:"index" bson:"index"`                               // required, 1-based per mod group
	SelectorID *int64   `json:"selector_id,omitempty" bson:"selector_id,omitempty"`
	Skill      *string  `json:"skill,omitempty" bson:"skill,omitempty"`
}

// ToRuntime converts the mappool entity into the runtime pool consumed by the
// match engine. Pieces are grouped by mod (in canonical mod order) and ordered
// by index; SHIRO slots keep a nil BeatmapID. Piece states start as NORMAL.
func (m *Mappool) ToRuntime() Pool {
	pool := NewPool()
	for _, entry := range m.SortedEntries() {
		piece := Piece{State: PieceStateNormal}
		if entry.BeatmapID != nil {
			id := *entry.BeatmapID
			piece.BeatmapID = &id
		}
		pool.Slots[entry.Mod] = append(pool.Slots[entry.Mod], piece)
	}
	return pool
}

// ValidateEntries checks the entry invariants: mod must be known, index must
// be positive, (mod, index) pairs must be unique, non-SHIRO entries need a
// beatmap id, SHIRO entries must not carry one, and at least one TB entry
// must be declared. It returns the wire-format field paths of the violated
// entries (or "entries" for pool-level invariants).
func (m *Mappool) ValidateEntries() []string {
	var problems []string
	seen := make(map[PoolSlot]bool, len(m.Entries))
	hasTB := false
	for i, entry := range m.Entries {
		if !isKnownPieceMod(entry.Mod) {
			problems = append(problems, entryField(i, "mod"))
			continue
		}
		if entry.Index < 1 {
			problems = append(problems, entryField(i, "index"))
			continue
		}
		slot := PoolSlot{Mod: entry.Mod, Index: entry.Index}
		if seen[slot] {
			problems = append(problems, entryField(i, "index"))
			continue
		}
		seen[slot] = true
		if entry.Mod == PieceModShiro {
			if entry.BeatmapID != nil {
				problems = append(problems, entryField(i, "beatmap_id"))
			}
			continue
		}
		if entry.Mod == PieceModTB {
			hasTB = true
		}
		if entry.BeatmapID == nil || *entry.BeatmapID <= 0 {
			problems = append(problems, entryField(i, "beatmap_id"))
		}
	}
	if !hasTB {
		problems = append(problems, "entries[].mod=tb")
	}
	return problems
}

// HasTBEntry reports whether the pool contains at least one TB entry. It is
// the runtime counterpart to the ValidateEntries "TB required" invariant;
// the factory uses it to skip the mappool-derived TB scan when the pool has
// no beatmap to seed.
func (m *Mappool) HasTBEntry() bool {
	if m == nil {
		return false
	}
	for _, entry := range m.Entries {
		if entry.Mod == PieceModTB {
			return true
		}
	}
	return false
}

// SortedEntries returns entries grouped by mod and ordered by index, ready
// for display or runtime conversion.
func (m *Mappool) SortedEntries() []MappoolEntry {
	order := []PieceMod{PieceModNM, PieceModHD, PieceModHR, PieceModDT, PieceModFM, PieceModShiro, PieceModTB}
	counts := make(map[PieceMod]int)
	for _, entry := range m.Entries {
		counts[entry.Mod]++
	}
	sorted := make([]MappoolEntry, 0, len(m.Entries))
	for _, mod := range order {
		group := make([]MappoolEntry, 0, counts[mod])
		for _, entry := range m.Entries {
			if entry.Mod == mod {
				group = append(group, entry)
			}
		}
		for i := 1; i < len(group); i++ {
			for j := i; j > 0 && group[j].Index < group[j-1].Index; j-- {
				group[j], group[j-1] = group[j-1], group[j]
			}
		}
		sorted = append(sorted, group...)
	}
	return sorted
}

func entryField(index int, field string) string {
	return "entries[" + itoa(index) + "]." + field
}
