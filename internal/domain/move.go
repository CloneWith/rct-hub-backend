package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// MoveType categorizes a move made on the board or during BP.
type MoveType string

const (
	MoveTypePick      MoveType = "pick"      // select and place a piece on the board
	MoveTypeUnpick    MoveType = "unpick"    // undo a pick (admin only)
	MoveTypeBan       MoveType = "ban"       // ban a piece from the pool
	MoveTypeUnban     MoveType = "unban"     // undo a ban (admin only)
	MoveTypeClaim     MoveType = "claim"     // claim ownership of a placed piece after comparison
	MoveTypeWin       MoveType = "win"       // mark a piece as won
	MoveTypeUnwin     MoveType = "unwin"     // undo a win marker (admin only)
	MoveTypeRob       MoveType = "rob"       // rob an opponent's piece, sacrificing one of your own
	MoveTypeUnrob     MoveType = "unrob"     // undo a rob (admin only)
	MoveTypeDead      MoveType = "dead"      // mark a piece as dead (sacrificed)
	MoveTypeUndead    MoveType = "undead"    // undo a dead marker (admin only)
	MoveTypeSurrender MoveType = "surrender" // team surrenders
)

// Move records a single action performed during a match.
type Move struct {
	ID         bson.ObjectID `json:"id" bson:"_id,omitempty"`
	MatchID    bson.ObjectID `json:"match_id" bson:"match_id"`
	RoomID     bson.ObjectID `json:"room_id" bson:"room_id"`
	Type       MoveType      `json:"type" bson:"type"`
	TeamSide   *TeamSide     `json:"team_side,omitempty" bson:"team_side,omitempty"`
	OperatorID int64         `json:"operator_id" bson:"operator_id"` // osu uid of strategist/referee

	Slot     *PoolSlot `json:"slot,omitempty" bson:"slot,omitempty"`
	From     *Position `json:"from,omitempty" bson:"from,omitempty"` // for rob/unrob: source cell
	To       *Position `json:"to,omitempty" bson:"to,omitempty"`     // for pick/claim/win
	ForceMod *ForceMod `json:"force_mod,omitempty" bson:"force_mod,omitempty"`

	// Optional score information for the beatmap played.
	RedScore  *PlayerScore `json:"red_score,omitempty" bson:"red_score,omitempty"`
	BlueScore *PlayerScore `json:"blue_score,omitempty" bson:"blue_score,omitempty"`

	Comment   string    `json:"comment" bson:"comment"`
	CreatedAt time.Time `json:"created_at" bson:"created_at"`
}

// Position is a coordinate on the match board.
type Position struct {
	X int `json:"x" bson:"x"`
	Y int `json:"y" bson:"y"`
}

// PlayerScore stores a single player's result on a beatmap.
type PlayerScore struct {
	UserID int64   `json:"user_id" bson:"user_id"`
	Score  int64   `json:"score" bson:"score"`
	Combo  int     `json:"combo" bson:"combo"`
	Acc    float64 `json:"accuracy" bson:"accuracy"`
}

// NewPickMove creates a move that places a piece on the board.
func NewPickMove(matchID, roomID bson.ObjectID, operator int64, side TeamSide, slot PoolSlot, to Position, forceMod *ForceMod) Move {
	now := time.Now()
	return Move{
		MatchID:    matchID,
		RoomID:     roomID,
		Type:       MoveTypePick,
		TeamSide:   &side,
		OperatorID: operator,
		Slot:       &slot,
		To:         &to,
		ForceMod:   forceMod,
		CreatedAt:  now,
	}
}

// NewBanMove creates a ban move.
func NewBanMove(matchID, roomID bson.ObjectID, operator int64, side TeamSide, slot PoolSlot) Move {
	now := time.Now()
	return Move{
		MatchID:    matchID,
		RoomID:     roomID,
		Type:       MoveTypeBan,
		TeamSide:   &side,
		OperatorID: operator,
		Slot:       &slot,
		CreatedAt:  now,
	}
}

// NewRobMove creates a rob move.
func NewRobMove(matchID, roomID bson.ObjectID, operator int64, side TeamSide, from, to Position) Move {
	now := time.Now()
	return Move{
		MatchID:    matchID,
		RoomID:     roomID,
		Type:       MoveTypeRob,
		TeamSide:   &side,
		OperatorID: operator,
		From:       &from,
		To:         &to,
		CreatedAt:  now,
	}
}
