package matchengine

import "encoding/json"

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
	Length        int      `json:"length"`
	Team          TeamSide `json:"team"`
	Cells         []Cell   `json:"cells"`
	BoardPieceIDs []string `json:"boardPieceIds"`
}

func (b Board) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Pieces map[Cell]BoardPiece `json:"pieces"`
	}{Pieces: b.pieces})
}

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

func NewBoard() Board {
	return Board{pieces: make(map[Cell]BoardPiece)}
}

func (b Board) Clone() Board {
	clone := NewBoard()
	for cell, piece := range b.pieces {
		clone.pieces[cell] = clonePiece(piece)
	}
	return clone
}

func clonePiece(piece BoardPiece) BoardPiece {
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
	column, row, ok := cellPosition(cell)
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
	return clonePiece(piece), true
}

func (b Board) empty(cell Cell) bool {
	if _, _, ok := cellPosition(cell); !ok {
		return false
	}
	_, occupied := b.pieces[cell]
	return !occupied
}

func (b *Board) place(cell Cell, piece BoardPiece) {
	b.pieces[cell] = clonePiece(piece)
}

func (b *Board) setOwner(pieceID string, owner TeamSide) bool {
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

func (b Board) pieceByID(pieceID string) (Cell, BoardPiece, bool) {
	for cell, piece := range b.pieces {
		if piece.ID == pieceID {
			return cell, clonePiece(piece), true
		}
	}
	return "", BoardPiece{}, false
}

func (b *Board) markDead(pieceIDs []string) {
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

func (b Board) containsPieceID(pieceID string) bool {
	for _, piece := range b.pieces {
		if piece.ID == pieceID {
			return true
		}
	}
	return false
}

func (b Board) hasFour(team TeamSide) bool {
	return len(b.FindAlignments(team, 4)) > 0
}

// FindAlignments returns connected lines of the requested length. Length two
// is horizontal/vertical only; length three and four also include diagonals.
// Only WON pieces owned by team participate.
func (b Board) FindAlignments(team TeamSide, length int) []Alignment {
	if !team.valid() || length < 2 || length > 4 {
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
					cell := positionCell(candidateColumn, candidateRow)
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

func (b Board) isAlignment(team TeamSide, pieceIDs []string, length int) bool {
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

func (b Board) pieceParticipatesInAlignment(team TeamSide, pieceID string, length int) bool {
	if pieceID == "" {
		return false
	}
	for _, alignment := range b.FindAlignments(team, length) {
		for _, candidate := range alignment.BoardPieceIDs {
			if candidate == pieceID {
				return true
			}
		}
	}
	return false
}

func cellPosition(cell Cell) (column int, row int, ok bool) {
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

func positionCell(column, row int) Cell {
	return Cell([]byte{byte('A' + column), byte('1' + row)})
}
