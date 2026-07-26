package matchengine

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestTurnThirteenTBRequestCanBeRejectedWithoutChangingPick(t *testing.T) {
	t.Parallel()

	state := stateAtTurn13(t)
	beforeTimer := state.Timer
	requested := mustExecute(t, state, StrategistActor(TeamRed), RequestTB{
		RequestID: "tb-request-1",
		Basis:     TBBasisTurnThirteen,
	}, testStart.Add(30*time.Second))
	state = requested.State

	if state.PendingTBRequest == nil || state.PendingTBRequest.ID != "tb-request-1" || state.PendingTBRequest.RequestedBy != TeamRed {
		t.Fatalf("pending TB request = %+v", state.PendingTBRequest)
	}
	assertStateHeader(t, state, LifecycleRunning, PhasePick, 13, TeamBlue)
	if state.Timer != beforeTimer {
		t.Fatal("TB request reset the active Pick timer")
	}
	assertEventTypes(t, requested.Events, EventTBRequested)

	rejected := mustExecute(t, state, StrategistActor(TeamBlue), RespondTBRequest{
		RequestID: "tb-request-1",
		Accept:    false,
	}, testStart.Add(31*time.Second))
	state = rejected.State
	if state.PendingTBRequest != nil {
		t.Fatalf("rejected TB request remains pending: %+v", state.PendingTBRequest)
	}
	assertStateHeader(t, state, LifecycleRunning, PhasePick, 13, TeamBlue)
	if state.Timer != beforeTimer {
		t.Fatal("TB rejection reset the active Pick timer")
	}
	assertEventTypes(t, rejected.Events, EventTBRequestRejected)
}

func TestAcceptedTBRequestStartsNinetySecondPreparation(t *testing.T) {
	t.Parallel()

	state := requestedTBState(t)
	acceptAt := testStart.Add(31 * time.Second)
	transition := mustExecute(t, state, StrategistActor(TeamBlue), RespondTBRequest{
		RequestID: "tb-request-1",
		Accept:    true,
	}, acceptAt)
	state = transition.State

	if state.Phase != PhaseTBPreparation || state.ActiveTeam != "" || state.PendingTBRequest != nil {
		t.Fatalf("accepted TB state = phase %q active %q pending %+v", state.Phase, state.ActiveTeam, state.PendingTBRequest)
	}
	assertTimer(t, state.Timer, acceptAt, TBPreparationDuration)
	assertEventTypes(t, transition.Events, EventTBRequestAccepted, EventTBPreparationStarted, EventTimerStarted)
}

func TestTBRequestAndResponseRejectInvalidCommandsWithoutMutation(t *testing.T) {
	t.Parallel()

	turn13 := stateAtTurn13(t)
	turn12 := turn13.Clone()
	turn12.Turn = 12
	turn12.ActiveTeam = pickTeam(turn12.FirstPick, turn12.Turn)
	waiting := turn13.Clone()
	waiting.Phase = PhaseWaitingForResult
	expired := turn13.Clone()
	expired.Timer = Timer{StartedAt: testStart, Duration: time.Second}
	paused := turn13.Clone()
	paused.Timer.pause(testStart.Add(21 * time.Second))

	requestTests := []struct {
		name  string
		state State
		actor Actor
		cmd   RequestTB
		code  ErrorCode
	}{
		{name: "too early", state: turn12, actor: StrategistActor(TeamRed), cmd: RequestTB{RequestID: "request", Basis: TBBasisTurnThirteen}, code: CodeTBNotAvailable},
		{name: "wrong phase", state: waiting, actor: StrategistActor(TeamRed), cmd: RequestTB{RequestID: "request", Basis: TBBasisTurnThirteen}, code: CodeMatchPhaseConflict},
		{name: "referee cannot request", state: turn13, actor: RefereeActor(), cmd: RequestTB{RequestID: "request", Basis: TBBasisTurnThirteen}, code: CodeActionNotAllowed},
		{name: "request id required", state: turn13, actor: StrategistActor(TeamRed), cmd: RequestTB{Basis: TBBasisTurnThirteen}, code: CodeInvalidRequest},
		{name: "unsupported no-four basis deferred", state: turn13, actor: StrategistActor(TeamRed), cmd: RequestTB{RequestID: "request", Basis: TBBasisNoFourWithoutRobbery}, code: CodeTBNotAvailable},
		{name: "expired timer", state: expired, actor: StrategistActor(TeamRed), cmd: RequestTB{RequestID: "request", Basis: TBBasisTurnThirteen}, code: CodeTimerExpired},
		{name: "paused timer", state: paused, actor: StrategistActor(TeamRed), cmd: RequestTB{RequestID: "request", Basis: TBBasisTurnThirteen}, code: CodeTimerPaused},
	}
	for _, tt := range requestTests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.state.Clone()
			_, err := Execute(tt.state, tt.actor, tt.cmd, testStart.Add(30*time.Second))
			assertErrorCode(t, err, tt.code)
			if !reflect.DeepEqual(tt.state, before) {
				t.Fatal("failed TB request mutated state")
			}
		})
	}

	requested := requestedTBState(t)
	responseTests := []struct {
		name  string
		actor Actor
		cmd   RespondTBRequest
		code  ErrorCode
	}{
		{name: "requester cannot respond", actor: StrategistActor(TeamRed), cmd: RespondTBRequest{RequestID: "tb-request-1", Accept: true}, code: CodeActionNotAllowed},
		{name: "referee cannot respond", actor: RefereeActor(), cmd: RespondTBRequest{RequestID: "tb-request-1", Accept: true}, code: CodeActionNotAllowed},
		{name: "wrong request id", actor: StrategistActor(TeamBlue), cmd: RespondTBRequest{RequestID: "wrong", Accept: true}, code: CodeTBNotAvailable},
	}
	for _, tt := range responseTests {
		t.Run(tt.name, func(t *testing.T) {
			before := requested.Clone()
			_, err := Execute(requested, tt.actor, tt.cmd, testStart.Add(31*time.Second))
			assertErrorCode(t, err, tt.code)
			if !reflect.DeepEqual(requested, before) {
				t.Fatal("failed TB response mutated state")
			}
		})
	}
}

func TestRefereeStartsTBAndConfirmsResult(t *testing.T) {
	t.Parallel()

	state := acceptedTBState(t)
	startAt := testStart.Add(32 * time.Second)
	started := mustExecute(t, state, RefereeActor(), StartTB{}, startAt)
	state = started.State
	if state.Phase != PhaseTBPlaying || state.Timer.Duration != 0 {
		t.Fatalf("started TB state = phase %q timer %+v", state.Phase, state.Timer)
	}
	assertEventTypes(t, started.Events, EventTBStarted, EventTimerStopped)

	confirmed := mustExecute(t, state, RefereeActor(), ConfirmTBResult{WinningTeam: TeamBlue}, startAt.Add(time.Minute))
	state = confirmed.State
	assertTerminalResult(t, state, TeamBlue, ResultReasonTB)
	assertEventTypes(t, confirmed.Events, EventTBResultConfirmed, EventMatchFinished)
}

func TestExpiredTBPreparationRequiresRefereeReasonButDoesNotAutoAdvance(t *testing.T) {
	t.Parallel()

	state := acceptedTBState(t)
	deadline := state.Timer.StartedAt.Add(TBPreparationDuration)
	before := state.Clone()
	_, err := Execute(state, RefereeActor(), StartTB{}, deadline)
	assertErrorCode(t, err, CodeInvalidRequest)
	if !reflect.DeepEqual(state, before) || state.Phase != PhaseTBPreparation {
		t.Fatal("expired TB preparation advanced or mutated state")
	}

	state = mustExecute(t, state, RefereeActor(), StartTB{Reason: "preparation timeout reviewed"}, deadline).State
	if state.Phase != PhaseTBPlaying {
		t.Fatalf("phase = %q, want TB_PLAYING", state.Phase)
	}
}

func TestTBStartAndResultRejectInvalidCommandsWithoutMutation(t *testing.T) {
	t.Parallel()

	preparing := acceptedTBState(t)
	paused := mustExecute(t, preparing, RefereeActor(), PauseTimer{Reason: "technical"}, preparing.Timer.StartedAt.Add(time.Second)).State
	startTests := []struct {
		name  string
		state State
		actor Actor
		code  ErrorCode
	}{
		{name: "strategist cannot start", state: preparing, actor: StrategistActor(TeamRed), code: CodeActionNotAllowed},
		{name: "paused timer", state: paused, actor: RefereeActor(), code: CodeTimerPaused},
	}
	for _, tt := range startTests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.state.Clone()
			_, err := Execute(tt.state, tt.actor, StartTB{}, preparing.Timer.StartedAt.Add(2*time.Second))
			assertErrorCode(t, err, tt.code)
			if !reflect.DeepEqual(tt.state, before) {
				t.Fatal("failed TB start mutated state")
			}
		})
	}

	playing := mustExecute(t, preparing, RefereeActor(), StartTB{}, preparing.Timer.StartedAt.Add(time.Second)).State
	resultTests := []struct {
		name  string
		actor Actor
		cmd   ConfirmTBResult
		code  ErrorCode
	}{
		{name: "strategist cannot confirm", actor: StrategistActor(TeamRed), cmd: ConfirmTBResult{WinningTeam: TeamRed}, code: CodeActionNotAllowed},
		{name: "invalid winner", actor: RefereeActor(), cmd: ConfirmTBResult{WinningTeam: "GREEN"}, code: CodeInvalidRequest},
	}
	for _, tt := range resultTests {
		t.Run(tt.name, func(t *testing.T) {
			before := playing.Clone()
			_, err := Execute(playing, tt.actor, tt.cmd, testStart.Add(time.Hour))
			assertErrorCode(t, err, tt.code)
			if !reflect.DeepEqual(playing, before) {
				t.Fatal("failed TB result mutated state")
			}
		})
	}
}

func TestRefereeRecordsSurrenderWithRosterEvidence(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	transition := mustExecute(t, state, RefereeActor(), RecordSurrender{
		SurrenderingTeam:    TeamRed,
		ConfirmingPlayerIDs: []int64{1001, 1002, 1003, 1004},
		Reason:              "captain and players confirmed",
	}, testStart.Add(10*time.Second))
	state = transition.State

	assertTerminalResult(t, state, TeamBlue, ResultReasonSurrender)
	if state.Result.SurrenderingTeam == nil || *state.Result.SurrenderingTeam != TeamRed {
		t.Fatalf("surrendering team = %v, want RED", state.Result.SurrenderingTeam)
	}
	if !reflect.DeepEqual(state.Result.ConfirmingPlayerIDs, []int64{1001, 1002, 1003, 1004}) {
		t.Fatalf("confirmation evidence = %v", state.Result.ConfirmingPlayerIDs)
	}
	assertEventTypes(t, transition.Events, EventSurrenderRecorded, EventMatchFinished)
	if transition.Events[0].Reason != "captain and players confirmed" ||
		!reflect.DeepEqual(transition.Events[0].PlayerIDs, []int64{1001, 1002, 1003, 1004}) {
		t.Fatalf("surrender audit event = %+v", transition.Events[0])
	}
}

func TestSurrenderRejectsInvalidEvidenceWithoutMutation(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	tests := []struct {
		name  string
		actor Actor
		cmd   RecordSurrender
		code  ErrorCode
	}{
		{name: "strategist cannot record", actor: StrategistActor(TeamBlue), cmd: RecordSurrender{SurrenderingTeam: TeamRed, ConfirmingPlayerIDs: []int64{1001, 1002, 1003, 1004}, Reason: "no"}, code: CodeActionNotAllowed},
		{name: "invalid team", actor: RefereeActor(), cmd: RecordSurrender{SurrenderingTeam: "GREEN", ConfirmingPlayerIDs: []int64{1001, 1002, 1003, 1004}, Reason: "invalid"}, code: CodeInvalidRequest},
		{name: "reason required", actor: RefereeActor(), cmd: RecordSurrender{SurrenderingTeam: TeamRed, ConfirmingPlayerIDs: []int64{1001, 1002, 1003, 1004}}, code: CodeInvalidRequest},
		{name: "fewer than four", actor: RefereeActor(), cmd: RecordSurrender{SurrenderingTeam: TeamRed, ConfirmingPlayerIDs: []int64{1001, 1002, 1003}, Reason: "insufficient"}, code: CodeSurrenderEvidenceInvalid},
		{name: "leader absent", actor: RefereeActor(), cmd: RecordSurrender{SurrenderingTeam: TeamRed, ConfirmingPlayerIDs: []int64{1002, 1003, 1004, 1005}, Reason: "no leader"}, code: CodeSurrenderEvidenceInvalid},
		{name: "non-roster player", actor: RefereeActor(), cmd: RecordSurrender{SurrenderingTeam: TeamRed, ConfirmingPlayerIDs: []int64{1001, 1002, 1003, 9999}, Reason: "outsider"}, code: CodeSurrenderEvidenceInvalid},
		{name: "duplicates do not count", actor: RefereeActor(), cmd: RecordSurrender{SurrenderingTeam: TeamRed, ConfirmingPlayerIDs: []int64{1001, 1001, 1002, 1003}, Reason: "duplicate"}, code: CodeSurrenderEvidenceInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := state.Clone()
			_, err := Execute(state, tt.actor, tt.cmd, testStart.Add(10*time.Second))
			assertErrorCode(t, err, tt.code)
			if !reflect.DeepEqual(state, before) {
				t.Fatal("failed surrender mutated state")
			}
		})
	}
}

func TestTerminalResultSurvivesJSONAndClosesOrdinaryWrites(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	state = mustExecute(t, state, RefereeActor(), RecordSurrender{
		SurrenderingTeam: TeamBlue, ConfirmingPlayerIDs: []int64{2001, 2002, 2003, 2004}, Reason: "verified",
	}, testStart.Add(10*time.Second)).State

	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal terminal state: %v", err)
	}
	var restored State
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatalf("Unmarshal terminal state: %v", err)
	}
	if !reflect.DeepEqual(restored, state) {
		t.Fatalf("restored terminal state differs\n got: %#v\nwant: %#v", restored, state)
	}

	before := restored.Clone()
	_, err = Execute(restored, StrategistActor(TeamBlue), PlacePiece{
		PoolSlotID: "NM5", PieceID: "late-piece", Cell: "A1",
	}, testStart.Add(11*time.Second))
	assertErrorCode(t, err, CodeMatchLifecycleConflict)
	if !reflect.DeepEqual(restored, before) {
		t.Fatal("write after terminal result mutated state")
	}
}

func TestPendingTBRequestSurvivesJSONWithIdenticalResponse(t *testing.T) {
	t.Parallel()

	state := requestedTBState(t)
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal pending TB state: %v", err)
	}
	var restored State
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatalf("Unmarshal pending TB state: %v", err)
	}
	if !reflect.DeepEqual(restored, state) {
		t.Fatalf("restored pending TB state differs\n got: %#v\nwant: %#v", restored, state)
	}

	command := RespondTBRequest{RequestID: "tb-request-1", Accept: true}
	want := mustExecute(t, state, StrategistActor(TeamBlue), command, testStart.Add(31*time.Second))
	got := mustExecute(t, restored, StrategistActor(TeamBlue), command, testStart.Add(31*time.Second))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TB response after restore differs\n got: %#v\nwant: %#v", got, want)
	}
}

func stateAtTurn13(t *testing.T) State {
	t.Helper()
	state := stateAtFirstPick(t)
	state.Turn = 13
	state.ActiveTeam = pickTeam(state.FirstPick, state.Turn)
	state.Timer = Timer{StartedAt: testStart.Add(20 * time.Second), Duration: PickDuration}
	return state
}

func requestedTBState(t *testing.T) State {
	t.Helper()
	return mustExecute(t, stateAtTurn13(t), StrategistActor(TeamRed), RequestTB{
		RequestID: "tb-request-1", Basis: TBBasisTurnThirteen,
	}, testStart.Add(30*time.Second)).State
}

func acceptedTBState(t *testing.T) State {
	t.Helper()
	return mustExecute(t, requestedTBState(t), StrategistActor(TeamBlue), RespondTBRequest{
		RequestID: "tb-request-1", Accept: true,
	}, testStart.Add(31*time.Second)).State
}

func assertTerminalResult(t *testing.T, state State, winner TeamSide, reason ResultReason) {
	t.Helper()
	if state.Lifecycle != LifecycleFinished || state.Phase != PhaseNone || state.ActiveTeam != "" {
		t.Fatalf("terminal header = %q/%q active %q", state.Lifecycle, state.Phase, state.ActiveTeam)
	}
	if state.Winner == nil || *state.Winner != winner {
		t.Fatalf("winner = %v, want %q", state.Winner, winner)
	}
	if state.Result == nil || state.Result.Winner != winner || state.Result.Reason != reason {
		t.Fatalf("result = %+v, want winner %q reason %q", state.Result, winner, reason)
	}
}
