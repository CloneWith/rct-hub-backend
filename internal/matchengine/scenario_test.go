package matchengine

import (
	"testing"
	"time"
)

func TestComplexMatchScenarioPauseRobberyAndNegotiatedTB(t *testing.T) {
	t.Parallel()

	configuration := testConfiguration()
	for index := 9; index <= 20; index++ {
		configuration.PoolSlots = append(configuration.PoolSlots, PoolSlot{ID: "NM" + twoDigitless(index), Mod: ModNM})
	}
	state, err := NewReadyState(configuration)
	if err != nil {
		t.Fatalf("NewReadyState: %v", err)
	}
	now := testStart
	var allEvents []Event
	apply := func(actor Actor, command Command) {
		t.Helper()
		now = now.Add(time.Second)
		transition := mustExecute(t, state, actor, command, now)
		state = transition.State
		allEvents = append(allEvents, transition.Events...)
	}

	apply(RefereeActor(), StartMatch{})
	for _, ban := range []struct {
		team TeamSide
		slot string
	}{{TeamRed, "NM1"}, {TeamBlue, "NM2"}, {TeamBlue, "NM3"}, {TeamRed, "NM4"}} {
		apply(StrategistActor(ban.team), BanPoolSlot{PoolSlotID: ban.slot})
	}

	opening := []struct {
		cell   Cell
		winner TeamSide
	}{
		{"A1", TeamBlue}, {"D4", TeamRed}, {"B1", TeamBlue},
		{"D2", TeamBlue}, {"C1", TeamBlue}, {"D3", TeamBlue},
	}
	for index, placement := range opening {
		pieceID := "piece-" + twoDigitless(index+1)
		apply(StrategistActor(state.ActiveTeam), PlacePiece{
			PoolSlotID: "NM" + twoDigitless(index+5), PieceID: pieceID, Cell: placement.cell,
		})
		apply(RefereeActor(), ConfirmBeatmapResult{BoardPieceID: pieceID, WinningTeam: placement.winner})
	}
	if state.Turn != 7 || state.ActiveTeam != TeamBlue {
		t.Fatalf("opening ended at turn %d active %q", state.Turn, state.ActiveTeam)
	}

	apply(RefereeActor(), PauseTimer{Reason: "network verification"})
	apply(RefereeActor(), ResumeTimer{Reason: "network stable"})
	apply(StrategistActor(TeamBlue), RobPiece{
		TargetPieceID: "piece-2",
		SacrificeSets: [][]string{{"piece-1", "piece-3", "piece-5"}},
	})
	if !state.RobberyUsed[TeamBlue] || state.Turn != 7 {
		t.Fatalf("robbery state = used %v turn %d", state.RobberyUsed[TeamBlue], state.Turn)
	}

	closing := []struct {
		cell   Cell
		winner TeamSide
	}{
		{"A2", TeamRed}, {"B2", TeamBlue}, {"C2", TeamRed},
		{"A3", TeamRed}, {"B3", TeamBlue}, {"C3", TeamRed},
	}
	for index, placement := range closing {
		pieceNumber := index + 7
		pieceID := "piece-" + twoDigitless(pieceNumber)
		apply(StrategistActor(state.ActiveTeam), PlacePiece{
			PoolSlotID: "NM" + twoDigitless(index+11), PieceID: pieceID, Cell: placement.cell,
		})
		apply(RefereeActor(), ConfirmBeatmapResult{BoardPieceID: pieceID, WinningTeam: placement.winner})
	}
	if state.Turn != 13 || state.Lifecycle != LifecycleRunning {
		t.Fatalf("pre-TB state = lifecycle %q turn %d", state.Lifecycle, state.Turn)
	}

	apply(CaptainActor(TeamRed), RequestTB{RequestID: "complex-tb", Basis: TBBasisCaptainAgreement})
	apply(CaptainActor(TeamBlue), RespondTBRequest{RequestID: "complex-tb", Accept: true})
	apply(RefereeActor(), StartTB{})
	apply(RefereeActor(), ConfirmTBResult{WinningTeam: TeamRed})
	assertTerminalResult(t, state, TeamRed, ResultReasonTB)

	for _, expected := range []EventType{
		EventTimerPaused, EventTimerResumed, EventPieceRobbed,
		EventTBRequested, EventTBPreparationStarted, EventTBStarted, EventTBResultConfirmed,
	} {
		if !hasEventType(allEvents, expected) {
			t.Fatalf("complex scenario missing event %q", expected)
		}
	}
}

func hasEventType(events []Event, wanted EventType) bool {
	for _, event := range events {
		if event.Type == wanted {
			return true
		}
	}
	return false
}

func twoDigitless(value int) string {
	if value < 10 {
		return string(rune('0' + value))
	}
	return string([]rune{rune('0' + value/10), rune('0' + value%10)})
}
