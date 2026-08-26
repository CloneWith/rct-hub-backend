package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// MatchStatus represents the lifecycle of a match.
type MatchStatus string

const (
	MatchStatusPending  MatchStatus = "pending"
	MatchStatusActive   MatchStatus = "active"
	MatchStatusFinished MatchStatus = "finished"
	MatchStatusCanceled MatchStatus = "canceled"
)

// Match represents a single RCT game session.
type Match struct {
	ID     bson.ObjectID `json:"id" bson:"_id,omitempty"`
	RoomID bson.ObjectID `json:"room_id" bson:"room_id"`
	Code   string        `json:"code" bson:"code"` // human-readable match code
	Name   string        `json:"name" bson:"name"`

	RoomType RoomType `json:"room_type" bson:"room_type"`

	TeamRed  TeamSnapshot `json:"team_red" bson:"team_red"`
	TeamBlue TeamSnapshot `json:"team_blue" bson:"team_blue"`

	Mappool Pool  `json:"mappool" bson:"mappool"`
	Board   Board `json:"board" bson:"board"`

	BPOrder   BPOrder    `json:"bp_order" bson:"bp_order"`
	TurnState TurnState  `json:"turn_state" bson:"turn_state"`
	Timer     TimerState `json:"timer" bson:"timer"`

	Status     MatchStatus `json:"status" bson:"status"`
	StartedAt  *time.Time  `json:"started_at,omitempty" bson:"started_at,omitempty"`
	FinishedAt *time.Time  `json:"finished_at,omitempty" bson:"finished_at,omitempty"`
	CreatedAt  time.Time   `json:"created_at" bson:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at" bson:"updated_at"`
}

// TeamSnapshot is the immutable per-side roster snapshot embedded in a match.
// It is built from the Team entity when a match starts.
type TeamSnapshot struct {
	ID           bson.ObjectID `json:"id" bson:"_id,omitempty"`
	Side         TeamSide      `json:"side" bson:"side"`
	Name         string        `json:"name" bson:"name"`
	Description  string        `json:"description" bson:"description"`
	Seed         string        `json:"seed" bson:"seed"`
	Color        string        `json:"color" bson:"color"`
	LeaderID     int64         `json:"leader_id" bson:"leader_id"`
	StrategistID int64         `json:"strategist_id" bson:"strategist_id"`
	Players      []int64       `json:"players" bson:"players"` // osu uids
}

// NewMatch creates a new match from room settings.
func NewMatch(room Room, redTeam, blueTeam TeamSnapshot) Match {
	now := time.Now()
	return Match{
		RoomID:    room.ID,
		RoomType:  room.Type,
		Name:      room.Name,
		TeamRed:   redTeam,
		TeamBlue:  blueTeam,
		Mappool:   room.Settings.Mappool,
		Board:     NewBoard(),
		BPOrder:   BPOrder{},
		TurnState: NewTurnState(),
		Timer:     NewTimerState(0, 0),
		Status:    MatchStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// IsStarted reports whether the match has left the pending/setup state.
func (m *Match) IsStarted() bool {
	return m.Status == MatchStatusActive || m.Status == MatchStatusFinished
}

// IsFinished reports whether the match has ended.
func (m *Match) IsFinished() bool {
	return m.Status == MatchStatusFinished || m.Status == MatchStatusCanceled
}

// TeamBySide returns the team for the given side.
func (m *Match) TeamBySide(side TeamSide) *TeamSnapshot {
	if side == TeamSideRed {
		return &m.TeamRed
	}
	return &m.TeamBlue
}

// ActiveStrategistID returns the osu uid of the strategist whose turn it is.
func (m *Match) ActiveStrategistID() *int64 {
	if m.TurnState.ActiveTeam == nil {
		return nil
	}
	team := m.TeamBySide(*m.TurnState.ActiveTeam)
	if team == nil {
		return nil
	}
	return &team.StrategistID
}

// WinningTeamID returns the side that has four in a row, if any.
func (m *Match) WinningTeamID() *TeamSide {
	if m.Board.HasFourInARow(string(TeamSideRed)) {
		red := TeamSideRed
		return &red
	}
	if m.Board.HasFourInARow(string(TeamSideBlue)) {
		blue := TeamSideBlue
		return &blue
	}
	return nil
}

// CanBan reports whether the given role can ban in the current turn.
func (m *Match) CanBan(member RoomMember) bool {
	if member.Role == RoomRoleAdmin {
		return true
	}
	return member.Role == RoomRoleStrategist &&
		m.TurnState.IsBanPhase() &&
		m.TurnState.IsTeamTurn(*member.TeamSide)
}

// CanPick reports whether the given role can pick/place in the current turn.
func (m *Match) CanPick(member RoomMember) bool {
	if member.Role == RoomRoleAdmin {
		return true
	}
	return member.Role == RoomRoleStrategist &&
		m.TurnState.IsPickPhase() &&
		m.TurnState.IsTeamTurn(*member.TeamSide)
}

// CanRob reports whether the given role can rob a piece in the current turn.
func (m *Match) CanRob(member RoomMember) bool {
	// Rob is allowed during the strategist's own pick turn.
	return m.CanPick(member)
}

// CanWin reports whether the given role can mark a piece as won.
// Admin can always win; strategists can win only when the admin has enabled
// win permission for their team (tracked externally or via a flag).
func (m *Match) CanWin(member RoomMember, winEnabledForTeam map[TeamSide]bool) bool {
	if member.Role == RoomRoleAdmin {
		return true
	}
	if member.Role != RoomRoleStrategist || member.TeamSide == nil {
		return false
	}
	return winEnabledForTeam[*member.TeamSide]
}

// CountWonPieces returns the number of won pieces per team on the board.
func (m *Match) CountWonPieces() map[TeamSide]int {
	counts := map[TeamSide]int{TeamSideRed: 0, TeamSideBlue: 0}
	for y := 0; y < m.Board.Rows; y++ {
		for x := 0; x < m.Board.Cols; x++ {
			cell := m.Board.Cells[y][x]
			if cell.State == CellStateOccupied && cell.TeamID != nil {
				side := TeamSide(*cell.TeamID)
				if _, ok := counts[side]; ok {
					counts[side]++
				}
			}
		}
	}
	return counts
}
