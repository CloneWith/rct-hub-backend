package matchengine

import (
	"reflect"
	"testing"
	"time"
)

func TestAcceptedCommandAdvancesVersionExactlyOnceAndFailureDoesNot(t *testing.T) {
	t.Parallel()

	ready := newReadyState(t)
	if ready.Version != 0 {
		t.Fatalf("ready version = %d, want 0", ready.Version)
	}
	started := mustExecute(t, ready, RefereeActor(), StartMatch{}, testStart).State
	if started.Version != 1 {
		t.Fatalf("started version = %d, want 1", started.Version)
	}
	before := started.Clone()
	_, err := Execute(started, StrategistActor(TeamBlue), BanPoolSlot{PoolSlotID: "NM1"}, testStart.Add(time.Second))
	assertErrorCode(t, err, CodeNotActiveTeam)
	if started.Version != 1 || !reflect.DeepEqual(started, before) {
		t.Fatal("failed command changed state or version")
	}
	accepted := mustExecute(t, started, StrategistActor(TeamRed), BanPoolSlot{PoolSlotID: "NM1"}, testStart.Add(time.Second)).State
	if accepted.Version != 2 {
		t.Fatalf("accepted version = %d, want 2", accepted.Version)
	}
}

func TestConfiguredTimerPresetDrivesEveryTimer(t *testing.T) {
	t.Parallel()

	configuration := testConfiguration()
	configuration.Timers = TimerConfiguration{
		PresetID:                     "TEST_FAST",
		Ban:                          11 * time.Second,
		BanAdditional:                3 * time.Second,
		Pick:                         13 * time.Second,
		PickAdditional:               5 * time.Second,
		ResultConfirmation:           7 * time.Second,
		ResultConfirmationAdditional: 2 * time.Second,
		TBPreparation:                17 * time.Second,
	}
	state, err := NewReadyState(configuration)
	if err != nil {
		t.Fatalf("NewReadyState: %v", err)
	}
	state = mustExecute(t, state, RefereeActor(), StartMatch{}, testStart).State
	assertTimer(t, state.Timer, testStart, 11*time.Second)

	grantAt := testStart.Add(11 * time.Second)
	state = mustExecute(t, state, RefereeActor(), GrantAdditionalTime{Reason: "timeout reviewed"}, grantAt).State
	assertTimer(t, state.Timer, grantAt, 3*time.Second)
}

func TestNewReadyStateRejectsInvalidTimerConfiguration(t *testing.T) {
	t.Parallel()

	configuration := testConfiguration()
	configuration.Timers.Pick = 0
	_, err := NewReadyState(configuration)
	assertErrorCode(t, err, CodeInvalidRequest)
}
