package domain

import (
	"encoding/json"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Cell is a canonical board coordinate from A1 through D4.
type Cell string

// Zone is one of the configured Mod regions on the board.
type Zone string

const (
	ZoneDT Zone = "DT"
	ZoneHD Zone = "HD"
	ZoneHR Zone = "HR"
)

// Board is a fixed 4x4 field. Empty cells are absent from pieces.
type Board struct {
	pieces map[Cell]BoardPiece
}

// Alignment is a deterministic connected line of WON pieces.
type Alignment struct {
	Length        int      `json:"length" bson:"length"`
	Team          TeamSide `json:"team" bson:"team"`
	Cells         []Cell   `json:"cells" bson:"cells"`
	BoardPieceIDs []string `json:"boardPieceIds" bson:"board_piece_ids"`
}

// MarshalJSON encodes the board as a map of cell→piece pairs.
func (b Board) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Pieces map[Cell]BoardPiece `json:"pieces"`
	}{Pieces: b.pieces})
}

// UnmarshalJSON decodes the board from a map of cell→piece pairs.
func (b *Board) UnmarshalJSON(data []byte) error {
	var encoded struct {
		Pieces map[Cell]BoardPiece `json:"pieces"`
	}
	if err := json.Unmarshal(data, &encoded); err != nil {
		return err
	}
	if encoded.Pieces == nil {
		encoded.Pieces = make(map[Cell]BoardPiece)
	}
	b.pieces = encoded.Pieces
	return nil
}

// MarshalBSON encodes the board for MongoDB storage.
func (b Board) MarshalBSON() ([]byte, error) {
	type boardDoc struct {
		Pieces map[Cell]BoardPiece `bson:"pieces"`
	}
	return bson.Marshal(boardDoc{Pieces: b.pieces})
}

// UnmarshalBSON decodes the board from a MongoDB document.
func (b *Board) UnmarshalBSON(data []byte) error {
	var doc struct {
		Pieces map[Cell]BoardPiece `bson:"pieces"`
	}
	if err := bson.Unmarshal(data, &doc); err != nil {
		return err
	}
	if doc.Pieces == nil {
		doc.Pieces = make(map[Cell]BoardPiece)
	}
	b.pieces = doc.Pieces
	return nil
}

// NewBoard creates an empty 4x4 board.
func NewBoard() Board {
	return Board{pieces: make(map[Cell]BoardPiece)}
}

// Clone returns an independent copy of the board.
func (b Board) Clone() Board {
	clone := NewBoard()
	for cell, piece := range b.pieces {
		clone.pieces[cell] = ClonePiece(piece)
	}
	return clone
}

// Pieces returns a copy of all pieces currently on the board.
func (b Board) Pieces() map[Cell]BoardPiece {
	result := make(map[Cell]BoardPiece, len(b.pieces))
	for cell, piece := range b.pieces {
		result[cell] = ClonePiece(piece)
	}
	return result
}

// ClonePiece returns an independent copy of a BoardPiece.
func ClonePiece(piece BoardPiece) BoardPiece {
	clone := piece
	if piece.ForceMod != nil {
		forceMod := *piece.ForceMod
		clone.ForceMod = &forceMod
	}
	if piece.Owner != nil {
		owner := *piece.Owner
		clone.Owner = &owner
	}
	return clone
}

// ZoneAt returns the confirmed RCTS1 quadrant for a canonical cell.
func (b Board) ZoneAt(cell Cell) (Zone, bool) {
	column, row, ok := CellPosition(cell)
	if !ok {
		return "", false
	}
	switch {
	case row < 2 && column < 2:
		return ZoneDT, true
	case row < 2:
		return ZoneHD, true
	case column < 2:
		return ZoneHR, true
	default:
		return ZoneDT, true
	}
}

// PieceAt returns a defensive copy of the piece occupying cell.
func (b Board) PieceAt(cell Cell) (BoardPiece, bool) {
	piece, ok := b.pieces[cell]
	if !ok {
		return BoardPiece{}, false
	}
	return ClonePiece(piece), true
}

// Empty reports whether the given cell is free.
func (b Board) Empty(cell Cell) bool {
	if _, _, ok := CellPosition(cell); !ok {
		return false
	}
	_, occupied := b.pieces[cell]
	return !occupied
}

// PlacePieceRaw places a piece at the given cell (raw engine operation).
func (b *Board) PlacePieceRaw(cell Cell, piece BoardPiece) {
	b.pieces[cell] = ClonePiece(piece)
}

// SetOwnerByPieceID sets the owner and outcome for the piece with the given ID.
func (b *Board) SetOwnerByPieceID(pieceID string, owner TeamSide) bool {
	for cell, piece := range b.pieces {
		if piece.ID != pieceID {
			continue
		}
		piece.Owner = &owner
		piece.Outcome = OutcomeWon
		b.pieces[cell] = piece
		return true
	}
	return false
}

// PieceByID returns the cell and piece for the given piece ID.
func (b Board) PieceByID(pieceID string) (Cell, BoardPiece, bool) {
	for cell, piece := range b.pieces {
		if piece.ID == pieceID {
			return cell, ClonePiece(piece), true
		}
	}
	return "", BoardPiece{}, false
}

// MarkDeadByIDs marks the given pieces as dead (sacrificed).
func (b *Board) MarkDeadByIDs(pieceIDs []string) {
	dead := make(map[string]struct{}, len(pieceIDs))
	for _, pieceID := range pieceIDs {
		dead[pieceID] = struct{}{}
	}
	for cell, piece := range b.pieces {
		if _, ok := dead[piece.ID]; !ok {
			continue
		}
		piece.Outcome = OutcomeDead
		b.pieces[cell] = piece
	}
}

// ContainsPieceID reports whether a piece with the given ID is on the board.
func (b Board) ContainsPieceID(pieceID string) bool {
	for _, piece := range b.pieces {
		if piece.ID == pieceID {
			return true
		}
	}
	return false
}

// HasFour reports whether the team has a four-in-a-row alignment.
func (b Board) HasFour(team TeamSide) bool {
	return len(b.FindAlignments(team, 4)) > 0
}

// FindAlignments returns connected lines of the requested length. Length two
// is horizontal/vertical only; length three and four also include diagonals.
// Only WON pieces owned by team participate.
func (b Board) FindAlignments(team TeamSide, length int) []Alignment {
	if !team.Valid() || length < 2 || length > 4 {
		return nil
	}

	directions := [][2]int{{1, 0}, {0, 1}, {1, 1}, {1, -1}}
	if length == 2 {
		directions = directions[:2]
	}
	var alignments []Alignment
	for row := 0; row < 4; row++ {
		for column := 0; column < 4; column++ {
			for _, direction := range directions {
				alignment := Alignment{Length: length, Team: team}
				for offset := 0; offset < length; offset++ {
					candidateColumn := column + direction[0]*offset
					candidateRow := row + direction[1]*offset
					if candidateColumn < 0 || candidateColumn >= 4 || candidateRow < 0 || candidateRow >= 4 {
						alignment.Cells = nil
						break
					}
					cell := PositionCell(candidateColumn, candidateRow)
					piece, ok := b.pieces[cell]
					if !ok || piece.Outcome != OutcomeWon || piece.Owner == nil || *piece.Owner != team {
						alignment.Cells = nil
						break
					}
					alignment.Cells = append(alignment.Cells, cell)
					alignment.BoardPieceIDs = append(alignment.BoardPieceIDs, piece.ID)
				}
				if len(alignment.Cells) == length {
					alignments = append(alignments, alignment)
				}
			}
		}
	}
	return alignments
}

// IsAlignment reports whether the given piece IDs form a valid alignment.
func (b Board) IsAlignment(team TeamSide, pieceIDs []string, length int) bool {
	if len(pieceIDs) != length {
		return false
	}
	wanted := make(map[string]struct{}, length)
	for _, pieceID := range pieceIDs {
		if pieceID == "" {
			return false
		}
		wanted[pieceID] = struct{}{}
	}
	if len(wanted) != length {
		return false
	}
	for _, alignment := range b.FindAlignments(team, length) {
		matched := true
		for _, pieceID := range alignment.BoardPieceIDs {
			if _, ok := wanted[pieceID]; !ok {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// AllOwnWon reports whether all given pieces are owned and won by team.
func (b Board) AllOwnWon(team TeamSide, pieceIDs []string) bool {
	for _, pieceID := range pieceIDs {
		_, piece, ok := b.PieceByID(pieceID)
		if !ok || piece.Outcome != OutcomeWon || piece.Owner == nil || *piece.Owner != team {
			return false
		}
	}
	return true
}

// CellPosition decodes a Cell string into column (0-3) and row (0-3).
func CellPosition(cell Cell) (column int, row int, ok bool) {
	if len(cell) != 2 {
		return 0, 0, false
	}
	column = int(cell[0] - 'A')
	row = int(cell[1] - '1')
	if column < 0 || column >= 4 || row < 0 || row >= 4 {
		return 0, 0, false
	}
	return column, row, true
}

// PositionCell encodes column and row into a Cell string.
func PositionCell(column, row int) Cell {
	return Cell([]byte{byte('A' + column), byte('1' + row)})
}

// =============================================================================
// Public board operations (adapter layer for service/move consumers)
// =============================================================================

// CellFromPosition converts a Position to a Cell string, or "" if out of bounds.
func CellFromPosition(p Position) Cell {
	return PositionCell(p.X, p.Y)
}

// CellToPosition converts a Cell string to a Position.
func CellToPosition(c Cell) Position {
	col, row, ok := CellPosition(c)
	if !ok {
		return Position{}
	}
	return Position{X: col, Y: row}
}

// Place puts a piece onto the board at the given position.
func (b *Board) Place(mod Mod, position Position, pieceID string, teamID TeamSide) bool {
	cell := CellFromPosition(position)
	if cell == "" || !b.Empty(cell) {
		return false
	}
	if IsRestrictedMod(mod) {
		zone, _ := b.ZoneAt(cell)
		if Zone(mod) != zone {
			return false
		}
	}
	owner := teamID
	b.PlacePieceRaw(cell, BoardPiece{
		ID:         pieceID,
		Mod:        mod,
		SelectedBy: teamID,
		Owner:      &owner,
		Outcome:    OutcomeWaitingResult,
	})
	return true
}

// Remove clears the piece at the given position.
func (b *Board) Remove(position Position) bool {
	cell := CellFromPosition(position)
	if cell == "" || b.Empty(cell) {
		return false
	}
	delete(b.pieces, cell)
	return true
}

// FindByPieceID returns the position of the piece with the given ID.
func (b Board) FindByPieceID(pieceID string) (Position, bool) {
	cell, _, ok := b.PieceByID(pieceID)
	if !ok {
		return Position{}, false
	}
	return CellToPosition(cell), true
}

// IsOccupied reports whether a piece occupies the given position.
func (b Board) IsOccupied(position Position) bool {
	cell := CellFromPosition(position)
	if cell == "" {
		return false
	}
	return !b.Empty(cell)
}

// PieceAtPosition returns the BoardPiece at the given position.
func (b Board) PieceAtPosition(position Position) (BoardPiece, bool) {
	cell := CellFromPosition(position)
	if cell == "" {
		return BoardPiece{}, false
	}
	return b.PieceAt(cell)
}

// WonCounts returns the number of WON pieces per team.
func (b Board) WonCounts() map[TeamSide]int {
	counts := map[TeamSide]int{TeamSideRed: 0, TeamSideBlue: 0}
	for _, piece := range b.pieces {
		if piece.Outcome == OutcomeWon && piece.Owner != nil && piece.Owner.Valid() {
			counts[*piece.Owner]++
		}
	}
	return counts
}

// CountWonPieces returns won piece counts per team (alias for WonCounts).
func (b Board) CountWonPieces() map[TeamSide]int {
	return b.WonCounts()
}

// TransferOwnership transfers a piece at the given position to a new team.
func (b *Board) TransferOwnership(position Position, newTeam TeamSide) bool {
	cell := CellFromPosition(position)
	piece, ok := b.pieces[cell]
	if !ok {
		return false
	}
	piece.Owner = &newTeam
	b.pieces[cell] = piece
	return true
}

// ClearCell removes the piece at the given position, if any.
func (b *Board) ClearCell(position Position) bool {
	cell := CellFromPosition(position)
	if _, ok := b.pieces[cell]; !ok {
		return false
	}
	delete(b.pieces, cell)
	return true
}

// SetOwner sets the outcome to WON for the piece at the given position.
func (b *Board) SetOwner(position Position, owner TeamSide) bool {
	cell := CellFromPosition(position)
	piece, ok := b.pieces[cell]
	if !ok {
		return false
	}
	piece.Owner = &owner
	piece.Outcome = OutcomeWon
	b.pieces[cell] = piece
	return true
}
