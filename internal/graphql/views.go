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

func computeRefereeView(matchID string, state matchengine.State, now time.Time) *RefereeView {
	view := &RefereeView{
		MatchID:  matchID,
		Snapshot: mapMatchSnapshot(state),
		Analysis: mapActorAnalysis(matchengine.AnalyzeForActor(state, matchengine.RefereeActor(), now)),
		AuditLog: []*AuditEntry{},
	}
	if state.Suspension != nil {
		view.SuspensionReason = optionalString(state.Suspension.Reason)
	}
	view.AbortReason = optionalString(state.AbortReason)
	return view
}
