package matchengine

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestPlaceShiroIsUnownedAndImmediatelyAdvancesTurn(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	transition := mustExecute(t, state, StrategistActor(TeamBlue), PlaceShiro{
		PieceID: "shiro-piece",
		Cell:    "A1",
	}, testStart.Add(5*time.Second))
	state = transition.State

	piece, ok := state.Board.PieceAt("A1")
	if !ok {
		t.Fatal("Shiro is absent from A1")
	}
	if piece.ID != "shiro-piece" || piece.Mod != ModShiro || piece.SourcePoolSlotID != "SHIRO" {
		t.Fatalf("unexpected Shiro piece: %+v", piece)
	}
	if piece.Owner != nil || piece.Outcome != OutcomeWhite {
		t.Fatalf("placed Shiro = %+v, want unowned WHITE", piece)
	}
	if state.PoolSlots["SHIRO"].State != PoolSlotSelected {
		t.Fatalf("Shiro pool state = %q, want selected", state.PoolSlots["SHIRO"].State)
	}
	assertStateHeader(t, state, LifecycleRunning, PhasePick, 2, TeamRed)
	assertEventTypes(t, transition.Events, EventShiroPlaced, EventTurnAdvanced, EventTimerStarted)
	assertTimer(t, state.Timer, testStart.Add(5*time.Second), PickDuration)
}

func TestPlaceShiroRejectsIllegalCommandsWithoutMutation(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	tests := []struct {
		name    string
		actor   Actor
		command PlaceShiro
		now     time.Time
		code    ErrorCode
	}{
		{name: "wrong team", actor: StrategistActor(TeamRed), command: PlaceShiro{PieceID: "shiro-piece", Cell: "A1"}, now: testStart.Add(5 * time.Second), code: CodeNotActiveTeam},
		{name: "expired timer", actor: StrategistActor(TeamBlue), command: PlaceShiro{PieceID: "shiro-piece", Cell: "A1"}, now: testStart.Add(4*time.Second + PickDuration), code: CodeTimerExpired},
		{name: "invalid cell", actor: StrategistActor(TeamBlue), command: PlaceShiro{PieceID: "shiro-piece", Cell: "E1"}, now: testStart.Add(5 * time.Second), code: CodeInvalidBoardCell},
		{name: "empty piece id", actor: StrategistActor(TeamBlue), command: PlaceShiro{Cell: "A1"}, now: testStart.Add(5 * time.Second), code: CodeInvalidRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := state.Clone()
			_, err := Execute(state, tt.actor, tt.command, tt.now)
			assertErrorCode(t, err, tt.code)
			if !reflect.DeepEqual(state, before) {
				t.Fatal("failed Shiro placement mutated input")
			}
		})
	}

	placed := mustExecute(t, state, StrategistActor(TeamBlue), PlaceShiro{PieceID: "shiro-piece", Cell: "A1"}, testStart.Add(5*time.Second)).State
	before := placed.Clone()
	_, err := Execute(placed, StrategistActor(TeamRed), PlaceShiro{PieceID: "second-shiro", Cell: "B1"}, testStart.Add(6*time.Second))
	assertErrorCode(t, err, CodePoolSlotUnavailable)
	if !reflect.DeepEqual(placed, before) {
		t.Fatal("second Shiro placement mutated input")
	}
}

func TestRobberyRequiresActiveStrategistPickPhaseAndLiveTimer(t *testing.T) {
	t.Parallel()

	state := robberyState(t)
	seedPiece(&state.Board, "A1", "blue-1", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "B1", "blue-2", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "C1", "blue-3", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "D2", "blue-anchor-1", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "D3", "blue-anchor-2", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "D4", "red-target", ModNM, OutcomeWon, team(TeamRed))
	command := RobPiece{TargetPieceID: "red-target", SacrificeSets: [][]string{{"blue-1", "blue-2", "blue-3"}}}

	tests := []struct {
		name  string
		state State
		actor Actor
		now   time.Time
		code  ErrorCode
	}{
		{name: "wrong team", state: state, actor: StrategistActor(TeamRed), now: testStart.Add(5 * time.Second), code: CodeNotActiveTeam},
		{name: "referee", state: state, actor: RefereeActor(), now: testStart.Add(5 * time.Second), code: CodeActionNotAllowed},
		{name: "expired timer", state: state, actor: StrategistActor(TeamBlue), now: testStart.Add(4*time.Second + PickDuration), code: CodeTimerExpired},
	}
	waiting := state.Clone()
	waiting.Phase = PhaseWaitingForResult
	tests = append(tests, struct {
		name  string
		state State
		actor Actor
		now   time.Time
		code  ErrorCode
	}{name: "wrong phase", state: waiting, actor: StrategistActor(TeamBlue), now: testStart.Add(5 * time.Second), code: CodeMatchPhaseConflict})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.state.Clone()
			_, err := Execute(tt.state, tt.actor, command, tt.now)
			assertErrorCode(t, err, tt.code)
			if !reflect.DeepEqual(tt.state, before) {
				t.Fatal("failed robbery mutated input")
			}
		})
	}
}

func TestAlignmentDirectionsUseWonPiecesOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		cells  []Cell
		length int
		want   int
	}{
		{name: "two horizontal", cells: []Cell{"A1", "B1"}, length: 2, want: 1},
		{name: "two vertical", cells: []Cell{"A1", "A2"}, length: 2, want: 1},
		{name: "two diagonal is invalid", cells: []Cell{"A1", "B2"}, length: 2, want: 0},
		{name: "three horizontal", cells: []Cell{"A1", "B1", "C1"}, length: 3, want: 1},
		{name: "three vertical", cells: []Cell{"A1", "A2", "A3"}, length: 3, want: 1},
		{name: "three descending diagonal", cells: []Cell{"A1", "B2", "C3"}, length: 3, want: 1},
		{name: "three ascending diagonal", cells: []Cell{"A3", "B2", "C1"}, length: 3, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			board := NewBoard()
			for i, cell := range tt.cells {
				seedPiece(&board, cell, "piece-"+string(rune('1'+i)), ModNM, OutcomeWon, team(TeamBlue))
			}
			if got := len(board.FindAlignments(TeamBlue, tt.length)); got != tt.want {
				t.Fatalf("alignment count = %d, want %d", got, tt.want)
			}
		})
	}

	board := NewBoard()
	seedPiece(&board, "A1", "won", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&board, "B1", "waiting", ModNM, OutcomeWaitingResult, nil)
	seedPiece(&board, "C1", "dead", ModNM, OutcomeDead, team(TeamBlue))
	seedPiece(&board, "D1", "white", ModShiro, OutcomeWhite, nil)
	if got := len(board.FindAlignments(TeamBlue, 2)); got != 0 {
		t.Fatalf("non-WON pieces formed %d alignments", got)
	}
}

func TestRobOpponentWithOneThreeAlignmentIsAtomicAndKeepsPick(t *testing.T) {
	t.Parallel()

	state := robberyState(t)
	seedPiece(&state.Board, "A1", "blue-1", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "B1", "blue-2", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "C1", "blue-3", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "D2", "blue-anchor-1", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "D3", "blue-anchor-2", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "D4", "red-target", ModNM, OutcomeWon, team(TeamRed))
	beforeTimer := state.Timer

	transition := mustExecute(t, state, StrategistActor(TeamBlue), RobPiece{
		TargetPieceID: "red-target",
		SacrificeSets: [][]string{{"blue-1", "blue-2", "blue-3"}},
	}, testStart.Add(5*time.Second))
	state = transition.State

	for _, cell := range []Cell{"A1", "B1", "C1"} {
		piece, ok := state.Board.PieceAt(cell)
		if !ok || piece.Outcome != OutcomeDead || piece.Owner == nil || *piece.Owner != TeamBlue {
			t.Fatalf("sacrifice at %s = %+v, want retained BLUE DEAD piece", cell, piece)
		}
	}
	target, _ := state.Board.PieceAt("D4")
	if target.Outcome != OutcomeWon || target.Owner == nil || *target.Owner != TeamBlue {
		t.Fatalf("robbed target = %+v, want BLUE WON", target)
	}
	if !state.RobberyUsed[TeamBlue] || state.RobberyUsed[TeamRed] {
		t.Fatalf("robbery usage = %#v, want BLUE only", state.RobberyUsed)
	}
	assertStateHeader(t, state, LifecycleRunning, PhasePick, 1, TeamBlue)
	if state.Timer != beforeTimer {
		t.Fatalf("robbery reset combined Pick timer: got %+v want %+v", state.Timer, beforeTimer)
	}
	if !state.Board.pieceParticipatesInAlignment(TeamBlue, "red-target", 3) {
		t.Fatal("robbed target does not participate in the required final three-alignment")
	}
	assertEventTypes(t, transition.Events, EventPiecesSacrificed, EventPieceRobbed)
}

func TestRobOpponentWithTwoDistinctTwoAlignments(t *testing.T) {
	t.Parallel()

	state := robberyState(t)
	seedPiece(&state.Board, "A1", "blue-1", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "B1", "blue-2", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "A3", "blue-3", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "B3", "blue-4", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "D2", "blue-anchor-1", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "D3", "blue-anchor-2", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "D4", "red-target", ModNM, OutcomeWon, team(TeamRed))

	transition := mustExecute(t, state, StrategistActor(TeamBlue), RobPiece{
		TargetPieceID: "red-target",
		SacrificeSets: [][]string{{"blue-1", "blue-2"}, {"blue-3", "blue-4"}},
	}, testStart.Add(5*time.Second))
	target, _ := transition.State.Board.PieceAt("D4")
	if target.Owner == nil || *target.Owner != TeamBlue {
		t.Fatalf("target owner = %v, want BLUE", target.Owner)
	}
}

func TestRobShiroWithOneTwoAlignmentAndRequiredTargetParticipation(t *testing.T) {
	t.Parallel()

	state := robberyState(t)
	seedPiece(&state.Board, "A1", "blue-1", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "B1", "blue-2", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "B2", "shiro-piece", ModShiro, OutcomeWhite, nil)
	seedPiece(&state.Board, "B3", "blue-anchor", ModNM, OutcomeWon, team(TeamBlue))

	transition := mustExecute(t, state, StrategistActor(TeamBlue), RobPiece{
		TargetPieceID: "shiro-piece",
		SacrificeSets: [][]string{{"blue-1", "blue-2"}},
	}, testStart.Add(5*time.Second))
	state = transition.State

	shiro, _ := state.Board.PieceAt("B2")
	if shiro.Outcome != OutcomeWon || shiro.Owner == nil || *shiro.Owner != TeamBlue {
		t.Fatalf("robbed Shiro = %+v, want BLUE WON", shiro)
	}
	for _, cell := range []Cell{"A1", "B1"} {
		piece, ok := state.Board.PieceAt(cell)
		if !ok || piece.Outcome != OutcomeDead {
			t.Fatalf("Shiro sacrifice at %s = %+v, want retained DEAD", cell, piece)
		}
	}
}

func TestRobberyTargetParticipationIsCheckedAfterSacrifices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		targetCell Cell
		targetMod  Mod
		outcome    Outcome
		owner      *TeamSide
		sacrifices [][]string
	}{
		{
			name: "opponent target only aligns with pieces being sacrificed", targetCell: "D1",
			targetMod: ModNM, outcome: OutcomeWon, owner: team(TeamRed),
			sacrifices: [][]string{{"blue-1", "blue-2", "blue-3"}},
		},
		{
			name: "Shiro only aligns with a piece being sacrificed", targetCell: "C1",
			targetMod: ModShiro, outcome: OutcomeWhite, owner: nil,
			sacrifices: [][]string{{"blue-1", "blue-2"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := robberyState(t)
			seedPiece(&state.Board, "A1", "blue-1", ModNM, OutcomeWon, team(TeamBlue))
			seedPiece(&state.Board, "B1", "blue-2", ModNM, OutcomeWon, team(TeamBlue))
			seedPiece(&state.Board, "C1", "blue-3", ModNM, OutcomeWon, team(TeamBlue))
			seedPiece(&state.Board, tt.targetCell, "target", tt.targetMod, tt.outcome, tt.owner)
			before := state.Clone()

			_, err := Execute(state, StrategistActor(TeamBlue), RobPiece{
				TargetPieceID: "target", SacrificeSets: tt.sacrifices,
			}, testStart.Add(5*time.Second))
			assertErrorCode(t, err, CodeRobberyRequirementsNotMet)
			if !reflect.DeepEqual(state, before) {
				t.Fatal("rejected robbery mutated input")
			}
		})
	}
}

func TestRobberyRejectsInvalidSacrificesAndTargetsWithoutMutation(t *testing.T) {
	t.Parallel()

	base := robberyState(t)
	seedPiece(&base.Board, "A1", "blue-1", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&base.Board, "B1", "blue-2", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&base.Board, "C1", "blue-3", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&base.Board, "A3", "blue-4", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&base.Board, "B3", "blue-5", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&base.Board, "D4", "red-target", ModNM, OutcomeWon, team(TeamRed))
	seedPiece(&base.Board, "D3", "blue-target", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&base.Board, "C4", "dead-target", ModNM, OutcomeDead, team(TeamRed))

	tests := []struct {
		name    string
		command RobPiece
		code    ErrorCode
	}{
		{name: "overlapping pairs", command: RobPiece{TargetPieceID: "red-target", SacrificeSets: [][]string{{"blue-1", "blue-2"}, {"blue-2", "blue-3"}}}, code: CodeAlignmentOverlap},
		{name: "non-adjacent pair", command: RobPiece{TargetPieceID: "red-target", SacrificeSets: [][]string{{"blue-1", "blue-5"}, {"blue-2", "blue-3"}}}, code: CodeRobberyRequirementsNotMet},
		{name: "non-contiguous three", command: RobPiece{TargetPieceID: "red-target", SacrificeSets: [][]string{{"blue-1", "blue-3", "blue-4"}}}, code: CodeRobberyRequirementsNotMet},
		{name: "own target", command: RobPiece{TargetPieceID: "blue-target", SacrificeSets: [][]string{{"blue-1", "blue-2", "blue-3"}}}, code: CodeRobberyRequirementsNotMet},
		{name: "dead target", command: RobPiece{TargetPieceID: "dead-target", SacrificeSets: [][]string{{"blue-1", "blue-2", "blue-3"}}}, code: CodeRobberyRequirementsNotMet},
		{name: "missing target", command: RobPiece{TargetPieceID: "missing", SacrificeSets: [][]string{{"blue-1", "blue-2", "blue-3"}}}, code: CodeRobberyRequirementsNotMet},
		{name: "missing sacrifice", command: RobPiece{TargetPieceID: "red-target", SacrificeSets: [][]string{{"blue-1", "blue-2", "missing"}}}, code: CodeRobberyRequirementsNotMet},
		{name: "wrong number of sets", command: RobPiece{TargetPieceID: "red-target", SacrificeSets: [][]string{{"blue-1", "blue-2"}}}, code: CodeRobberyRequirementsNotMet},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := base.Clone()
			_, err := Execute(base, StrategistActor(TeamBlue), tt.command, testStart.Add(5*time.Second))
			assertErrorCode(t, err, tt.code)
			if !reflect.DeepEqual(base, before) {
				t.Fatal("failed robbery mutated input")
			}
		})
	}
}

func TestEachTeamCanRobOnlyOnce(t *testing.T) {
	t.Parallel()

	state := robberyState(t)
	seedPiece(&state.Board, "A1", "blue-1", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "B1", "blue-2", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "C1", "blue-3", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "D2", "blue-anchor-1", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "D3", "blue-anchor-2", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "D4", "red-target", ModNM, OutcomeWon, team(TeamRed))
	state = mustExecute(t, state, StrategistActor(TeamBlue), RobPiece{
		TargetPieceID: "red-target", SacrificeSets: [][]string{{"blue-1", "blue-2", "blue-3"}},
	}, testStart.Add(5*time.Second)).State

	before := state.Clone()
	_, err := Execute(state, StrategistActor(TeamBlue), RobPiece{
		TargetPieceID: "anything", SacrificeSets: [][]string{{"anything-1", "anything-2"}},
	}, testStart.Add(6*time.Second))
	assertErrorCode(t, err, CodeRobberyNotAvailable)
	if !reflect.DeepEqual(state, before) {
		t.Fatal("second robbery mutated input")
	}
}

func TestSecondTeamRobberyAtTurnFifteenStartsForcedTB(t *testing.T) {
	t.Parallel()

	state := stateAtTurn(t, 15)
	state.RobberyUsed[TeamRed] = true
	seedPiece(&state.Board, "A1", "blue-1", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "B1", "blue-2", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "C1", "blue-3", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "D2", "blue-anchor-1", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "D3", "blue-anchor-2", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "D4", "red-target", ModNM, OutcomeWon, team(TeamRed))

	transition := mustExecute(t, state, StrategistActor(TeamBlue), RobPiece{
		TargetPieceID: "red-target",
		SacrificeSets: [][]string{{"blue-1", "blue-2", "blue-3"}},
	}, testStart.Add(30*time.Second))

	state = transition.State
	if state.Phase != PhaseTBPreparation || state.ActiveTeam != "" || state.Turn != 15 {
		t.Fatalf("forced TB after second robbery = phase %q active %q turn %d", state.Phase, state.ActiveTeam, state.Turn)
	}
	if state.TBEntry == nil || state.TBEntry.Basis != TBBasisForcedAfterRobberies {
		t.Fatalf("forced TB evidence = %+v", state.TBEntry)
	}
	assertEventTypes(t, transition.Events,
		EventPiecesSacrificed, EventPieceRobbed, EventTBForced, EventTBPreparationStarted, EventTimerStarted)
}

func TestRobberyCanCreateFourAndFinishMatch(t *testing.T) {
	t.Parallel()

	state := robberyState(t)
	seedPiece(&state.Board, "A1", "blue-row-1", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "B1", "blue-row-2", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "C1", "blue-row-3", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "D1", "red-target", ModNM, OutcomeWon, team(TeamRed))
	seedPiece(&state.Board, "A3", "blue-sacrifice-1", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "B3", "blue-sacrifice-2", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "C3", "blue-sacrifice-3", ModNM, OutcomeWon, team(TeamBlue))

	transition := mustExecute(t, state, StrategistActor(TeamBlue), RobPiece{
		TargetPieceID: "red-target",
		SacrificeSets: [][]string{{"blue-sacrifice-1", "blue-sacrifice-2", "blue-sacrifice-3"}},
	}, testStart.Add(5*time.Second))
	state = transition.State

	if state.Lifecycle != LifecycleFinished || state.Phase != PhaseNone || state.Winner == nil || *state.Winner != TeamBlue {
		t.Fatalf("terminal state = %+v, want BLUE four-alignment win", state)
	}
	assertEventTypes(t, transition.Events, EventPiecesSacrificed, EventPieceRobbed, EventMatchFinished)
}

func TestRobberyStateRoundTripPreservesDeadCellsAndUsage(t *testing.T) {
	t.Parallel()

	state := robberyState(t)
	seedPiece(&state.Board, "A1", "blue-1", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "B1", "blue-2", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "B2", "shiro-piece", ModShiro, OutcomeWhite, nil)
	seedPiece(&state.Board, "B3", "blue-anchor", ModNM, OutcomeWon, team(TeamBlue))
	state = mustExecute(t, state, StrategistActor(TeamBlue), RobPiece{
		TargetPieceID: "shiro-piece",
		SacrificeSets: [][]string{{"blue-1", "blue-2"}},
	}, testStart.Add(5*time.Second)).State

	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal robbery state: %v", err)
	}
	var restored State
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatalf("Unmarshal robbery state: %v", err)
	}
	if !reflect.DeepEqual(restored, state) {
		t.Fatalf("restored robbery state differs\n got: %#v\nwant: %#v", restored, state)
	}

	_, err = Execute(restored, StrategistActor(TeamBlue), RobPiece{
		TargetPieceID: "anything",
		SacrificeSets: [][]string{{"anything-1", "anything-2"}},
	}, testStart.Add(6*time.Second))
	assertErrorCode(t, err, CodeRobberyNotAvailable)

	_, err = Execute(restored, StrategistActor(TeamBlue), PlacePiece{
		PoolSlotID: "NM5", PieceID: "new-piece", Cell: "A1",
	}, testStart.Add(6*time.Second))
	assertErrorCode(t, err, CodeInvalidBoardCell)
}

func robberyState(t *testing.T) State {
	t.Helper()
	state := stateAtFirstPick(t)
	if state.ActiveTeam != TeamBlue {
		t.Fatalf("robbery fixture active team = %q, want BLUE", state.ActiveTeam)
	}
	return state
}

func seedPiece(board *Board, cell Cell, id string, mod Mod, outcome Outcome, owner *TeamSide) {
	board.place(cell, BoardPiece{
		ID:               id,
		SourcePoolSlotID: "fixture-" + id,
		Mod:              mod,
		SelectedBy:       TeamBlue,
		Owner:            owner,
		Outcome:          outcome,
	})
}

func team(side TeamSide) *TeamSide {
	return &side
}
