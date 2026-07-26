package matchengine

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestExpiredTeamTimerDoesNotAdvanceState(t *testing.T) {
	t.Parallel()

	state := mustExecute(t, newReadyState(t), RefereeActor(), StartMatch{}, testStart).State
	before := state.Clone()
	_, err := Execute(state, StrategistActor(TeamRed), BanPoolSlot{PoolSlotID: "NM1"}, testStart.Add(BanDuration))
	assertErrorCode(t, err, CodeTimerExpired)
	if !reflect.DeepEqual(state, before) {
		t.Fatal("timer expiry or rejected command changed formal state")
	}
	assertStateHeader(t, state, LifecycleRunning, PhaseBan, -3, TeamRed)
}

func TestRefereeGrantsConfiguredBanAdditionalTimeAfterExpiry(t *testing.T) {
	t.Parallel()

	state := mustExecute(t, newReadyState(t), RefereeActor(), StartMatch{}, testStart).State
	deadline := testStart.Add(BanDuration)
	transition := mustExecute(t, state, RefereeActor(), GrantAdditionalTime{Reason: "timeout reviewed"}, deadline)
	state = transition.State

	assertStateHeader(t, state, LifecycleRunning, PhaseBan, -3, TeamRed)
	assertTimer(t, state.Timer, deadline, BanAdditionalDuration)
	if !state.TeamPauseUsed[TeamRed] || state.TeamPauseUsed[TeamBlue] {
		t.Fatalf("team pause usage = %#v, want RED only", state.TeamPauseUsed)
	}
	assertEventTypes(t, transition.Events, EventAdditionalTimeGranted, EventTimerStarted)
	if transition.Events[0].Duration != BanAdditionalDuration || transition.Events[0].Reason != "timeout reviewed" {
		t.Fatalf("additional-time event = %+v, want duration and audit reason", transition.Events[0])
	}

	_ = mustExecute(t, state, StrategistActor(TeamRed), BanPoolSlot{PoolSlotID: "NM1"}, deadline.Add(BanAdditionalDuration-time.Second))
	_, err := Execute(state, StrategistActor(TeamRed), BanPoolSlot{PoolSlotID: "NM1"}, deadline.Add(BanAdditionalDuration))
	assertErrorCode(t, err, CodeTimerExpired)
}

func TestRefereeGrantsConfiguredPickAdditionalTime(t *testing.T) {
	t.Parallel()

	state := stateAtFirstPick(t)
	deadline := state.Timer.StartedAt.Add(PickDuration)
	transition := mustExecute(t, state, RefereeActor(), GrantAdditionalTime{Reason: "team pause"}, deadline)
	state = transition.State

	assertStateHeader(t, state, LifecycleRunning, PhasePick, 1, TeamBlue)
	assertTimer(t, state.Timer, deadline, PickAdditionalDuration)
	if !state.TeamPauseUsed[TeamBlue] || state.TeamPauseUsed[TeamRed] {
		t.Fatalf("team pause usage = %#v, want BLUE only", state.TeamPauseUsed)
	}
}

func TestTeamPauseEntitlementsAreIndependentAndSingleUse(t *testing.T) {
	t.Parallel()

	state := mustExecute(t, newReadyState(t), RefereeActor(), StartMatch{}, testStart).State
	redGrantAt := testStart.Add(BanDuration)
	state = mustExecute(t, state, RefereeActor(), GrantAdditionalTime{Reason: "red pause"}, redGrantAt).State
	redBanAt := redGrantAt.Add(time.Second)
	state = mustExecute(t, state, StrategistActor(TeamRed), BanPoolSlot{PoolSlotID: "NM1"}, redBanAt).State

	blueGrantAt := redBanAt.Add(BanDuration)
	state = mustExecute(t, state, RefereeActor(), GrantAdditionalTime{Reason: "blue pause"}, blueGrantAt).State
	if !state.TeamPauseUsed[TeamRed] || !state.TeamPauseUsed[TeamBlue] {
		t.Fatalf("team pause usage = %#v, want both teams used", state.TeamPauseUsed)
	}

	state = mustExecute(t, state, StrategistActor(TeamBlue), BanPoolSlot{PoolSlotID: "NM2"}, blueGrantAt.Add(time.Second)).State
	state = mustExecute(t, state, StrategistActor(TeamBlue), BanPoolSlot{PoolSlotID: "NM3"}, blueGrantAt.Add(2*time.Second)).State
	redSecondDeadline := blueGrantAt.Add(2*time.Second + BanDuration)
	before := state.Clone()
	_, err := Execute(state, RefereeActor(), GrantAdditionalTime{Reason: "second red pause"}, redSecondDeadline)
	assertErrorCode(t, err, CodeTeamPauseAlreadyUsed)
	if !reflect.DeepEqual(state, before) {
		t.Fatal("rejected second team pause mutated state")
	}
}

func TestGrantAdditionalTimeRejectsInvalidRequestsWithoutMutation(t *testing.T) {
	t.Parallel()

	started := mustExecute(t, newReadyState(t), RefereeActor(), StartMatch{}, testStart).State
	waiting := started.Clone()
	waiting.Phase = PhaseWaitingForResult
	paused := mustExecute(t, started, RefereeActor(), PauseTimer{Reason: "technical"}, testStart.Add(time.Second)).State
	tests := []struct {
		name  string
		state State
		actor Actor
		now   time.Time
		cmd   GrantAdditionalTime
		code  ErrorCode
	}{
		{name: "before expiry", state: started, actor: RefereeActor(), now: testStart.Add(time.Second), cmd: GrantAdditionalTime{Reason: "too early"}, code: CodeActionNotAllowed},
		{name: "strategist cannot grant", state: started, actor: StrategistActor(TeamRed), now: testStart.Add(BanDuration), cmd: GrantAdditionalTime{Reason: "self grant"}, code: CodeActionNotAllowed},
		{name: "reason required", state: started, actor: RefereeActor(), now: testStart.Add(BanDuration), cmd: GrantAdditionalTime{}, code: CodeInvalidRequest},
		{name: "not a team action phase", state: waiting, actor: RefereeActor(), now: testStart.Add(BanDuration), cmd: GrantAdditionalTime{Reason: "result timer"}, code: CodeActionNotAllowed},
		{name: "positive paused timer is not expired", state: paused, actor: RefereeActor(), now: testStart.Add(time.Hour), cmd: GrantAdditionalTime{Reason: "bypass attempt"}, code: CodeActionNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.state.Clone()
			_, err := Execute(tt.state, tt.actor, tt.cmd, tt.now)
			assertErrorCode(t, err, tt.code)
			if !reflect.DeepEqual(tt.state, before) {
				t.Fatal("failed additional-time grant mutated state")
			}
		})
	}
}

func TestOperationalPauseResumeFreezesRemainingTimeWithoutTeamEntitlement(t *testing.T) {
	t.Parallel()

	state := mustExecute(t, newReadyState(t), RefereeActor(), StartMatch{}, testStart).State
	pauseAt := testStart.Add(10 * time.Second)
	paused := mustExecute(t, state, RefereeActor(), PauseTimer{Reason: "technical check"}, pauseAt)
	state = paused.State

	if !state.Timer.Paused || state.Timer.Remaining(testStart.Add(time.Hour)) != 50*time.Second {
		t.Fatalf("paused timer = %+v, want frozen 50s", state.Timer)
	}
	if state.TeamPauseUsed[TeamRed] || state.TeamPauseUsed[TeamBlue] {
		t.Fatalf("operational pause consumed team entitlement: %#v", state.TeamPauseUsed)
	}
	assertEventTypes(t, paused.Events, EventTimerPaused)
	if paused.Events[0].Reason != "technical check" {
		t.Fatalf("pause event reason = %q", paused.Events[0].Reason)
	}

	before := state.Clone()
	_, err := Execute(state, StrategistActor(TeamRed), BanPoolSlot{PoolSlotID: "NM1"}, pauseAt.Add(time.Second))
	assertErrorCode(t, err, CodeTimerPaused)
	if !reflect.DeepEqual(state, before) {
		t.Fatal("strategist command during pause mutated state")
	}

	resumeAt := testStart.Add(100 * time.Second)
	resumed := mustExecute(t, state, RefereeActor(), ResumeTimer{Reason: "technical check complete"}, resumeAt)
	state = resumed.State
	if state.Timer.Paused {
		t.Fatal("timer remained paused after resume")
	}
	assertTimer(t, state.Timer, resumeAt, 50*time.Second)
	assertEventTypes(t, resumed.Events, EventTimerResumed)
	if resumed.Events[0].Reason != "technical check complete" {
		t.Fatalf("resume event reason = %q", resumed.Events[0].Reason)
	}

	_ = mustExecute(t, state, StrategistActor(TeamRed), BanPoolSlot{PoolSlotID: "NM1"}, resumeAt.Add(49*time.Second))
	_, err = Execute(state, StrategistActor(TeamRed), BanPoolSlot{PoolSlotID: "NM1"}, resumeAt.Add(50*time.Second))
	assertErrorCode(t, err, CodeTimerExpired)
}

func TestPauseCannotCreateTimeWhenNowPrecedesTimerAnchor(t *testing.T) {
	t.Parallel()

	state := mustExecute(t, newReadyState(t), RefereeActor(), StartMatch{}, testStart).State
	state = mustExecute(t, state, RefereeActor(), PauseTimer{Reason: "clock correction"}, testStart.Add(-time.Second)).State
	if remaining := state.Timer.Remaining(testStart.Add(time.Hour)); remaining != BanDuration {
		t.Fatalf("remaining after regressed time = %s, want capped %s", remaining, BanDuration)
	}
	resumeAt := testStart.Add(time.Hour)
	state = mustExecute(t, state, RefereeActor(), ResumeTimer{Reason: "clock corrected"}, resumeAt).State
	assertTimer(t, state.Timer, resumeAt, BanDuration)
}

func TestPauseAtExpiredTimerCannotCreatePlayableTime(t *testing.T) {
	t.Parallel()

	state := mustExecute(t, newReadyState(t), RefereeActor(), StartMatch{}, testStart).State
	pauseAt := testStart.Add(BanDuration + time.Second)
	state = mustExecute(t, state, RefereeActor(), PauseTimer{Reason: "review timeout"}, pauseAt).State
	if remaining := state.Timer.Remaining(pauseAt.Add(time.Hour)); remaining != 0 {
		t.Fatalf("expired paused timer remaining = %s, want 0", remaining)
	}
	resumeAt := pauseAt.Add(time.Minute)
	state = mustExecute(t, state, RefereeActor(), ResumeTimer{Reason: "review complete"}, resumeAt).State
	_, err := Execute(state, StrategistActor(TeamRed), BanPoolSlot{PoolSlotID: "NM1"}, resumeAt)
	assertErrorCode(t, err, CodeTimerExpired)

	state = mustExecute(t, state, RefereeActor(), GrantAdditionalTime{Reason: "approved team pause"}, resumeAt).State
	assertTimer(t, state.Timer, resumeAt, BanAdditionalDuration)
}

func TestTimerPauseAndTeamEntitlementSurviveJSONRoundTrip(t *testing.T) {
	t.Parallel()

	state := mustExecute(t, newReadyState(t), RefereeActor(), StartMatch{}, testStart).State
	grantAt := testStart.Add(BanDuration)
	state = mustExecute(t, state, RefereeActor(), GrantAdditionalTime{Reason: "team pause"}, grantAt).State
	state = mustExecute(t, state, RefereeActor(), PauseTimer{Reason: "broadcast interruption"}, grantAt.Add(5*time.Second)).State

	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal timer state: %v", err)
	}
	var restored State
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatalf("Unmarshal timer state: %v", err)
	}
	if !reflect.DeepEqual(restored, state) {
		t.Fatalf("restored timer state differs\n got: %#v\nwant: %#v", restored, state)
	}

	resumeAt := grantAt.Add(time.Hour)
	restored = mustExecute(t, restored, RefereeActor(), ResumeTimer{Reason: "broadcast restored"}, resumeAt).State
	assertTimer(t, restored.Timer, resumeAt, 10*time.Second)
	_, err = Execute(restored, RefereeActor(), GrantAdditionalTime{Reason: "duplicate"}, resumeAt.Add(10*time.Second))
	assertErrorCode(t, err, CodeTeamPauseAlreadyUsed)
}

func TestPauseResumeRejectInvalidRequestsWithoutMutation(t *testing.T) {
	t.Parallel()

	started := mustExecute(t, newReadyState(t), RefereeActor(), StartMatch{}, testStart).State
	tests := []struct {
		name  string
		state State
		actor Actor
		cmd   Command
		code  ErrorCode
	}{
		{name: "strategist pause", state: started, actor: StrategistActor(TeamRed), cmd: PauseTimer{Reason: "no"}, code: CodeActionNotAllowed},
		{name: "pause reason required", state: started, actor: RefereeActor(), cmd: PauseTimer{}, code: CodeInvalidRequest},
		{name: "resume running timer", state: started, actor: RefereeActor(), cmd: ResumeTimer{Reason: "not paused"}, code: CodeActionNotAllowed},
	}
	paused := mustExecute(t, started, RefereeActor(), PauseTimer{Reason: "technical"}, testStart.Add(time.Second)).State
	tests = append(tests,
		struct {
			name  string
			state State
			actor Actor
			cmd   Command
			code  ErrorCode
		}{name: "pause already paused", state: paused, actor: RefereeActor(), cmd: PauseTimer{Reason: "again"}, code: CodeActionNotAllowed},
		struct {
			name  string
			state State
			actor Actor
			cmd   Command
			code  ErrorCode
		}{name: "resume reason required", state: paused, actor: RefereeActor(), cmd: ResumeTimer{}, code: CodeInvalidRequest},
	)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.state.Clone()
			_, err := Execute(tt.state, tt.actor, tt.cmd, testStart.Add(2*time.Second))
			assertErrorCode(t, err, tt.code)
			if !reflect.DeepEqual(tt.state, before) {
				t.Fatal("failed pause/resume command mutated state")
			}
		})
	}
}
