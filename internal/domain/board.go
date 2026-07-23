package domain

// CellState represents the occupancy state of a board cell.
type CellState string

const (
	CellStateEmpty    CellState = "empty"
	CellStateOccupied CellState = "occupied"
)

// Zone represents a mod-restricted area on the board.
// The standard board is 4x4; zones are 2x2 corners marked by a mod.
type Zone string

const (
	ZoneHD Zone = "HD"
	ZoneHR Zone = "HR"
	ZoneDT Zone = "DT"
	ZoneNM Zone = "NM" // free/no restriction zone, also used for the center area
)

// BoardSize defines the standard RCT board dimensions.
const (
	BoardRows = 4
	BoardCols = 4
)

// Cell is a single square on the board.
type Cell struct {
	Position Position  `json:"position" bson:"position"`
	Zone     Zone      `json:"zone" bson:"zone"`
	State    CellState `json:"state" bson:"state"`
	PieceID  *string   `json:"piece_id,omitempty" bson:"piece_id,omitempty"` // reference to piece mod+slot, e.g. "NM-1"
	TeamID   *string   `json:"team_id,omitempty" bson:"team_id,omitempty"`   // team that owns the cell
}

// NewBoard creates a standard 4x4 RCT board with mod zones:
//
//	A B C D
//
// 1 H H D D
// 2 H H D D
// 3 R R N/F N/F
// 4 R R N/F N/F
func NewBoard() Board {
	b := Board{
		Rows:  BoardRows,
		Cols:  BoardCols,
		Cells: make([][]Cell, BoardRows),
	}
	zones := [BoardRows][BoardCols]Zone{
		{ZoneHD, ZoneHD, ZoneDT, ZoneDT},
		{ZoneHD, ZoneHD, ZoneDT, ZoneDT},
		{ZoneHR, ZoneHR, ZoneNM, ZoneNM},
		{ZoneHR, ZoneHR, ZoneNM, ZoneNM},
	}
	for y := range BoardRows {
		b.Cells[y] = make([]Cell, BoardCols)
		for x := range BoardCols {
			b.Cells[y][x] = Cell{
				Position: Position{X: x, Y: y},
				Zone:     zones[y][x],
				State:    CellStateEmpty,
			}
		}
	}
	return b
}

// Board represents the 4x4 RCT playing field.
type Board struct {
	Rows  int      `json:"rows" bson:"rows"`
	Cols  int      `json:"cols" bson:"cols"`
	Cells [][]Cell `json:"cells" bson:"cells"`
}

// CellAt returns the cell at the given position, or nil if out of bounds.
func (b *Board) CellAt(p Position) *Cell {
	if p.Y < 0 || p.Y >= b.Rows || p.X < 0 || p.X >= b.Cols {
		return nil
	}
	return &b.Cells[p.Y][p.X]
}

// IsEmpty reports whether the cell at p is empty.
func (b *Board) IsEmpty(p Position) bool {
	c := b.CellAt(p)
	return c != nil && c.State == CellStateEmpty
}

// CanPlace reports whether a piece of the given mod can be placed at p according to zone rules.
func (b *Board) CanPlace(mod PieceMod, p Position) bool {
	c := b.CellAt(p)
	if c == nil || c.State != CellStateEmpty {
		return false
	}
	if IsFreeMod(mod) {
		return true
	}
	return Zone(mod) == c.Zone
}

// Place puts a piece onto the board and returns the updated cell.
func (b *Board) Place(mod PieceMod, p Position, pieceID string, teamID string) bool {
	if !b.CanPlace(mod, p) {
		return false
	}
	c := &b.Cells[p.Y][p.X]
	c.State = CellStateOccupied
	c.PieceID = &pieceID
	c.TeamID = &teamID
	return true
}

// Remove clears the cell at p.
func (b *Board) Remove(p Position) bool {
	c := b.CellAt(p)
	if c == nil || c.State == CellStateEmpty {
		return false
	}
	c.State = CellStateEmpty
	c.PieceID = nil
	c.TeamID = nil
	return true
}

// WinningAlignment describes a connected line that satisfies a win condition.
type WinningAlignment struct {
	Length    int        `json:"length" bson:"length"`
	Positions []Position `json:"positions" bson:"positions"`
	TeamID    string     `json:"team_id" bson:"team_id"`
}

// FindAlignments returns all connected alignments of the given length for a team.
// length 2 only checks horizontal/vertical adjacency.
// length 3+ checks horizontal, vertical, and both diagonal directions.
func (b *Board) FindAlignments(teamID string, length int) []WinningAlignment {
	var alignments []WinningAlignment
	if length < 2 {
		return alignments
	}

	directions := [][2]int{
		{1, 0}, // horizontal
		{0, 1}, // vertical
	}
	if length >= 3 {
		directions = append(directions, [2]int{1, 1}, [2]int{1, -1}) // diagonals
	}

	for y := 0; y < b.Rows; y++ {
		for x := 0; x < b.Cols; x++ {
			start := Position{X: x, Y: y}
			cell := b.CellAt(start)
			if cell == nil || cell.TeamID == nil || *cell.TeamID != teamID {
				continue
			}
			for _, d := range directions {
				positions := []Position{start}
				for i := 1; i < length; i++ {
					np := Position{X: x + d[0]*i, Y: y + d[1]*i}
					nc := b.CellAt(np)
					if nc == nil || nc.TeamID == nil || *nc.TeamID != teamID {
						break
					}
					positions = append(positions, np)
				}
				if len(positions) == length {
					alignments = append(alignments, WinningAlignment{
						Length:    length,
						Positions: positions,
						TeamID:    teamID,
					})
				}
			}
		}
	}
	return alignments
}

// HasFourInARow reports whether the team has formed a four-in-a-row alignment.
func (b *Board) HasFourInARow(teamID string) bool {
	return len(b.FindAlignments(teamID, 4)) > 0
}
