package matchengine

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestAnalyzeSeparatesSelectableSlotsFromLegalPlacements(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	state.PoolSlots["HD1"] = PoolSlot{ID: "HD1", Mod: ModHD, State: PoolSlotAvailable}
	for _, cell := range []Cell{"C1", "D1", "C2", "D2"} {
		placeFixturePiece(&state, string(cell), cell, TeamRed, OutcomeDead)
	}

	analysis := Analyze(state)
	if !containsSlot(analysis.SelectablePoolSlotIDs, "HD1") {
		t.Fatalf("HD1 missing from selectable slots: %v", analysis.SelectablePoolSlotIDs)
	}
	if cells := analysis.LegalCellsByPoolSlot["HD1"]; len(cells) != 0 {
		t.Fatalf("HD1 legal cells = %v, want none", cells)
	}
	if cells := analysis.LegalCellsByPoolSlot["FM1"]; len(cells) == 0 {
		t.Fatal("FM1 should remain legal outside the occupied HD zone")
	}
}

func TestAnalyzeTreatsDeadCellsAsOccupiedAndMapsFMInDTToNM(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	placeFixturePiece(&state, "dead-a1", "A1", TeamRed, OutcomeDead)
	analysis := Analyze(state)

	if containsCell(analysis.EmptyCells, "A1") {
		t.Fatal("dead A1 was reported as empty")
	}
	option, ok := findPlacement(analysis, "FM1", "B1")
	if !ok || option.ForceMod == nil || *option.ForceMod != ForceModNM {
		t.Fatalf("FM1/B1 placement = %+v, want NM Force Mod", option)
	}
}

func TestAnalyzeUsesEmptyCollectionsForFullBoard(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	fillBoardForStalemate(&state, 8)
	analysis := Analyze(state)

	if analysis.EmptyCells == nil || len(analysis.EmptyCells) != 0 {
		t.Fatalf("empty cells = %#v, want non-nil empty collection", analysis.EmptyCells)
	}
	if analysis.LegalPlacements == nil || len(analysis.LegalPlacements) != 0 {
		t.Fatalf("legal placements = %#v, want non-nil empty collection", analysis.LegalPlacements)
	}
	encoded, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || bytes.Contains(encoded, []byte(`"emptyCells":null`)) || bytes.Contains(encoded, []byte(`"legalPlacements":null`)) {
		t.Fatalf("full-board analysis contains null collections: %s", encoded)
	}
}

func TestPickEntryStalemateWithUnequalWonCountsFinishesMatch(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	fillBoardForStalemate(&state, 9)
	state.Phase = PhaseWaitingForResult
	state.PendingPieceID = "RED-9"
	pieceCell, piece, ok := state.Board.pieceByID("RED-9")
	if !ok {
		t.Fatal("pending fixture piece missing")
	}
	piece.Outcome = OutcomeWaitingResult
	piece.Owner = nil
	state.Board.pieces[pieceCell] = piece

	transition := mustExecute(t, state, RefereeActor(), ConfirmBeatmapResult{
		BoardPieceID: "RED-9", WinningTeam: TeamRed,
	}, testStart.Add(time.Minute))
	assertTerminalResult(t, transition.State, TeamRed, ResultReasonStalemateWonCount)
	if transition.State.Result.RedWonCount != 9 || transition.State.Result.BlueWonCount != 7 {
		t.Fatalf("stalemate counts = %+v", transition.State.Result)
	}
	assertEventTypes(t, transition.Events,
		EventBeatmapResultConfirmed, EventPieceWon, EventTurnAdvanced, EventStalemateDetected, EventMatchFinished)
}

func TestPickEntryStalemateWithEqualWonCountsRequiresAdjudication(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	fillBoardForStalemate(&state, 8)
	state.Phase = PhaseWaitingForResult
	state.PendingPieceID = "RED-8"
	pieceCell, piece, ok := state.Board.pieceByID("RED-8")
	if !ok {
		t.Fatal("pending fixture piece missing")
	}
	piece.Outcome = OutcomeWaitingResult
	piece.Owner = nil
	state.Board.pieces[pieceCell] = piece

	transition := mustExecute(t, state, RefereeActor(), ConfirmBeatmapResult{
		BoardPieceID: "RED-8", WinningTeam: TeamRed,
	}, testStart.Add(time.Minute))
	got := transition.State
	if got.Lifecycle != LifecycleAdjudicationRequired || got.Phase != PhaseNone || got.Winner != nil || got.Result != nil {
		t.Fatalf("equal stalemate state = lifecycle %q phase %q winner %v result %+v", got.Lifecycle, got.Phase, got.Winner, got.Result)
	}
	if got.Stalemate == nil || got.Stalemate.RedWonCount != 8 || got.Stalemate.BlueWonCount != 8 {
		t.Fatalf("stalemate evidence = %+v", got.Stalemate)
	}
	assertEventTypes(t, transition.Events,
		EventBeatmapResultConfirmed, EventPieceWon, EventTurnAdvanced, EventStalemateDetected, EventAdjudicationRequired)

	before := got.Clone()
	_, err := Execute(got, RefereeActor(), ConfirmTBResult{WinningTeam: TeamRed}, testStart.Add(2*time.Minute))
	assertErrorCode(t, err, CodeMatchLifecycleConflict)
	if !reflect.DeepEqual(got, before) {
		t.Fatal("rejected write mutated adjudication state")
	}
}

func TestShiroPlacementDetectsPoolExhaustionAtNextPick(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	for id, slot := range state.PoolSlots {
		if slot.Mod != ModShiro && slot.Mod != ModTB {
			slot.State = PoolSlotSelected
			state.PoolSlots[id] = slot
		}
	}
	placeFixturePiece(&state, "red", "A1", TeamRed, OutcomeWon)

	transition := mustExecute(t, state, StrategistActor(TeamBlue), PlaceShiro{
		PieceID: "shiro", Cell: "B1",
	}, testStart.Add(5*time.Second))
	if transition.State.Lifecycle != LifecycleFinished || transition.State.Winner == nil || *transition.State.Winner != TeamRed {
		t.Fatalf("pool exhaustion did not finish for RED: %+v", transition.State)
	}
	assertEventTypes(t, transition.Events, EventShiroPlaced, EventTurnAdvanced, EventStalemateDetected, EventMatchFinished)
}

func TestEqualStalemateJSONRecoveryPreservesClosedBehavior(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	fillBoardForStalemate(&state, 8)
	state.Phase = PhaseWaitingForResult
	state.PendingPieceID = "RED-8"
	cell, piece, _ := state.Board.pieceByID("RED-8")
	piece.Outcome, piece.Owner = OutcomeWaitingResult, nil
	state.Board.pieces[cell] = piece
	state = mustExecute(t, state, RefereeActor(), ConfirmBeatmapResult{
		BoardPieceID: "RED-8", WinningTeam: TeamRed,
	}, testStart.Add(time.Minute)).State

	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var restored State
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(restored, state) {
		t.Fatalf("restored adjudication state differs\n got: %#v\nwant: %#v", restored, state)
	}
	_, err = Execute(restored, RefereeActor(), StartTB{}, testStart.Add(2*time.Minute))
	assertErrorCode(t, err, CodeMatchLifecycleConflict)
}

func fillBoardForStalemate(state *State, redCount int) {
	redCells := map[Cell]bool{
		"A1": true, "B1": true, "C2": true, "D2": true,
		"A3": true, "C3": true, "B4": true, "D4": true,
	}
	if redCount == 9 {
		redCells["D1"] = true
	}
	redOrdinal, blueOrdinal := 0, 0
	for row := 1; row <= 4; row++ {
		for column := 'A'; column <= 'D'; column++ {
			cell := Cell(string(column) + string(rune('0'+row)))
			team := TeamBlue
			blueOrdinal++
			ordinal := blueOrdinal
			if redCells[cell] {
				team = TeamRed
				redOrdinal++
				ordinal = redOrdinal
				blueOrdinal--
			}
			id := string(team) + "-" + string(rune('0'+ordinal))
			placeFixturePiece(state, id, cell, team, OutcomeWon)
		}
	}
}

func placeFixturePiece(state *State, id string, cell Cell, owner TeamSide, outcome Outcome) {
	seedPiece(&state.Board, cell, id, ModNM, outcome, team(owner))
}

func containsSlot(slots []string, wanted string) bool {
	for _, slot := range slots {
		if slot == wanted {
			return true
		}
	}
	return false
}

func containsCell(cells []Cell, wanted Cell) bool {
	for _, cell := range cells {
		if cell == wanted {
			return true
		}
	}
	return false
}

func findPlacement(analysis Analysis, slotID string, cell Cell) (PlacementOption, bool) {
	for _, option := range analysis.LegalPlacements {
		if option.PoolSlotID == slotID && option.Cell == cell {
			return option, true
		}
	}
	return PlacementOption{}, false
}
