package matchengine

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestRefereeCalibratesRunningAndPausedTimers(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	now := testStart.Add(10 * time.Second)
	transition := mustExecute(t, state, RefereeActor(), CalibrateTimer{
		Remaining: 42 * time.Second, Reason: "stream clock cross-check",
	}, now)
	state = transition.State
	assertTimer(t, state.Timer, now, 42*time.Second)
	assertEventTypes(t, transition.Events, EventTimerCalibrated)
	if transition.Events[0].Duration != 42*time.Second || transition.Events[0].Reason == "" {
		t.Fatalf("calibration event = %+v", transition.Events[0])
	}

	state = mustExecute(t, state, RefereeActor(), PauseTimer{Reason: "technical"}, now.Add(time.Second)).State
	transition = mustExecute(t, state, RefereeActor(), CalibrateTimer{
		Remaining: 17 * time.Second, Reason: "paused clock correction",
	}, now.Add(2*time.Second))
	if !transition.State.Timer.Paused || transition.State.Timer.Remaining(now.Add(time.Hour)) != 17*time.Second {
		t.Fatalf("paused calibrated timer = %+v", transition.State.Timer)
	}
}

func TestCalibrateTimerRejectsInvalidRequestsWithoutMutation(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	tests := []struct {
		name  string
		actor Actor
		cmd   CalibrateTimer
		code  ErrorCode
	}{
		{name: "strategist", actor: StrategistActor(TeamBlue), cmd: CalibrateTimer{Remaining: time.Second, Reason: "no"}, code: CodeActionNotAllowed},
		{name: "negative", actor: RefereeActor(), cmd: CalibrateTimer{Remaining: -time.Second, Reason: "bad"}, code: CodeInvalidRequest},
		{name: "reason required", actor: RefereeActor(), cmd: CalibrateTimer{Remaining: time.Second}, code: CodeInvalidRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := state.Clone()
			_, err := Execute(state, tt.actor, tt.cmd, testStart.Add(time.Second))
			assertErrorCode(t, err, tt.code)
			if !reflect.DeepEqual(state, before) {
				t.Fatal("failed calibration mutated state")
			}
		})
	}
}

func TestSuspendResumeFreezesRunningTimerAndPreservesExistingPause(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	suspendAt := testStart.Add(14 * time.Second)
	transition := mustExecute(t, state, RefereeActor(), SuspendMatch{Reason: "venue network incident"}, suspendAt)
	state = transition.State
	if state.Lifecycle != LifecycleSuspended || state.Suspension == nil || state.Timer.Remaining(suspendAt.Add(time.Hour)) != 80*time.Second {
		t.Fatalf("suspended state = %+v", state)
	}
	assertEventTypes(t, transition.Events, EventMatchSuspended, EventTimerPaused)

	resumeAt := suspendAt.Add(time.Hour)
	transition = mustExecute(t, state, RefereeActor(), ResumeMatch{Reason: "network stable"}, resumeAt)
	state = transition.State
	if state.Lifecycle != LifecycleRunning || state.Suspension != nil || state.Timer.Paused {
		t.Fatalf("resumed state = %+v", state)
	}
	if state.Timer.Remaining(resumeAt) != 80*time.Second {
		t.Fatalf("resumed remainder = %s, want 80s", state.Timer.Remaining(resumeAt))
	}
	assertEventTypes(t, transition.Events, EventMatchResumed, EventTimerResumed)

	paused := mustExecute(t, state, RefereeActor(), PauseTimer{Reason: "manual review"}, resumeAt.Add(time.Second)).State
	paused = mustExecute(t, paused, RefereeActor(), SuspendMatch{Reason: "escalate"}, resumeAt.Add(2*time.Second)).State
	paused = mustExecute(t, paused, RefereeActor(), ResumeMatch{Reason: "return to review"}, resumeAt.Add(time.Hour)).State
	if !paused.Timer.Paused {
		t.Fatal("match resume silently resumed an operationally paused timer")
	}
}

func TestSuspendedStateSurvivesJSONAndRejectsOrdinaryWrites(t *testing.T) {
	t.Parallel()

	state := mustExecute(t, stateAtFirstPick(t), RefereeActor(), SuspendMatch{Reason: "review"}, testStart.Add(5*time.Second)).State
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var restored State
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(restored, state) {
		t.Fatalf("restored suspension differs\n got: %#v\nwant: %#v", restored, state)
	}

	_, err = Execute(restored, StrategistActor(TeamBlue), PlacePiece{
		PoolSlotID: "NM5", PieceID: "blocked", Cell: "A1",
	}, testStart.Add(6*time.Second))
	assertErrorCode(t, err, CodeMatchLifecycleConflict)
}

func TestRefereeAbortsRunningOrSuspendedMatchWithoutWinner(t *testing.T) {
	t.Parallel()

	for _, suspended := range []bool{false, true} {
		state := stateAtFirstPick(t)
		if suspended {
			state = mustExecute(t, state, RefereeActor(), SuspendMatch{Reason: "review"}, testStart.Add(5*time.Second)).State
		}
		transition := mustExecute(t, state, RefereeActor(), AbortMatch{Reason: "organizer voided match"}, testStart.Add(6*time.Second))
		got := transition.State
		if got.Lifecycle != LifecycleAborted || got.Phase != PhaseNone || got.Winner != nil || got.Result != nil || got.AbortReason == "" {
			t.Fatalf("aborted state = %+v", got)
		}
		assertEventTypes(t, transition.Events, EventMatchAborted)
	}
}

func TestSkipExpiredBanAndPickAdvancesWithoutFabricatingResult(t *testing.T) {
	t.Parallel()

	ban := mustExecute(t, newReadyState(t), RefereeActor(), StartMatch{}, testStart).State
	transition := mustExecute(t, ban, RefereeActor(), SkipCurrentAction{Reason: "team unavailable"}, testStart.Add(BanDuration))
	assertStateHeader(t, transition.State, LifecycleRunning, PhaseBan, -2, TeamBlue)
	assertEventTypes(t, transition.Events, EventActionSkipped, EventTurnAdvanced, EventTimerStarted)

	pick := stateAtFirstPick(t)
	transition = mustExecute(t, pick, RefereeActor(), SkipCurrentAction{Reason: "pause already consumed"}, pick.Timer.StartedAt.Add(PickDuration))
	assertStateHeader(t, transition.State, LifecycleRunning, PhasePick, 2, TeamRed)
	if transition.State.PendingPieceID != "" {
		t.Fatal("skip fabricated a pending board piece")
	}
	assertEventTypes(t, transition.Events, EventActionSkipped, EventTurnAdvanced, EventTimerStarted)
}

func TestSkipRejectsLiveTimerAndPendingResult(t *testing.T) {
	t.Parallel()

	pick := stateAtFirstPick(t)
	before := pick.Clone()
	_, err := Execute(pick, RefereeActor(), SkipCurrentAction{Reason: "too early"}, pick.Timer.StartedAt.Add(time.Second))
	assertErrorCode(t, err, CodeActionNotAllowed)
	if !reflect.DeepEqual(pick, before) {
		t.Fatal("failed live skip mutated state")
	}

	waiting := mustExecute(t, pick, StrategistActor(TeamBlue), PlacePiece{
		PoolSlotID: "NM5", PieceID: "played", Cell: "A1",
	}, pick.Timer.StartedAt.Add(time.Second)).State
	_, err = Execute(waiting, RefereeActor(), SkipCurrentAction{Reason: "cannot discard result"}, waiting.Timer.StartedAt.Add(ResultConfirmationDuration))
	assertErrorCode(t, err, CodeResultNotPending)
}
