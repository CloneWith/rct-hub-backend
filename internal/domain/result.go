package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// WinReason explains how the match ended.
type WinReason string

const (
	WinReasonFourInARow WinReason = "four_in_a_row" // alignment win
	WinReasonTB         WinReason = "tie_breaker"   // tie-breaker win
	WinReasonSurrender  WinReason = "surrender"     // team surrendered
	WinReasonDraw       WinReason = "draw"          // no moves left, equal pieces and alignment score
	WinReasonForfeit    WinReason = "forfeit"       // timer/admin forfeit
)

// Result stores the final outcome of a match.
type Result struct {
	ID         bson.ObjectID      `json:"id" bson:"_id,omitempty"`
	MatchID    bson.ObjectID      `json:"match_id" bson:"match_id"`
	RoomID     bson.ObjectID      `json:"room_id" bson:"room_id"`
	WinnerID   *TeamSide          `json:"winner_id,omitempty" bson:"winner_id,omitempty"`
	WinReason  WinReason          `json:"win_reason" bson:"win_reason"`
	Scores     []TeamScore        `json:"scores" bson:"scores"`
	WonPieces  map[TeamSide]int   `json:"won_pieces" bson:"won_pieces"`
	Alignments []WinningAlignment `json:"alignments,omitempty" bson:"alignments,omitempty"`
	Summary    string             `json:"summary" bson:"summary"`
	CreatedAt  time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at" bson:"updated_at"`
}

// TeamScore records a team's final score in a match.
type TeamScore struct {
	TeamID TeamSide `json:"team_id" bson:"team_id"`
	Score  int      `json:"score" bson:"score"`
}

// NewResult creates a result from a finished match.
func NewResult(match Match, reason WinReason, winner *TeamSide) Result {
	now := time.Now()
	won := match.CountWonPieces()
	scores := []TeamScore{
		{TeamID: TeamSideRed, Score: won[TeamSideRed]},
		{TeamID: TeamSideBlue, Score: won[TeamSideBlue]},
	}
	return Result{
		MatchID:    match.ID,
		RoomID:     match.RoomID,
		WinnerID:   winner,
		WinReason:  reason,
		Scores:     scores,
		WonPieces:  won,
		Alignments: match.Board.FindAlignments(string(TeamSideRed), 2),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

// IsDraw reports whether the match ended without a winner.
func (r Result) IsDraw() bool {
	return r.WinnerID == nil
}
