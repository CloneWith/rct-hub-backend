package matchengine

import (
	"reflect"
	"testing"
	"time"
)

func TestWhitespaceOnlyAuditReasonsAreRejectedWithoutMutation(t *testing.T) {
	t.Parallel()

	base := stateAtFirstPick(t)
	paused := mustExecute(t, base, RefereeActor(), PauseTimer{Reason: "technical"}, testStart.Add(time.Second)).State
	suspended := mustExecute(t, base, RefereeActor(), SuspendMatch{Reason: "incident"}, testStart.Add(time.Second)).State
	preparing := acceptedTBState(t)

	tests := []struct {
		name    string
		state   State
		command Command
		now     time.Time
	}{
		{name: "additional time", state: base, command: GrantAdditionalTime{Reason: " \t\n"}, now: base.Timer.StartedAt.Add(base.Timer.Duration)},
		{name: "timer calibration", state: base, command: CalibrateTimer{Remaining: time.Second, Reason: " \t\n"}, now: testStart.Add(time.Second)},
		{name: "timer pause", state: base, command: PauseTimer{Reason: " \t\n"}, now: testStart.Add(time.Second)},
		{name: "timer resume", state: paused, command: ResumeTimer{Reason: " \t\n"}, now: testStart.Add(2 * time.Second)},
		{name: "match suspension", state: base, command: SuspendMatch{Reason: " \t\n"}, now: testStart.Add(time.Second)},
		{name: "match resume", state: suspended, command: ResumeMatch{Reason: " \t\n"}, now: testStart.Add(2 * time.Second)},
		{name: "action skip", state: base, command: SkipCurrentAction{Reason: " \t\n"}, now: base.Timer.StartedAt.Add(base.Timer.Duration)},
		{name: "match abort", state: base, command: AbortMatch{Reason: " \t\n"}, now: testStart.Add(time.Second)},
		{name: "referee proxy", state: base, command: RefereePlaceShiro{ActingTeam: TeamBlue, PieceID: "proxy-shiro", Cell: "A1", Reason: " \t\n"}, now: base.Timer.StartedAt.Add(base.Timer.Duration)},
		{name: "expired TB preparation", state: preparing, command: StartTB{Reason: " \t\n"}, now: preparing.Timer.StartedAt.Add(preparing.Timer.Duration)},
		{name: "surrender", state: base, command: RecordSurrender{SurrenderingTeam: TeamRed, ConfirmingPlayerIDs: []int64{1001, 1002, 1003, 1004}, Reason: " \t\n"}, now: testStart.Add(time.Second)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.state.Clone()
			_, err := Execute(tt.state, RefereeActor(), tt.command, tt.now)
			assertErrorCode(t, err, CodeInvalidRequest)
			if !reflect.DeepEqual(tt.state, before) {
				t.Fatal("rejected whitespace-only reason mutated state")
			}
		})
	}
}
