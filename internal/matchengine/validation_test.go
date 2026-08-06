package matchengine

import (
	"fmt"
	"testing"
	"time"
)

func TestValidateStateAcceptsEngineProducedStates(t *testing.T) {
	t.Parallel()

	ready := newReadyState(t)
	started := mustExecute(t, ready, RefereeActor(), StartMatch{}, testStart).State
	waiting := stateAtFirstPick(t)
	waiting = mustExecute(t, waiting, StrategistActor(waiting.ActiveTeam), PlacePiece{
		PoolSlotID: "NM5", PieceID: "pending", Cell: "A1",
	}, testStart.Add(time.Second)).State
	suspended := mustExecute(t, started, RefereeActor(), SuspendMatch{Reason: "review"}, testStart.Add(2*time.Second)).State
	aborted := mustExecute(t, started, RefereeActor(), AbortMatch{Reason: "voided"}, testStart.Add(3*time.Second)).State
	finished := mustExecute(t, started, RefereeActor(), RecordSurrender{
		SurrenderingTeam:    TeamRed,
		ConfirmingPlayerIDs: []int64{1001, 1002, 1003, 1004},
		Reason:              "confirmed",
	}, testStart.Add(4*time.Second)).State
	stalemateFinished := stateAtPoolExhaustion(t, false)
	adjudication := stateAtPoolExhaustion(t, true)
	negotiatedTB := acceptedTBState(t)
	forcedTB := stateAtTurn(t, 14)
	forcedTB = mustExecute(t, forcedTB, StrategistActor(forcedTB.ActiveTeam), PlaceShiro{
		PieceID: "forced-shiro", Cell: "A1",
	}, testStart.Add(5*time.Second)).State

	for name, state := range map[string]State{
		"ready": ready, "running": started, "waiting": waiting,
		"suspended": suspended, "aborted": aborted, "finished": finished,
		"stalemate-finished": stalemateFinished, "adjudication": adjudication,
		"negotiated-TB": negotiatedTB, "forced-TB": forcedTB,
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateState(state); err != nil {
				t.Fatalf("ValidateState rejected engine-produced state: %v", err)
			}
		})
	}
}

func stateAtPoolExhaustion(t *testing.T, equalCounts bool) State {
	t.Helper()
	state, err := NewReadyState(Configuration{
		FirstBan:  TeamRed,
		FirstPick: TeamBlue,
		PoolSlots: []PoolSlot{
			{ID: "NM1", Mod: ModNM}, {ID: "NM2", Mod: ModNM},
			{ID: "NM3", Mod: ModNM}, {ID: "NM4", Mod: ModNM},
			{ID: "NM5", Mod: ModNM}, {ID: "NM6", Mod: ModNM},
			{ID: "SHIRO", Mod: ModShiro}, {ID: "TB", Mod: ModTB},
		},
		Rosters: testConfiguration().Rosters,
		Timers:  StandardTimerConfiguration(),
	})
	if err != nil {
		t.Fatalf("NewReadyState: %v", err)
	}
	state = mustExecute(t, state, RefereeActor(), StartMatch{}, testStart).State
	for index, slotID := range []string{"NM1", "NM2", "NM3", "NM4"} {
		state = mustExecute(t, state, StrategistActor(state.ActiveTeam), BanPoolSlot{PoolSlotID: slotID}, testStart.Add(time.Duration(index+1)*time.Second)).State
	}
	state = mustExecute(t, state, StrategistActor(state.ActiveTeam), PlacePiece{
		PoolSlotID: "NM5", PieceID: "pool-red", Cell: "A1",
	}, testStart.Add(5*time.Second)).State
	state = mustExecute(t, state, RefereeActor(), ConfirmBeatmapResult{
		BoardPieceID: "pool-red", WinningTeam: TeamRed,
	}, testStart.Add(6*time.Second)).State
	state = mustExecute(t, state, StrategistActor(state.ActiveTeam), PlacePiece{
		PoolSlotID: "NM6", PieceID: "pool-second", Cell: "B1",
	}, testStart.Add(7*time.Second)).State
	secondWinner := TeamRed
	if equalCounts {
		secondWinner = TeamBlue
	}
	state = mustExecute(t, state, RefereeActor(), ConfirmBeatmapResult{
		BoardPieceID: "pool-second", WinningTeam: secondWinner,
	}, testStart.Add(8*time.Second)).State
	state = mustExecute(t, state, StrategistActor(state.ActiveTeam), PlaceShiro{
		PieceID: "pool-shiro", Cell: "C1",
	}, testStart.Add(9*time.Second)).State
	return state
}

func TestValidateStateRejectsCorruptRecoveredState(t *testing.T) {
	t.Parallel()

	base := newReadyState(t)
	started := mustExecute(t, base, RefereeActor(), StartMatch{}, testStart).State
	finished := mustExecute(t,
		started,
		RefereeActor(),
		RecordSurrender{SurrenderingTeam: TeamRed, ConfirmingPlayerIDs: []int64{1001, 1002, 1003, 1004}, Reason: "confirmed"},
		testStart.Add(time.Second),
	).State
	tests := []struct {
		name   string
		mutate func(*State)
	}{
		{name: "unknown lifecycle", mutate: func(s *State) { s.Lifecycle = "BROKEN" }},
		{name: "missing side flags", mutate: func(s *State) { delete(s.RobberyUsed, TeamRed) }},
		{name: "pool key mismatch", mutate: func(s *State) {
			slot := s.PoolSlots["NM1"]
			slot.ID = "NM2"
			s.PoolSlots["NM1"] = slot
		}},
		{name: "banned Shiro", mutate: func(s *State) {
			slot := s.PoolSlots["SHIRO"]
			slot.State = PoolSlotBanned
			s.PoolSlots["SHIRO"] = slot
		}},
		{name: "selected TB", mutate: func(s *State) {
			slot := s.PoolSlots["TB"]
			slot.State = PoolSlotSelected
			s.PoolSlots["TB"] = slot
		}},
		{name: "ready with active phase", mutate: func(s *State) { s.Phase = PhaseBan }},
		{name: "ready with committed version", mutate: func(s *State) { s.Version = 1 }},
		{name: "ready with banned pool slot", mutate: func(s *State) {
			slot := s.PoolSlots["NM1"]
			slot.State = PoolSlotBanned
			s.PoolSlots["NM1"] = slot
		}},
		{name: "ready with used entitlement", mutate: func(s *State) { s.RobberyUsed[TeamRed] = true }},
		{name: "running with zero version", mutate: func(s *State) {
			*s = started.Clone()
			s.Version = 0
		}},
		{name: "active timer without anchor", mutate: func(s *State) {
			*s = started.Clone()
			s.Timer.StartedAt = time.Time{}
		}},
		{name: "negative timer duration", mutate: func(s *State) {
			*s = started.Clone()
			s.Timer.Duration = -time.Second
		}},
		{name: "paused timer exceeds window", mutate: func(s *State) {
			*s = started.Clone()
			s.Timer.Paused = true
			s.Timer.RemainingAtPause = s.Timer.Duration + time.Second
		}},
		{name: "running timer retains paused remainder", mutate: func(s *State) {
			*s = started.Clone()
			s.Timer.RemainingAtPause = time.Second
		}},
		{name: "ban turn out of range", mutate: func(s *State) {
			*s = started.Clone()
			s.Turn = 1
		}},
		{name: "wrong active team for turn", mutate: func(s *State) {
			*s = started.Clone()
			s.ActiveTeam = s.ActiveTeam.opponent()
		}},
		{name: "terminal state retains timer", mutate: func(s *State) {
			*s = finished.Clone()
			s.Timer = started.Timer
		}},
		{name: "invalid terminal reason", mutate: func(s *State) {
			*s = finished.Clone()
			s.Result.Reason = "BROKEN"
		}},
		{name: "four-alignment result without board evidence", mutate: func(s *State) {
			*s = finished.Clone()
			s.Result.Reason = ResultReasonFourAlignment
			s.Result.SurrenderingTeam = nil
			s.Result.ConfirmingPlayerIDs = nil
		}},
		{name: "stalemate result without board evidence", mutate: func(s *State) {
			*s = finished.Clone()
			s.Result.Reason = ResultReasonStalemateWonCount
			s.Result.SurrenderingTeam = nil
			s.Result.ConfirmingPlayerIDs = nil
			s.Result.RedWonCount = 0
			s.Result.BlueWonCount = 1
		}},
		{name: "surrender result with stalemate counts", mutate: func(s *State) {
			*s = finished.Clone()
			s.Result.RedWonCount = 1
		}},
		{name: "unequal adjudication evidence", mutate: func(s *State) {
			s.Lifecycle = LifecycleAdjudicationRequired
			s.Stalemate = &StalemateEvidence{RedWonCount: 2, BlueWonCount: 1}
		}},
		{name: "invalid pending TB request", mutate: func(s *State) {
			s.Lifecycle = LifecycleRunning
			s.Phase = PhasePick
			s.ActiveTeam = TeamRed
			s.PendingTBRequest = &TBRequestState{ID: "", RequestedBy: TeamRed, Basis: TBBasisCaptainAgreement}
		}},
		{name: "TB preparation without entry evidence", mutate: func(s *State) {
			*s = started.Clone()
			s.Phase = PhaseTBPreparation
			s.ActiveTeam = ""
			s.Timer = Timer{StartedAt: testStart, Duration: TBPreparationDuration}
		}},
		{name: "turn 15 with no legal robberies outside TB", mutate: func(s *State) {
			*s = stateAtTurn(t, 15)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := base.Clone()
			tt.mutate(&state)
			if err := ValidateState(state); err == nil {
				t.Fatal("ValidateState accepted corrupt state")
			}
		})
	}
}

func TestValidateStateRejectsTBEntryOnNonTBTerminalResults(t *testing.T) {
	t.Parallel()

	four := stateAtFirstPick(t)
	for index, cell := range []Cell{"A1", "B1", "C1", "D1"} {
		pieceID := fmt.Sprintf("four-%d", index+1)
		four = mustExecute(t, four, StrategistActor(four.ActiveTeam), PlacePiece{
			PoolSlotID: fmt.Sprintf("NM%d", index+5), PieceID: pieceID, Cell: cell,
		}, testStart.Add(time.Duration(index*2+1)*time.Second)).State
		four = mustExecute(t, four, RefereeActor(), ConfirmBeatmapResult{
			BoardPieceID: pieceID, WinningTeam: TeamRed,
		}, testStart.Add(time.Duration(index*2+2)*time.Second)).State
	}
	four.Turn = 11
	four.TBEntry = &TBEntryState{Basis: TBBasisCaptainAgreement, RequestID: "impossible-four-tb", RequestedBy: TeamRed}

	stalemate := stateAtPoolExhaustion(t, false)
	stalemate.Turn = 11
	stalemate.TBEntry = &TBEntryState{Basis: TBBasisCaptainAgreement, RequestID: "impossible-stalemate-tb", RequestedBy: TeamBlue}

	for name, state := range map[string]State{"four-alignment": four, "stalemate": stalemate} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateState(state); err == nil {
				t.Fatal("ValidateState accepted a non-TB result with TB entry evidence")
			}
		})
	}
}

func TestValidateStateRejectsForcedTBWhileRobberyRemainsAvailable(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	state.PoolSlots["NM9"] = PoolSlot{ID: "NM9", Mod: ModNM, State: PoolSlotAvailable}
	state.PoolSlots["NM10"] = PoolSlot{ID: "NM10", Mod: ModNM, State: PoolSlotAvailable}
	plays := []struct {
		slot, piece string
		cell        Cell
		winner      TeamSide
	}{
		{slot: "NM5", piece: "blue-sacrifice-1", cell: "A1", winner: TeamBlue},
		{slot: "NM6", piece: "blue-sacrifice-2", cell: "B1", winner: TeamBlue},
		{slot: "NM7", piece: "blue-sacrifice-3", cell: "C1", winner: TeamBlue},
		{slot: "NM8", piece: "blue-result-anchor-1", cell: "D2", winner: TeamBlue},
		{slot: "NM9", piece: "blue-result-anchor-2", cell: "D3", winner: TeamBlue},
		{slot: "NM10", piece: "red-target", cell: "D4", winner: TeamRed},
	}
	for index, play := range plays {
		state = mustExecute(t, state, StrategistActor(state.ActiveTeam), PlacePiece{
			PoolSlotID: play.slot, PieceID: play.piece, Cell: play.cell,
		}, testStart.Add(time.Duration(index*2+1)*time.Second)).State
		state = mustExecute(t, state, RefereeActor(), ConfirmBeatmapResult{
			BoardPieceID: play.piece, WinningTeam: play.winner,
		}, testStart.Add(time.Duration(index*2+2)*time.Second)).State
	}
	state.Turn = 15
	state.ActiveTeam = pickTeam(state.FirstPick, state.Turn)
	state.RobberyUsed[TeamRed] = true
	state.TBEntry = &TBEntryState{Basis: TBBasisForcedAfterRobberyChecks}
	state.Phase = PhaseTBPreparation
	state.ActiveTeam = ""
	state.Timer = Timer{StartedAt: testStart, Duration: TBPreparationDuration}

	if err := ValidateState(state); err == nil {
		t.Fatal("ValidateState accepted forced TB while BLUE still had a legal robbery")
	}
}
