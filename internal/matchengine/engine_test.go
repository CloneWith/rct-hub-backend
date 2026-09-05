package matchengine

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

var testStart = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

func TestNewBoardUsesConfirmedZoneLayout(t *testing.T) {
	t.Parallel()

	board := NewBoard()
	tests := []struct {
		cell Cell
		zone Zone
	}{
		{cell: "A1", zone: ZoneDT},
		{cell: "B2", zone: ZoneDT},
		{cell: "C1", zone: ZoneHD},
		{cell: "D2", zone: ZoneHD},
		{cell: "A3", zone: ZoneHR},
		{cell: "B4", zone: ZoneHR},
		{cell: "C3", zone: ZoneDT},
		{cell: "D4", zone: ZoneDT},
	}

	for _, tt := range tests {
		t.Run(string(tt.cell), func(t *testing.T) {
			zone, ok := board.ZoneAt(tt.cell)
			if !ok {
				t.Fatalf("ZoneAt(%q) did not recognize a valid cell", tt.cell)
			}
			if zone != tt.zone {
				t.Fatalf("ZoneAt(%q) = %q, want %q", tt.cell, zone, tt.zone)
			}
		})
	}

	if _, ok := board.ZoneAt("E1"); ok {
		t.Fatal("ZoneAt(E1) accepted an out-of-board cell")
	}
}

func TestNewReadyStateRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	validSlots := []PoolSlot{{ID: "SHIRO", Mod: ModShiro}, {ID: "TB", Mod: ModTB}}
	tests := []struct {
		name          string
		configuration Configuration
	}{
		{name: "invalid team", configuration: Configuration{FirstBan: "GREEN", FirstPick: TeamBlue, PoolSlots: validSlots}},
		{name: "duplicate slot id", configuration: Configuration{FirstBan: TeamRed, FirstPick: TeamBlue, PoolSlots: []PoolSlot{{ID: "same", Mod: ModShiro}, {ID: "same", Mod: ModTB}}}},
		{name: "missing Shiro", configuration: Configuration{FirstBan: TeamRed, FirstPick: TeamBlue, PoolSlots: []PoolSlot{{ID: "TB", Mod: ModTB}}}},
		{name: "missing TB", configuration: Configuration{FirstBan: TeamRed, FirstPick: TeamBlue, PoolSlots: []PoolSlot{{ID: "SHIRO", Mod: ModShiro}}}},
		{name: "invalid mod", configuration: Configuration{FirstBan: TeamRed, FirstPick: TeamBlue, PoolSlots: []PoolSlot{{ID: "SHIRO", Mod: ModShiro}, {ID: "TB", Mod: ModTB}, {ID: "bad", Mod: "RX"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewReadyState(tt.configuration)
			assertErrorCode(t, err, CodeInvalidRequest)
		})
	}
}

func TestNewReadyStateRejectsInvalidRosters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Configuration)
	}{
		{name: "missing team", mutate: func(configuration *Configuration) { delete(configuration.Rosters, TeamBlue) }},
		{name: "no leader", mutate: func(configuration *Configuration) {
			roster := configuration.Rosters[TeamRed]
			roster.LeaderID = 0
			configuration.Rosters[TeamRed] = roster
		}},
		{name: "negative leader", mutate: func(configuration *Configuration) {
			roster := configuration.Rosters[TeamRed]
			roster.LeaderID = -1
			configuration.Rosters[TeamRed] = roster
		}},
		{name: "leader not rostered", mutate: func(configuration *Configuration) {
			roster := configuration.Rosters[TeamRed]
			roster.LeaderID = 9999
			configuration.Rosters[TeamRed] = roster
		}},
		{name: "non-positive player id", mutate: func(configuration *Configuration) {
			roster := configuration.Rosters[TeamRed]
			roster.PlayerIDs[3] = 0
			configuration.Rosters[TeamRed] = roster
		}},
		{name: "duplicate within team", mutate: func(configuration *Configuration) {
			roster := configuration.Rosters[TeamRed]
			roster.PlayerIDs[7] = roster.PlayerIDs[6]
			configuration.Rosters[TeamRed] = roster
		}},
		{name: "duplicate across teams", mutate: func(configuration *Configuration) {
			roster := configuration.Rosters[TeamBlue]
			roster.PlayerIDs[7] = 1008
			configuration.Rosters[TeamBlue] = roster
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configuration := testConfiguration()
			tt.mutate(&configuration)
			_, err := NewReadyState(configuration)
			assertErrorCode(t, err, CodeInvalidRequest)
		})
	}
}

func TestNewReadyStateAcceptsUnderRoster(t *testing.T) {
	t.Parallel()

	// Only the leader needs to belong to the roster; smaller rosters are
	// accepted so a team can run a match as long as it has a leader and a
	// strategist.
	configuration := testConfiguration()
	configuration.Rosters[TeamRed] = Roster{
		LeaderID:  1001,
		PlayerIDs: []int64{1001},
	}
	configuration.Rosters[TeamBlue] = Roster{
		LeaderID:  2001,
		PlayerIDs: []int64{2001, 2002},
	}
	state, err := NewReadyState(configuration)
	if err != nil {
		t.Fatalf("NewReadyState: %v", err)
	}
	if len(state.Rosters[TeamRed].PlayerIDs) != 1 || len(state.Rosters[TeamBlue].PlayerIDs) != 2 {
		t.Fatalf("under-roster accepted with mutated sizes: red=%d blue=%d",
			len(state.Rosters[TeamRed].PlayerIDs),
			len(state.Rosters[TeamBlue].PlayerIDs))
	}
}

func TestStartAndBanFollowABBAThenFirstPick(t *testing.T) {
	t.Parallel()

	state := newReadyState(t)
	transition := mustExecute(t, state, RefereeActor(), StartMatch{}, testStart)
	state = transition.State

	assertStateHeader(t, state, LifecycleRunning, PhaseBan, -3, TeamRed)
	assertTimer(t, state.Timer, testStart, BanDuration)
	assertEventTypes(t, transition.Events, EventMatchStarted, EventBanPhaseStarted, EventTimerStarted)

	steps := []struct {
		team       TeamSide
		slotID     string
		wantTurn   int
		wantActive TeamSide
		wantPhase  Phase
	}{
		{team: TeamRed, slotID: "NM1", wantTurn: -2, wantActive: TeamBlue, wantPhase: PhaseBan},
		{team: TeamBlue, slotID: "NM2", wantTurn: -1, wantActive: TeamBlue, wantPhase: PhaseBan},
		{team: TeamBlue, slotID: "NM3", wantTurn: 0, wantActive: TeamRed, wantPhase: PhaseBan},
		{team: TeamRed, slotID: "NM4", wantTurn: 1, wantActive: TeamBlue, wantPhase: PhasePick},
	}

	for i, step := range steps {
		now := testStart.Add(time.Duration(i+1) * time.Second)
		transition = mustExecute(t, state, StrategistActor(step.team), BanPoolSlot{PoolSlotID: step.slotID}, now)
		state = transition.State
		assertStateHeader(t, state, LifecycleRunning, step.wantPhase, step.wantTurn, step.wantActive)
		if state.PoolSlots[step.slotID].State != PoolSlotBanned {
			t.Fatalf("slot %s state = %q, want banned", step.slotID, state.PoolSlots[step.slotID].State)
		}
	}

	assertEventTypes(t, transition.Events, EventPoolSlotBanned, EventTurnAdvanced, EventPickPhaseStarted, EventTimerStarted)
	assertTimer(t, state.Timer, testStart.Add(4*time.Second), PickDuration)
}

func TestSuccessfulCommandDoesNotMutateInputState(t *testing.T) {
	t.Parallel()

	ready := newReadyState(t)
	before := ready.Clone()
	transition := mustExecute(t, ready, RefereeActor(), StartMatch{}, testStart)
	if !reflect.DeepEqual(ready, before) {
		t.Fatal("successful command mutated its input state")
	}
	if reflect.DeepEqual(transition.State, ready) {
		t.Fatal("successful command did not produce a changed state")
	}
}

func TestCommandsRejectWrongLifecycleAndPhase(t *testing.T) {
	t.Parallel()

	ready := newReadyState(t)
	_, err := Execute(ready, StrategistActor(TeamRed), BanPoolSlot{PoolSlotID: "NM1"}, testStart)
	assertErrorCode(t, err, CodeMatchLifecycleConflict)

	running := mustExecute(t, ready, RefereeActor(), StartMatch{}, testStart).State
	_, err = Execute(running, StrategistActor(TeamRed), PlacePiece{PoolSlotID: "NM5", PieceID: "piece-1", Cell: "A1"}, testStart.Add(time.Second))
	assertErrorCode(t, err, CodeMatchPhaseConflict)

	_, err = Execute(running, RefereeActor(), StartMatch{}, testStart.Add(time.Second))
	assertErrorCode(t, err, CodeMatchLifecycleConflict)
}

func TestBanRejectsIllegalCommandsWithoutMutatingInput(t *testing.T) {
	t.Parallel()

	started := mustExecute(t, newReadyState(t), RefereeActor(), StartMatch{}, testStart).State
	tests := []struct {
		name    string
		actor   Actor
		command Command
		now     time.Time
		code    ErrorCode
	}{
		{name: "wrong team", actor: StrategistActor(TeamBlue), command: BanPoolSlot{PoolSlotID: "NM1"}, now: testStart.Add(time.Second), code: CodeNotActiveTeam},
		{name: "referee cannot use strategist command", actor: RefereeActor(), command: BanPoolSlot{PoolSlotID: "NM1"}, now: testStart.Add(time.Second), code: CodeActionNotAllowed},
		{name: "timer expired at deadline", actor: StrategistActor(TeamRed), command: BanPoolSlot{PoolSlotID: "NM1"}, now: testStart.Add(BanDuration), code: CodeTimerExpired},
		{name: "unknown slot", actor: StrategistActor(TeamRed), command: BanPoolSlot{PoolSlotID: "missing"}, now: testStart.Add(time.Second), code: CodeInvalidPoolSlot},
		{name: "shiro cannot be banned", actor: StrategistActor(TeamRed), command: BanPoolSlot{PoolSlotID: "SHIRO"}, now: testStart.Add(time.Second), code: CodePoolSlotUnavailable},
		{name: "tb cannot be banned", actor: StrategistActor(TeamRed), command: BanPoolSlot{PoolSlotID: "TB"}, now: testStart.Add(time.Second), code: CodePoolSlotUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := started.Clone()
			_, err := Execute(started, tt.actor, tt.command, tt.now)
			assertErrorCode(t, err, tt.code)
			if !reflect.DeepEqual(started, before) {
				t.Fatal("failed command mutated its input state")
			}
		})
	}

	once := mustExecute(t, started, StrategistActor(TeamRed), BanPoolSlot{PoolSlotID: "NM1"}, testStart.Add(time.Second)).State
	before := once.Clone()
	_, err := Execute(once, StrategistActor(TeamBlue), BanPoolSlot{PoolSlotID: "NM1"}, testStart.Add(2*time.Second))
	assertErrorCode(t, err, CodePoolSlotUnavailable)
	if !reflect.DeepEqual(once, before) {
		t.Fatal("re-banning a slot mutated its input state")
	}
}

func TestPlacePieceDerivesFMAndWaitsForRefereeResult(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	placed := mustExecute(t, state, StrategistActor(TeamBlue), PlacePiece{
		PoolSlotID: "FM1",
		PieceID:    "piece-1",
		Cell:       "A1",
	}, testStart.Add(5*time.Second))
	state = placed.State

	assertStateHeader(t, state, LifecycleRunning, PhaseWaitingForResult, 1, TeamBlue)
	piece, ok := state.Board.PieceAt("A1")
	if !ok {
		t.Fatal("placed piece is absent from A1")
	}
	if piece.ID != "piece-1" || piece.SourcePoolSlotID != "FM1" || piece.SelectedBy != TeamBlue {
		t.Fatalf("unexpected placed piece: %+v", piece)
	}
	if piece.Owner != nil || piece.Outcome != OutcomeWaitingResult {
		t.Fatalf("piece before referee result = %+v, want unowned WAITING_RESULT", piece)
	}
	if piece.ForceMod == nil || *piece.ForceMod != ForceModNM {
		t.Fatalf("FM in DT zone has Force Mod %v, want NM", piece.ForceMod)
	}
	if state.PendingPieceID != "piece-1" {
		t.Fatalf("pending piece = %q, want piece-1", state.PendingPieceID)
	}
	if state.PoolSlots["FM1"].State != PoolSlotSelected {
		t.Fatalf("FM1 state = %q, want selected", state.PoolSlots["FM1"].State)
	}
	assertEventTypes(t, placed.Events, EventPiecePlaced, EventResultConfirmationRequested, EventTimerStarted)
	assertTimer(t, state.Timer, testStart.Add(5*time.Second), ResultConfirmationDuration)

	confirmed := mustExecute(t, state, RefereeActor(), ConfirmBeatmapResult{
		BoardPieceID: "piece-1",
		WinningTeam:  TeamRed,
	}, testStart.Add(6*time.Second))
	state = confirmed.State
	piece, _ = state.Board.PieceAt("A1")
	if piece.Owner == nil || *piece.Owner != TeamRed || piece.Outcome != OutcomeWon {
		t.Fatalf("piece after result = %+v, want RED WON", piece)
	}
	assertStateHeader(t, state, LifecycleRunning, PhasePick, 2, TeamRed)
	if state.PendingPieceID != "" {
		t.Fatalf("pending piece after confirmation = %q, want empty", state.PendingPieceID)
	}
	assertEventTypes(t, confirmed.Events, EventBeatmapResultConfirmed, EventPieceWon, EventTurnAdvanced, EventTimerStarted)
}

func TestPlacementModRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		slotID    string
		cell      Cell
		wantForce *ForceMod
	}{
		{name: "NM in DT", slotID: "NM5", cell: "A1"},
		{name: "HD in HD", slotID: "HD1", cell: "C1"},
		{name: "HR in HR", slotID: "HR1", cell: "A3"},
		{name: "DT in top-left DT", slotID: "DT1", cell: "A1"},
		{name: "DT in bottom-right DT", slotID: "DT1", cell: "C3"},
		{name: "FM in HD", slotID: "FM1", cell: "C1", wantForce: new(ForceModHD)},
		{name: "FM in HR", slotID: "FM1", cell: "A3", wantForce: new(ForceModHR)},
		{name: "FM in top-left DT becomes NM", slotID: "FM1", cell: "A1", wantForce: new(ForceModNM)},
		{name: "FM in bottom-right DT becomes NM", slotID: "FM1", cell: "C3", wantForce: new(ForceModNM)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := stateAtFirstPick(t)
			transition := mustExecute(t, state, StrategistActor(TeamBlue), PlacePiece{
				PoolSlotID: tt.slotID,
				PieceID:    "piece-1",
				Cell:       tt.cell,
			}, testStart.Add(5*time.Second))
			piece, _ := transition.State.Board.PieceAt(tt.cell)
			if !equalForceMod(piece.ForceMod, tt.wantForce) {
				t.Fatalf("Force Mod = %v, want %v", piece.ForceMod, tt.wantForce)
			}
		})
	}

	invalid := []struct {
		name   string
		slotID string
		cell   Cell
	}{
		{name: "HD in DT", slotID: "HD1", cell: "A1"},
		{name: "HR in HD", slotID: "HR1", cell: "C1"},
		{name: "DT in HR", slotID: "DT1", cell: "A3"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			state := stateAtFirstPick(t)
			_, err := Execute(state, StrategistActor(TeamBlue), PlacePiece{
				PoolSlotID: tt.slotID,
				PieceID:    "piece-1",
				Cell:       tt.cell,
			}, testStart.Add(5*time.Second))
			assertErrorCode(t, err, CodeInvalidModZone)
		})
	}
}

func TestPlaceAndConfirmRejectInvalidCommands(t *testing.T) {
	t.Parallel()

	pick := stateAtFirstPick(t)
	placeTests := []struct {
		name    string
		actor   Actor
		command PlacePiece
		now     time.Time
		code    ErrorCode
	}{
		{name: "wrong team", actor: StrategistActor(TeamRed), command: PlacePiece{PoolSlotID: "NM5", PieceID: "piece-1", Cell: "A1"}, now: testStart.Add(5 * time.Second), code: CodeNotActiveTeam},
		{name: "timer expired", actor: StrategistActor(TeamBlue), command: PlacePiece{PoolSlotID: "NM5", PieceID: "piece-1", Cell: "A1"}, now: testStart.Add(4*time.Second + PickDuration), code: CodeTimerExpired},
		{name: "unknown cell", actor: StrategistActor(TeamBlue), command: PlacePiece{PoolSlotID: "NM5", PieceID: "piece-1", Cell: "E1"}, now: testStart.Add(5 * time.Second), code: CodeInvalidBoardCell},
		{name: "empty piece id", actor: StrategistActor(TeamBlue), command: PlacePiece{PoolSlotID: "NM5", Cell: "A1"}, now: testStart.Add(5 * time.Second), code: CodeInvalidRequest},
		{name: "shiro uses separate command", actor: StrategistActor(TeamBlue), command: PlacePiece{PoolSlotID: "SHIRO", PieceID: "piece-1", Cell: "A1"}, now: testStart.Add(5 * time.Second), code: CodePoolSlotUnavailable},
	}

	for _, tt := range placeTests {
		t.Run(tt.name, func(t *testing.T) {
			before := pick.Clone()
			_, err := Execute(pick, tt.actor, tt.command, tt.now)
			assertErrorCode(t, err, tt.code)
			if !reflect.DeepEqual(pick, before) {
				t.Fatal("failed placement mutated input")
			}
		})
	}

	waiting := mustExecute(t, pick, StrategistActor(TeamBlue), PlacePiece{
		PoolSlotID: "NM5", PieceID: "piece-1", Cell: "A1",
	}, testStart.Add(5*time.Second)).State
	confirmTests := []struct {
		name    string
		actor   Actor
		command ConfirmBeatmapResult
		code    ErrorCode
	}{
		{name: "strategist cannot confirm", actor: StrategistActor(TeamBlue), command: ConfirmBeatmapResult{BoardPieceID: "piece-1", WinningTeam: TeamBlue}, code: CodeActionNotAllowed},
		{name: "wrong pending piece", actor: RefereeActor(), command: ConfirmBeatmapResult{BoardPieceID: "piece-2", WinningTeam: TeamBlue}, code: CodeResultNotPending},
		{name: "invalid winner", actor: RefereeActor(), command: ConfirmBeatmapResult{BoardPieceID: "piece-1", WinningTeam: TeamSide("GREEN")}, code: CodeInvalidRequest},
	}
	for _, tt := range confirmTests {
		t.Run(tt.name, func(t *testing.T) {
			before := waiting.Clone()
			_, err := Execute(waiting, tt.actor, tt.command, testStart.Add(6*time.Second))
			assertErrorCode(t, err, tt.code)
			if !reflect.DeepEqual(waiting, before) {
				t.Fatal("failed confirmation mutated input")
			}
		})
	}
}

func TestPlacementRejectsOccupiedCellSelectedSlotAndDuplicatePieceID(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	state = mustExecute(t, state, StrategistActor(TeamBlue), PlacePiece{
		PoolSlotID: "NM5", PieceID: "piece-1", Cell: "A1",
	}, testStart.Add(5*time.Second)).State
	state = mustExecute(t, state, RefereeActor(), ConfirmBeatmapResult{
		BoardPieceID: "piece-1", WinningTeam: TeamBlue,
	}, testStart.Add(6*time.Second)).State

	tests := []struct {
		name    string
		command PlacePiece
		code    ErrorCode
	}{
		{name: "occupied cell", command: PlacePiece{PoolSlotID: "NM6", PieceID: "piece-2", Cell: "A1"}, code: CodeInvalidBoardCell},
		{name: "selected pool slot", command: PlacePiece{PoolSlotID: "NM5", PieceID: "piece-2", Cell: "B1"}, code: CodePoolSlotUnavailable},
		{name: "duplicate board piece id", command: PlacePiece{PoolSlotID: "NM6", PieceID: "piece-1", Cell: "B1"}, code: CodeInvalidRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := state.Clone()
			_, err := Execute(state, StrategistActor(TeamRed), tt.command, testStart.Add(7*time.Second))
			assertErrorCode(t, err, tt.code)
			if !reflect.DeepEqual(state, before) {
				t.Fatal("failed placement mutated input")
			}
		})
	}
}

func TestStateJSONRoundTripPreservesPendingRuleBehavior(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	state = mustExecute(t, state, StrategistActor(TeamBlue), PlacePiece{
		PoolSlotID: "FM1", PieceID: "piece-1", Cell: "C1",
	}, testStart.Add(5*time.Second)).State

	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal state: %v", err)
	}
	var restored State
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatalf("Unmarshal state: %v", err)
	}
	if !reflect.DeepEqual(restored, state) {
		t.Fatalf("restored state differs\n got: %#v\nwant: %#v", restored, state)
	}

	command := ConfirmBeatmapResult{BoardPieceID: "piece-1", WinningTeam: TeamRed}
	want := mustExecute(t, state, RefereeActor(), command, testStart.Add(6*time.Second))
	got := mustExecute(t, restored, RefereeActor(), command, testStart.Add(6*time.Second))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("transition after restore differs\n got: %#v\nwant: %#v", got, want)
	}
}

func TestFourWonPiecesFinishOnlyAfterRefereeConfirmation(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	cells := []Cell{"A1", "B1", "C1", "D1"}
	slots := []string{"NM5", "NM6", "NM7", "NM8"}

	for i := range cells {
		pieceID := "winning-piece-" + string(rune('1'+i))
		placed := mustExecute(t, state, StrategistActor(state.ActiveTeam), PlacePiece{
			PoolSlotID: slots[i],
			PieceID:    pieceID,
			Cell:       cells[i],
		}, testStart.Add(time.Duration(5+i*2)*time.Second))
		state = placed.State

		if state.Winner != nil || state.Lifecycle == LifecycleFinished {
			t.Fatalf("placement %d ended match before referee confirmation", i+1)
		}

		confirmed := mustExecute(t, state, RefereeActor(), ConfirmBeatmapResult{
			BoardPieceID: pieceID,
			WinningTeam:  TeamRed,
		}, testStart.Add(time.Duration(6+i*2)*time.Second))
		state = confirmed.State

		if i < 3 && state.Lifecycle == LifecycleFinished {
			t.Fatalf("match ended after only %d won pieces", i+1)
		}
		if i == 3 {
			assertEventTypes(t, confirmed.Events, EventBeatmapResultConfirmed, EventPieceWon, EventMatchFinished)
		}
	}

	if state.Lifecycle != LifecycleFinished || state.Phase != PhaseNone {
		t.Fatalf("final state lifecycle/phase = %q/%q, want FINISHED/NONE", state.Lifecycle, state.Phase)
	}
	if state.Winner == nil || *state.Winner != TeamRed {
		t.Fatalf("winner = %v, want RED", state.Winner)
	}
	if state.Result == nil || state.Result.Winner != TeamRed || state.Result.Reason != ResultReasonFourAlignment {
		t.Fatalf("four-alignment result = %+v", state.Result)
	}
	if err := ValidateState(state); err != nil {
		t.Fatalf("ValidateState rejected engine-produced four-alignment result: %v", err)
	}
}

func TestFourAlignmentDirections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cells []Cell
	}{
		{name: "horizontal", cells: []Cell{"A2", "B2", "C2", "D2"}},
		{name: "vertical", cells: []Cell{"B1", "B2", "B3", "B4"}},
		{name: "descending diagonal", cells: []Cell{"A1", "B2", "C3", "D4"}},
		{name: "ascending diagonal", cells: []Cell{"A4", "B3", "C2", "D1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := stateAtFirstPick(t)
			for i, cell := range tt.cells {
				pieceID := "direction-piece-" + string(rune('1'+i))
				state = mustExecute(t, state, StrategistActor(state.ActiveTeam), PlacePiece{
					PoolSlotID: []string{"NM5", "NM6", "NM7", "NM8"}[i],
					PieceID:    pieceID,
					Cell:       cell,
				}, testStart.Add(time.Duration(5+i*2)*time.Second)).State
				state = mustExecute(t, state, RefereeActor(), ConfirmBeatmapResult{
					BoardPieceID: pieceID,
					WinningTeam:  TeamRed,
				}, testStart.Add(time.Duration(6+i*2)*time.Second)).State
			}
			if state.Winner == nil || *state.Winner != TeamRed {
				t.Fatalf("winner = %v, want RED", state.Winner)
			}
		})
	}
}

func newReadyState(t *testing.T) State {
	t.Helper()

	state, err := NewReadyState(testConfiguration())
	if err != nil {
		t.Fatalf("NewReadyState: %v", err)
	}
	return state
}

func testConfiguration() Configuration {
	slots := []PoolSlot{
		{ID: "NM1", Mod: ModNM}, {ID: "NM2", Mod: ModNM},
		{ID: "NM3", Mod: ModNM}, {ID: "NM4", Mod: ModNM},
		{ID: "NM5", Mod: ModNM}, {ID: "NM6", Mod: ModNM},
		{ID: "NM7", Mod: ModNM}, {ID: "NM8", Mod: ModNM},
		{ID: "HD1", Mod: ModHD}, {ID: "HR1", Mod: ModHR},
		{ID: "DT1", Mod: ModDT}, {ID: "FM1", Mod: ModFM},
		{ID: "SHIRO", Mod: ModShiro}, {ID: "TB", Mod: ModTB},
	}
	return Configuration{
		FirstBan:  TeamRed,
		FirstPick: TeamBlue,
		PoolSlots: slots,
		Rosters: map[TeamSide]Roster{
			TeamRed: {
				LeaderID:  1001,
				PlayerIDs: []int64{1001, 1002, 1003, 1004, 1005, 1006, 1007, 1008},
			},
			TeamBlue: {
				LeaderID:  2001,
				PlayerIDs: []int64{2001, 2002, 2003, 2004, 2005, 2006, 2007, 2008},
			},
		},
		Timers: StandardTimerConfiguration(),
	}
}

func stateAtFirstPick(t *testing.T) State {
	t.Helper()

	state := mustExecute(t, newReadyState(t), RefereeActor(), StartMatch{}, testStart).State
	bans := []struct {
		team TeamSide
		slot string
	}{
		{TeamRed, "NM1"},
		{TeamBlue, "NM2"},
		{TeamBlue, "NM3"},
		{TeamRed, "NM4"},
	}
	for i, ban := range bans {
		state = mustExecute(t, state, StrategistActor(ban.team), BanPoolSlot{PoolSlotID: ban.slot}, testStart.Add(time.Duration(i+1)*time.Second)).State
	}
	return state
}

func mustExecute(t *testing.T, state State, actor Actor, command Command, now time.Time) Transition {
	t.Helper()

	transition, err := Execute(state, actor, command, now)
	if err != nil {
		t.Fatalf("Execute(%T): %v", command, err)
	}
	return transition
}

func assertStateHeader(t *testing.T, state State, lifecycle Lifecycle, phase Phase, turn int, active TeamSide) {
	t.Helper()

	if state.Lifecycle != lifecycle || state.Phase != phase || state.Turn != turn || state.ActiveTeam != active {
		t.Fatalf("state header = %q/%q turn %d active %q, want %q/%q turn %d active %q",
			state.Lifecycle, state.Phase, state.Turn, state.ActiveTeam,
			lifecycle, phase, turn, active)
	}
}

func assertTimer(t *testing.T, timer Timer, startedAt time.Time, duration time.Duration) {
	t.Helper()

	if timer.StartedAt != startedAt || timer.Duration != duration {
		t.Fatalf("timer = start %s duration %s, want start %s duration %s", timer.StartedAt, timer.Duration, startedAt, duration)
	}
}

func assertEventTypes(t *testing.T, events []Event, want ...EventType) {
	t.Helper()

	got := make([]EventType, len(events))
	for i := range events {
		got[i] = events[i].Type
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
}

func assertErrorCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()

	if err == nil {
		t.Fatalf("error = nil, want code %q", want)
	}
	if got := CodeOf(err); got != want {
		t.Fatalf("error code = %q (%v), want %q", got, err, want)
	}
}

func equalForceMod(a, b *ForceMod) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
