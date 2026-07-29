package domain

import "sort"

// PlacementOption is a server-derived legal PoolSlot/cell pairing.
type PlacementOption struct {
	PoolSlotID string    `json:"poolSlotId" bson:"pool_slot_id"`
	Cell       Cell      `json:"cell" bson:"cell"`
	ForceMod   *ForceMod `json:"forceMod,omitempty" bson:"force_mod,omitempty"`
}

// Analysis is a deterministic, read-only view of currently possible play.
type Analysis struct {
	SelectablePoolSlotIDs []string          `json:"selectablePoolSlotIds" bson:"selectable_pool_slot_ids"`
	EmptyCells            []Cell            `json:"emptyCells" bson:"empty_cells"`
	LegalCellsByPoolSlot  map[string][]Cell `json:"legalCellsByPoolSlot" bson:"legal_cells_by_pool_slot"`
	LegalPlacements       []PlacementOption `json:"legalPlacements" bson:"legal_placements"`
	WonCounts             map[TeamSide]int  `json:"wonCounts" bson:"won_counts"`
	Stalemate             bool              `json:"stalemate" bson:"stalemate"`
	NoFourWithoutRobbery  bool              `json:"noFourWithoutRobbery" bson:"no_four_without_robbery"`
}

// Analyze computes availability from state without mutating it.
func Analyze(state State) Analysis {
	analysis := Analysis{
		SelectablePoolSlotIDs: []string{},
		EmptyCells:            []Cell{},
		LegalCellsByPoolSlot:  make(map[string][]Cell),
		LegalPlacements:       []PlacementOption{},
		WonCounts:             map[TeamSide]int{TeamSideRed: 0, TeamSideBlue: 0},
	}
	for row := 0; row < 4; row++ {
		for column := 0; column < 4; column++ {
			cell := PositionCell(column, row)
			if state.Board.Empty(cell) {
				analysis.EmptyCells = append(analysis.EmptyCells, cell)
			}
		}
	}

	for _, piece := range state.Board.pieces {
		if piece.Outcome == OutcomeWon && piece.Owner != nil && piece.Owner.Valid() {
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
	analysis.NoFourWithoutRobbery = !canEitherTeamStillFormFour(state, analysis)
	return analysis
}

func placementForceMod(mod Mod, zone Zone) (*ForceMod, error) {
	switch mod {
	case ModNM:
		return nil, nil
	case ModHD:
		if zone != ZoneHD {
			return nil, NewRuleError(CodeInvalidModZone, "HD requires an HD zone")
		}
		return nil, nil
	case ModHR:
		if zone != ZoneHR {
			return nil, NewRuleError(CodeInvalidModZone, "HR requires an HR zone")
		}
		return nil, nil
	case ModDT:
		if zone != ZoneDT {
			return nil, NewRuleError(CodeInvalidModZone, "DT requires a DT zone")
		}
		return nil, nil
	case ModFM:
		forceMod := ForceModNM
		switch zone {
		case ZoneHD:
			forceMod = ForceModHD
		case ZoneHR:
			forceMod = ForceModHR
		case ZoneDT:
			forceMod = ForceModNM
		}
		return &forceMod, nil
	default:
		return nil, NewRuleError(CodePoolSlotUnavailable, "slot mod cannot be placed as a normal beatmap")
	}
}

func canEitherTeamStillFormFour(state State, analysis Analysis) bool {
	for _, team := range []TeamSide{TeamSideRed, TeamSideBlue} {
		if canTeamStillFormFour(state, analysis, team) {
			return true
		}
	}
	return false
}

func canTeamStillFormFour(state State, analysis Analysis, team TeamSide) bool {
	directions := [][2]int{{1, 0}, {0, 1}, {1, 1}, {1, -1}}
	for row := 0; row < 4; row++ {
		for column := 0; column < 4; column++ {
			for _, direction := range directions {
				var required []Cell
				possible := true
				for offset := 0; offset < 4; offset++ {
					candidateColumn := column + direction[0]*offset
					candidateRow := row + direction[1]*offset
					if candidateColumn < 0 || candidateColumn >= 4 || candidateRow < 0 || candidateRow >= 4 {
						possible = false
						break
					}
					cell := PositionCell(candidateColumn, candidateRow)
					piece, occupied := state.Board.pieces[cell]
					if !occupied {
						required = append(required, cell)
						continue
					}
					if piece.Outcome != OutcomeWon || piece.Owner == nil || *piece.Owner != team {
						possible = false
						break
					}
				}
				if possible && distinctNormalSlotsCanFill(required, state, analysis) {
					return true
				}
			}
		}
	}
	return false
}

func distinctNormalSlotsCanFill(cells []Cell, state State, analysis Analysis) bool {
	var assign func(int, map[string]bool) bool
	assign = func(index int, visited map[string]bool) bool {
		if index == len(cells) {
			return true
		}
		cell := cells[index]
		for _, slotID := range analysis.SelectablePoolSlotIDs {
			slot := state.PoolSlots[slotID]
			if slot.Mod == ModShiro || visited[slotID] || !containsAnalysisCell(analysis.LegalCellsByPoolSlot[slotID], cell) {
				continue
			}
			visited[slotID] = true
			if assign(index+1, visited) {
				return true
			}
			delete(visited, slotID)
		}
		return false
	}
	return assign(0, make(map[string]bool))
}

func containsAnalysisCell(cells []Cell, wanted Cell) bool {
	for _, cell := range cells {
		if cell == wanted {
			return true
		}
	}
	return false
}
