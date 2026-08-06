package matchengine

import "sort"

// PlacementOption is a server-derived legal PoolSlot/cell pairing.
type PlacementOption struct {
	PoolSlotID string    `json:"poolSlotId"`
	Cell       Cell      `json:"cell"`
	ForceMod   *ForceMod `json:"forceMod,omitempty"`
}

// Analysis is a deterministic, read-only view of currently possible play.
// It is shared by terminal evaluation and non-authoritative tooling.
type Analysis struct {
	SelectablePoolSlotIDs []string          `json:"selectablePoolSlotIds"`
	EmptyCells            []Cell            `json:"emptyCells"`
	LegalCellsByPoolSlot  map[string][]Cell `json:"legalCellsByPoolSlot"`
	LegalPlacements       []PlacementOption `json:"legalPlacements"`
	WonCounts             map[TeamSide]int  `json:"wonCounts"`
	Stalemate             bool              `json:"stalemate"`
}

// Analyze computes availability from state without mutating it.
func Analyze(state State) Analysis {
	analysis := Analysis{
		SelectablePoolSlotIDs: []string{},
		EmptyCells:            []Cell{},
		LegalCellsByPoolSlot:  make(map[string][]Cell),
		LegalPlacements:       []PlacementOption{},
		WonCounts:             map[TeamSide]int{TeamRed: 0, TeamBlue: 0},
	}
	for row := 0; row < 4; row++ {
		for column := 0; column < 4; column++ {
			cell := positionCell(column, row)
			if state.Board.empty(cell) {
				analysis.EmptyCells = append(analysis.EmptyCells, cell)
			}
		}
	}

	for _, piece := range state.Board.pieces {
		if piece.Outcome == OutcomeWon && piece.Owner != nil && piece.Owner.valid() {
			analysis.WonCounts[*piece.Owner]++
		}
	}

	for id, slot := range state.PoolSlots {
		if slot.State != PoolSlotAvailable || slot.Mod == ModTB {
			continue
		}
		analysis.SelectablePoolSlotIDs = append(analysis.SelectablePoolSlotIDs, id)
	}
	sort.Strings(analysis.SelectablePoolSlotIDs)

	for _, slotID := range analysis.SelectablePoolSlotIDs {
		slot := state.PoolSlots[slotID]
		for _, cell := range analysis.EmptyCells {
			if slot.Mod == ModShiro {
				analysis.LegalCellsByPoolSlot[slotID] = append(analysis.LegalCellsByPoolSlot[slotID], cell)
				analysis.LegalPlacements = append(analysis.LegalPlacements, PlacementOption{PoolSlotID: slotID, Cell: cell})
				continue
			}
			zone, _ := state.Board.ZoneAt(cell)
			forceMod, err := placementForceMod(slot.Mod, zone)
			if err != nil {
				continue
			}
			analysis.LegalCellsByPoolSlot[slotID] = append(analysis.LegalCellsByPoolSlot[slotID], cell)
			analysis.LegalPlacements = append(analysis.LegalPlacements, PlacementOption{
				PoolSlotID: slotID, Cell: cell, ForceMod: forceMod,
			})
		}
		if _, exists := analysis.LegalCellsByPoolSlot[slotID]; !exists {
			analysis.LegalCellsByPoolSlot[slotID] = []Cell{}
		}
	}

	analysis.Stalemate = len(analysis.SelectablePoolSlotIDs) == 0 || len(analysis.LegalPlacements) == 0
	return analysis
}
