package domain

// PieceMod represents the mod category used to organize mappool slots
// and determine board zone restrictions.
type PieceMod string

const (
	PieceModNM    PieceMod = "NM"
	PieceModHD    PieceMod = "HD"
	PieceModHR    PieceMod = "HR"
	PieceModDT    PieceMod = "DT"
	PieceModFM    PieceMod = "FM"
	PieceModShiro PieceMod = "Shiro"
	PieceModTB    PieceMod = "TB"
)

// PieceState represents the current state of a piece on the board or in the pool.
type PieceState string

const (
	PieceStateNormal PieceState = "normal" // default, selectable state
	PieceStateBanned PieceState = "banned" // banned during BP phase
	PieceStatePicked PieceState = "picked" // placed on the board
	PieceStateWon    PieceState = "won"    // won by a team, counts as a winning piece
	PieceStateDead   PieceState = "dead"   // sacrificed during a rob action
)

// ForceMod represents the mod that must be used when an FM piece lands in a zone.
type ForceMod string

const (
	ForceModHD ForceMod = "HD"
	ForceModHR ForceMod = "HR"
	ForceModNM ForceMod = "NM"
)

// Piece represents a single selectable beatmap slot in a match.
// A piece either references a Beatmap (normal case) or has no BeatmapID
// (Shiro). The mod category is determined by the mappool group it belongs to.
type Piece struct {
	BeatmapID *int64     `json:"beatmap_id,omitempty" bson:"beatmap_id,omitempty"` // nil means Shiro; -1 means removed
	State     PieceState `json:"state" bson:"state"`
	TeamID    *string    `json:"team_id,omitempty" bson:"team_id,omitempty"`     // owner when picked/won/dead
	ForceMod  *ForceMod  `json:"force_mod,omitempty" bson:"force_mod,omitempty"` // force mod chosen for FM pieces
	Position  *Position  `json:"position,omitempty" bson:"position,omitempty"`   // board position when picked
}

// IsRemoved reports whether the slot has been removed from the pool.
func (p Piece) IsRemoved() bool {
	return p.BeatmapID != nil && *p.BeatmapID == -1
}

// CanBeSelected reports whether the piece can be picked (i.e. not banned and not already picked).
func (p Piece) CanBeSelected() bool {
	return p.State != PieceStateBanned && p.State != PieceStatePicked && p.State != PieceStateWon && p.State != PieceStateDead
}

// IsRestrictedMod reports whether pieces of the given mod are constrained to a matching zone.
func IsRestrictedMod(mod PieceMod) bool {
	switch mod {
	case PieceModHD, PieceModHR, PieceModDT:
		return true
	default:
		return false
	}
}

// IsFreeMod reports whether pieces of the given mod can be placed in any zone.
func IsFreeMod(mod PieceMod) bool {
	return !IsRestrictedMod(mod)
}
