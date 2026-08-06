package matchengine

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestCaptainTBRequestInNegotiationWindowCanBeRejectedWithoutChangingPick(t *testing.T) {
	t.Parallel()

	state := stateAtTurn(t, 11)
	beforeTimer := state.Timer
	requested := mustExecute(t, state, CaptainActor(TeamRed), RequestTB{
		RequestID: "tb-request-1",
		Basis:     TBBasisCaptainAgreement,
	}, testStart.Add(30*time.Second))
	state = requested.State

	if state.PendingTBRequest == nil || state.PendingTBRequest.ID != "tb-request-1" || state.PendingTBRequest.RequestedBy != TeamRed {
		t.Fatalf("pending TB request = %+v", state.PendingTBRequest)
	}
	assertStateHeader(t, state, LifecycleRunning, PhasePick, 11, TeamBlue)
	if state.Timer != beforeTimer {
		t.Fatal("TB request reset the active Pick timer")
	}
	assertEventTypes(t, requested.Events, EventTBRequested)
	if event := requested.Events[0]; event.Basis != TBBasisCaptainAgreement || event.Team != TeamRed {
		t.Fatalf("TB request event evidence = %+v", event)
	}

	rejected := mustExecute(t, state, CaptainActor(TeamBlue), RespondTBRequest{
		RequestID: "tb-request-1",
		Accept:    false,
	}, testStart.Add(31*time.Second))
	state = rejected.State
	if state.PendingTBRequest != nil {
		t.Fatalf("rejected TB request remains pending: %+v", state.PendingTBRequest)
	}
	assertStateHeader(t, state, LifecycleRunning, PhasePick, 11, TeamBlue)
	if state.Timer != beforeTimer {
		t.Fatal("TB rejection reset the active Pick timer")
	}
	assertEventTypes(t, rejected.Events, EventTBRequestRejected)
	if event := rejected.Events[0]; event.Basis != TBBasisCaptainAgreement || event.Team != TeamBlue {
		t.Fatalf("TB rejection event evidence = %+v", event)
	}
}

func TestAcceptedTBRequestStartsNinetySecondPreparation(t *testing.T) {
	t.Parallel()

	state := requestedTBState(t)
	acceptAt := testStart.Add(31 * time.Second)
	transition := mustExecute(t, state, CaptainActor(TeamBlue), RespondTBRequest{
		RequestID: "tb-request-1",
		Accept:    true,
	}, acceptAt)
	state = transition.State

	if state.Phase != PhaseTBPreparation || state.ActiveTeam != "" || state.PendingTBRequest != nil {
		t.Fatalf("accepted TB state = phase %q active %q pending %+v", state.Phase, state.ActiveTeam, state.PendingTBRequest)
	}
	if state.TBEntry == nil || state.TBEntry.Basis != TBBasisCaptainAgreement || state.TBEntry.RequestID != "tb-request-1" {
		t.Fatalf("accepted TB entry evidence = %+v", state.TBEntry)
	}
	assertTimer(t, state.Timer, acceptAt, TBPreparationDuration)
	assertEventTypes(t, transition.Events, EventTBRequestAccepted, EventTBPreparationStarted, EventTimerStarted)
	if transition.Events[0].Basis != TBBasisCaptainAgreement || transition.Events[1].Basis != TBBasisCaptainAgreement {
		t.Fatalf("TB acceptance event evidence = %+v", transition.Events)
	}
}

func TestTBRequestAndResponseRejectInvalidCommandsWithoutMutation(t *testing.T) {
	t.Parallel()

	turn11 := stateAtTurn(t, 11)
	turn10 := stateAtTurn(t, 10)
	turn15 := stateAtTurn(t, 15)
	waiting := turn11.Clone()
	waiting.Phase = PhaseWaitingForResult
	expired := turn11.Clone()
	expired.Timer = Timer{StartedAt: testStart, Duration: time.Second}
	paused := turn11.Clone()
	paused.Timer.pause(testStart.Add(21 * time.Second))

	requestTests := []struct {
		name  string
		state State
		actor Actor
		cmd   RequestTB
		code  ErrorCode
	}{
		{name: "too early", state: turn10, actor: CaptainActor(TeamRed), cmd: RequestTB{RequestID: "request", Basis: TBBasisCaptainAgreement}, code: CodeTBNotAvailable},
		{name: "too late", state: turn15, actor: CaptainActor(TeamRed), cmd: RequestTB{RequestID: "request", Basis: TBBasisCaptainAgreement}, code: CodeTBNotAvailable},
		{name: "wrong phase", state: waiting, actor: CaptainActor(TeamRed), cmd: RequestTB{RequestID: "request", Basis: TBBasisCaptainAgreement}, code: CodeMatchPhaseConflict},
		{name: "strategist cannot request", state: turn11, actor: StrategistActor(TeamRed), cmd: RequestTB{RequestID: "request", Basis: TBBasisCaptainAgreement}, code: CodeActionNotAllowed},
		{name: "referee cannot request directly", state: turn11, actor: RefereeActor(), cmd: RequestTB{RequestID: "request", Basis: TBBasisCaptainAgreement}, code: CodeActionNotAllowed},
		{name: "request id required", state: turn11, actor: CaptainActor(TeamRed), cmd: RequestTB{Basis: TBBasisCaptainAgreement}, code: CodeInvalidRequest},
		{name: "forced basis cannot be requested", state: turn11, actor: CaptainActor(TeamRed), cmd: RequestTB{RequestID: "request", Basis: TBBasisForcedAfterRobberies}, code: CodeTBNotAvailable},
		{name: "expired pick timer does not block captain agreement", state: expired, actor: CaptainActor(TeamRed), cmd: RequestTB{RequestID: "request", Basis: TBBasisCaptainAgreement}, code: ""},
		{name: "paused match timer", state: paused, actor: CaptainActor(TeamRed), cmd: RequestTB{RequestID: "request", Basis: TBBasisCaptainAgreement}, code: CodeTimerPaused},
	}
	for _, tt := range requestTests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.state.Clone()
			transition, err := Execute(tt.state, tt.actor, tt.cmd, testStart.Add(30*time.Second))
			if tt.code == "" {
				if err != nil || transition.State.PendingTBRequest == nil {
					t.Fatalf("captain request after pick expiry = transition %+v err %v", transition, err)
				}
			} else {
				assertErrorCode(t, err, tt.code)
			}
			if !reflect.DeepEqual(tt.state, before) {
				t.Fatal("TB request mutated input state")
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
		{name: "requester cannot respond", actor: CaptainActor(TeamRed), cmd: RespondTBRequest{RequestID: "tb-request-1", Accept: true}, code: CodeActionNotAllowed},
		{name: "strategist cannot respond", actor: StrategistActor(TeamBlue), cmd: RespondTBRequest{RequestID: "tb-request-1", Accept: true}, code: CodeActionNotAllowed},
		{name: "referee cannot respond", actor: RefereeActor(), cmd: RespondTBRequest{RequestID: "tb-request-1", Accept: true}, code: CodeActionNotAllowed},
		{name: "wrong request id", actor: CaptainActor(TeamBlue), cmd: RespondTBRequest{RequestID: "wrong", Accept: true}, code: CodeTBNotAvailable},
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

func TestCaptainTBRequestIsAvailableOnEveryNegotiationTurn(t *testing.T) {
	t.Parallel()

	for turn := 11; turn <= 14; turn++ {
		t.Run(fmt.Sprintf("turn-%d", turn), func(t *testing.T) {
			state := stateAtTurn(t, turn)
			transition := mustExecute(t, state, CaptainActor(TeamRed), RequestTB{
				RequestID: fmt.Sprintf("tb-%d", turn), Basis: TBBasisCaptainAgreement,
			}, testStart.Add(30*time.Second))
			if transition.State.PendingTBRequest == nil {
				t.Fatalf("turn %d did not retain pending TB request", turn)
			}
		})
	}
}

func TestTurnFifteenStartsForcedTBWhenBothTeamsAlreadyRobbed(t *testing.T) {
	t.Parallel()

	state := stateAtTurn(t, 14)
	state.RobberyUsed[TeamRed] = true
	state.RobberyUsed[TeamBlue] = true
	transition := mustExecute(t, state, StrategistActor(state.ActiveTeam), PlaceShiro{
		PieceID: "turn-14-shiro", Cell: "A1",
	}, testStart.Add(30*time.Second))

	state = transition.State
	if state.Phase != PhaseTBPreparation || state.Turn != 15 || state.ActiveTeam != "" {
		t.Fatalf("forced TB state = phase %q turn %d active %q", state.Phase, state.Turn, state.ActiveTeam)
	}
	if state.TBEntry == nil || state.TBEntry.Basis != TBBasisForcedAfterRobberies || state.TBEntry.RequestID != "" {
		t.Fatalf("forced TB evidence = %+v", state.TBEntry)
	}
	assertTimer(t, state.Timer, testStart.Add(30*time.Second), TBPreparationDuration)
	assertEventTypes(t, transition.Events,
		EventShiroPlaced, EventTurnAdvanced, EventTBForced, EventTBPreparationStarted, EventTimerStarted)
}

func TestTurnFifteenDoesNotForceTBBeforeBothTeamsRob(t *testing.T) {
	t.Parallel()

	state := stateAtTurn(t, 14)
	state.RobberyUsed[TeamRed] = true
	state = mustExecute(t, state, CaptainActor(TeamRed), RequestTB{
		RequestID: "expires-at-turn-15", Basis: TBBasisCaptainAgreement,
	}, testStart.Add(29*time.Second)).State
	transition := mustExecute(t, state, StrategistActor(state.ActiveTeam), PlaceShiro{
		PieceID: "turn-14-shiro", Cell: "A1",
	}, testStart.Add(30*time.Second))

	assertStateHeader(t, transition.State, LifecycleRunning, PhasePick, 15, TeamBlue)
	if transition.State.TBEntry != nil {
		t.Fatalf("TB started before both robberies: %+v", transition.State.TBEntry)
	}
	if transition.State.PendingTBRequest != nil {
		t.Fatalf("turn-14 TB request survived outside the negotiation window: %+v", transition.State.PendingTBRequest)
	}
	assertEventTypes(t, transition.Events,
		EventShiroPlaced, EventTurnAdvanced, EventTBRequestExpired, EventTimerStarted)
	expired := transition.Events[2]
	if expired.RequestID != "expires-at-turn-15" || expired.Team != TeamRed || expired.Basis != TBBasisCaptainAgreement {
		t.Fatalf("expired TB request event evidence = %+v", expired)
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
	want := mustExecute(t, state, CaptainActor(TeamBlue), command, testStart.Add(31*time.Second))
	got := mustExecute(t, restored, CaptainActor(TeamBlue), command, testStart.Add(31*time.Second))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TB response after restore differs\n got: %#v\nwant: %#v", got, want)
	}
}

func stateAtTurn(t *testing.T, turn int) State {
	t.Helper()
	state := stateAtFirstPick(t)
	state.Turn = turn
	state.ActiveTeam = pickTeam(state.FirstPick, state.Turn)
	state.Timer = Timer{StartedAt: testStart.Add(20 * time.Second), Duration: PickDuration}
	return state
}

func stateAtTurn13(t *testing.T) State {
	t.Helper()
	return stateAtTurn(t, 13)
}

func requestedTBState(t *testing.T) State {
	t.Helper()
	return mustExecute(t, stateAtTurn13(t), CaptainActor(TeamRed), RequestTB{
		RequestID: "tb-request-1", Basis: TBBasisCaptainAgreement,
	}, testStart.Add(30*time.Second)).State
}

func acceptedTBState(t *testing.T) State {
	t.Helper()
	return mustExecute(t, requestedTBState(t), CaptainActor(TeamBlue), RespondTBRequest{
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
