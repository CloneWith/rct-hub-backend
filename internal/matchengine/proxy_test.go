package matchengine

import (
	"reflect"
	"testing"
	"time"
)

func TestRefereeProxyBanOverridesIdentityAndExpiryOnly(t *testing.T) {
	t.Parallel()

	state := mustExecute(t, newReadyState(t), RefereeActor(), StartMatch{}, testStart).State
	transition := mustExecute(t, state, RefereeActor(), RefereeBanPoolSlot{
		ActingTeam: TeamRed, PoolSlotID: "NM1", Reason: "red strategist disconnected",
	}, testStart.Add(BanDuration))
	if transition.State.PoolSlots["NM1"].State != PoolSlotBanned {
		t.Fatal("proxy ban did not ban NM1")
	}
	assertEventTypes(t, transition.Events, EventPoolSlotBanned, EventTurnAdvanced, EventTimerStarted, EventRefereeProxyActionRecorded)

	before := state.Clone()
	_, err := Execute(state, RefereeActor(), RefereeBanPoolSlot{
		ActingTeam: TeamBlue, PoolSlotID: "NM1", Reason: "wrong side",
	}, testStart.Add(BanDuration))
	assertErrorCode(t, err, CodeNotActiveTeam)
	if !reflect.DeepEqual(state, before) {
		t.Fatal("failed proxy ban mutated state")
	}
}

func TestRefereeProxyPlacementStillEnforcesCellAndModRules(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	deadline := state.Timer.StartedAt.Add(state.Timer.Duration)
	transition := mustExecute(t, state, RefereeActor(), RefereePlacePiece{
		ActingTeam: TeamBlue, PoolSlotID: "HD1", PieceID: "proxy-piece", Cell: "C1", Reason: "blue disconnected",
	}, deadline)
	if transition.State.PendingPieceID != "proxy-piece" {
		t.Fatalf("pending piece = %q", transition.State.PendingPieceID)
	}
	assertEventTypes(t, transition.Events, EventPiecePlaced, EventResultConfirmationRequested, EventTimerStarted, EventRefereeProxyActionRecorded)

	_, err := Execute(state, RefereeActor(), RefereePlacePiece{
		ActingTeam: TeamBlue, PoolSlotID: "HD1", PieceID: "bad-zone", Cell: "A1", Reason: "blue disconnected",
	}, deadline)
	assertErrorCode(t, err, CodeInvalidModZone)
}

func TestRefereeProxyShiroAndRobberyUseNormalRulePaths(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	transition := mustExecute(t, state, RefereeActor(), RefereePlaceShiro{
		ActingTeam: TeamBlue, PieceID: "proxy-shiro", Cell: "A1", Reason: "blue disconnected",
	}, state.Timer.StartedAt.Add(state.Timer.Duration))
	assertEventTypes(t, transition.Events, EventShiroPlaced, EventTurnAdvanced, EventTimerStarted, EventRefereeProxyActionRecorded)

	state = stateAtFirstPick(t)
	seedPiece(&state.Board, "A1", "blue-1", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "B1", "blue-2", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "C1", "blue-3", ModNM, OutcomeWon, team(TeamBlue))
	seedPiece(&state.Board, "D4", "red-target", ModNM, OutcomeWon, team(TeamRed))
	transition = mustExecute(t, state, RefereeActor(), RefereeRobPiece{
		ActingTeam: TeamBlue, TargetPieceID: "red-target", SacrificeSets: [][]string{{"blue-1", "blue-2", "blue-3"}}, Reason: "blue disconnected",
	}, state.Timer.StartedAt.Add(state.Timer.Duration))
	assertEventTypes(t, transition.Events, EventPiecesSacrificed, EventPieceRobbed, EventRefereeProxyActionRecorded)
}

func TestRefereeProxyTBRequestAndResponsePreserveNegotiationRoles(t *testing.T) {
	t.Parallel()

	state := stateAtTurn13(t)
	deadline := state.Timer.StartedAt.Add(state.Timer.Duration)
	transition := mustExecute(t, state, RefereeActor(), RefereeRequestTB{
		ActingTeam: TeamRed, RequestID: "proxy-tb", Basis: TBBasisTurnThirteen, Reason: "red disconnected",
	}, deadline)
	assertEventTypes(t, transition.Events, EventTBRequested, EventRefereeProxyActionRecorded)

	transition = mustExecute(t, transition.State, RefereeActor(), RefereeRespondTBRequest{
		ActingTeam: TeamBlue, RequestID: "proxy-tb", Accept: true, Reason: "blue disconnected",
	}, deadline.Add(time.Minute))
	assertEventTypes(t, transition.Events,
		EventTBRequestAccepted, EventTBPreparationStarted, EventTimerStarted, EventRefereeProxyActionRecorded)
}

func TestRefereeProxyRejectsMissingReasonWrongActorAndPausedTimer(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	paused := mustExecute(t, state, RefereeActor(), PauseTimer{Reason: "review"}, state.Timer.StartedAt.Add(time.Second)).State
	tests := []struct {
		name  string
		state State
		actor Actor
		cmd   Command
		code  ErrorCode
	}{
		{name: "reason required", state: state, actor: RefereeActor(), cmd: RefereePlaceShiro{ActingTeam: TeamBlue, PieceID: "x", Cell: "A1"}, code: CodeInvalidRequest},
		{name: "strategist cannot proxy", state: state, actor: StrategistActor(TeamBlue), cmd: RefereePlaceShiro{ActingTeam: TeamBlue, PieceID: "x", Cell: "A1", Reason: "no"}, code: CodeActionNotAllowed},
		{name: "paused timer remains authoritative", state: paused, actor: RefereeActor(), cmd: RefereePlaceShiro{ActingTeam: TeamBlue, PieceID: "x", Cell: "A1", Reason: "proxy"}, code: CodeTimerPaused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.state.Clone()
			_, err := Execute(tt.state, tt.actor, tt.cmd, testStart.Add(time.Hour))
			assertErrorCode(t, err, tt.code)
			if !reflect.DeepEqual(tt.state, before) {
				t.Fatal("failed proxy command mutated state")
			}
		})
	}
}
