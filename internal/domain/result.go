package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// WinReason is an alias for the rule-engine ResultReason.
// Deprecated: use ResultReason instead.
type WinReason = ResultReason

const (
	WinReasonFourInARow = ResultReasonFourAlignment
	WinReasonTB         = ResultReasonTB
	WinReasonSurrender  = ResultReasonSurrender
	WinReasonDraw       = ResultReasonStalemateWonCount
	WinReasonForfeit    = "FORFEIT"
)

// MatchResult is a persisted result document for a completed match.
// It embeds the engine Result as the core outcome.
type MatchResult struct {
	ID         bson.ObjectID    `json:"id" bson:"_id,omitempty"`
	MatchID    bson.ObjectID    `json:"match_id" bson:"match_id"`
	RoomID     bson.ObjectID    `json:"room_id" bson:"room_id"`
	WinnerID   *TeamSide        `json:"winner_id,omitempty" bson:"winner_id,omitempty"`
	WinReason  WinReason        `json:"win_reason" bson:"win_reason"`
	Scores     []TeamScore      `json:"scores" bson:"scores"`
	WonPieces  map[TeamSide]int `json:"won_pieces" bson:"won_pieces"`
	Alignments []Alignment      `json:"alignments,omitempty" bson:"alignments,omitempty"`
	Summary    string           `json:"summary" bson:"summary"`
	CreatedAt  time.Time        `json:"created_at" bson:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at" bson:"updated_at"`
}

// TeamScore records a team's final score in a match.
type TeamScore struct {
	TeamID TeamSide `json:"team_id" bson:"team_id"`
	Score  int      `json:"score" bson:"score"`
}

// NewMatchResult creates a persisted result from a finished match.
func NewMatchResult(match Match, reason WinReason, winner *TeamSide) MatchResult {
	now := time.Now()
	won := match.CountWonPieces()
	scores := []TeamScore{
		{TeamID: TeamSideRed, Score: won[TeamSideRed]},
		{TeamID: TeamSideBlue, Score: won[TeamSideBlue]},
	}
	return MatchResult{
		MatchID:   match.ID,
		RoomID:    match.RoomID,
		WinnerID:  winner,
		WinReason: reason,
		Scores:    scores,
		WonPieces: won,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// IsDraw reports whether the match ended without a winner.
func (r MatchResult) IsDraw() bool {
	return r.WinnerID == nil
}
