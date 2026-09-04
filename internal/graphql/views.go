package graphql

import (
	"time"

	"rctHubBackend/internal/matchengine"
)

func computeStrategistView(state matchengine.State, team matchengine.TeamSide, now time.Time) *StrategistView {
	return &StrategistView{
		IsMyTurn: state.Lifecycle == matchengine.LifecycleRunning && state.ActiveTeam == team,
		MyTeam:   gqlTeam(team),
		Analysis: mapActorAnalysis(matchengine.AnalyzeForActor(state, matchengine.StrategistActor(team), now)),
	}
}

func computeCaptainView(state matchengine.State, team matchengine.TeamSide, now time.Time) *CaptainView {
	return &CaptainView{MyTeam: gqlTeam(team), Analysis: mapActorAnalysis(matchengine.AnalyzeForActor(state, matchengine.CaptainActor(team), now))}
}

func computeSpectatorView(snapshot *MatchSnapshot) *SpectatorView {
	return &SpectatorView{
		Board: snapshot.Board, WonCounts: snapshot.WonCounts,
		CurrentPhase: snapshot.Phase, ActiveTeam: snapshot.ActiveTeam,
		TurnNumber: snapshot.Turn, Lifecycle: snapshot.Lifecycle,
	}
}

func computeOverlayView(snapshot *MatchSnapshot) *OverlayView {
	return &OverlayView{
		Board: snapshot.Board, WonCounts: snapshot.WonCounts,
		Timer: snapshot.Timer, Lifecycle: snapshot.Lifecycle,
		Phase: snapshot.Phase, ActiveTeam: snapshot.ActiveTeam,
	}
}

// computeRefereeView builds the referee-facing read model.
//
// Two-phase start gate: the referee may only fire START_MATCH once both
// strategists have confirmed readiness (status == MatchStatusReady). When
// the match is still PENDING, START_MATCH is filtered out of allowedActions
// so the referee console renders a "等待双方策略师准备" state instead.
//
// (The orchestrator also rejects START_MATCH with status==PENDING as an
// authoritative guard; the UI filter here is purely a UX hint.)
func computeRefereeView(matchID string, status MatchStatus, state matchengine.State, now time.Time) *RefereeView {
	analysis := mapActorAnalysis(matchengine.AnalyzeForActor(state, matchengine.RefereeActor(), now))
	if status != MatchStatusReady {
		analysis.AllowedActions = dropMatchAction(analysis.AllowedActions, MatchActionStartMatch)
	}
	view := &RefereeView{
		MatchID:  matchID,
		Snapshot: mapMatchSnapshot(state),
		Analysis: analysis,
		AuditLog: []*AuditEntry{},
	}
	if state.Suspension != nil {
		view.SuspensionReason = optionalString(state.Suspension.Reason)
	}
	view.AbortReason = optionalString(state.AbortReason)
	return view
}

// dropMatchAction returns a copy of `actions` with every occurrence of
// `target` removed. Pure helper — does not mutate the input slice.
func dropMatchAction(actions []MatchAction, target MatchAction) []MatchAction {
	out := make([]MatchAction, 0, len(actions))
	for _, a := range actions {
		if a != target {
			out = append(out, a)
		}
	}
	return out
}
