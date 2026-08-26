package domain

import (
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// RoomType categorizes a match room.
type RoomType string

const (
	RoomTypePrivate RoomType = "private" // one per verified user, full owner control
	RoomTypeCasual  RoomType = "casual"  // user-created friendly room
	RoomTypeMatch   RoomType = "match"   // referee-created tournament room
)

// RoomRole is the permission group inside a room.
type RoomRole string

const (
	RoomRoleAdmin      RoomRole = "admin"      // room owner / creator / referee
	RoomRoleStrategist RoomRole = "strategist" // assigned strategist for a team
	RoomRoleStreamer   RoomRole = "streamer"   // match streamer (match rooms only)
	RoomRoleSpectator  RoomRole = "spectator"  // free viewers
)

// TeamSide identifies the red or blue side.
type TeamSide string

const (
	TeamSideRed  TeamSide = "red"
	TeamSideBlue TeamSide = "blue"
)

// Opponent returns the other team side.
func (s TeamSide) Opponent() TeamSide {
	if s == TeamSideRed {
		return TeamSideBlue
	}
	return TeamSideRed
}

// RoomMember links a user to a room with a role and optional team assignment.
type RoomMember struct {
	UserID   int64         `json:"user_id" bson:"user_id"`
	RoomID   bson.ObjectID `json:"room_id" bson:"room_id"`
	Role     RoomRole      `json:"role" bson:"role"`
	TeamSide *TeamSide     `json:"team_side,omitempty" bson:"team_side,omitempty"`
	JoinedAt time.Time     `json:"joined_at" bson:"joined_at"`
}

// HasRole reports whether the member has the given room role.
func (m RoomMember) HasRole(role RoomRole) bool {
	return m.Role == role
}

// HasAnyRole reports whether the member has any of the given roles.
func (m RoomMember) HasAnyRole(roles ...RoomRole) bool {
	return slices.ContainsFunc(roles, m.HasRole)
}

// IsPrivileged reports whether the member can act as an admin/strategist.
func (m RoomMember) IsPrivileged() bool {
	return m.Role == RoomRoleAdmin || m.Role == RoomRoleStrategist
}

// RoomSettings contains editable configuration for a room.
type RoomSettings struct {
	RedStrategistUserID  *int64 `json:"red_strategist_user_id,omitempty" bson:"red_strategist_user_id,omitempty"`
	BlueStrategistUserID *int64 `json:"blue_strategist_user_id,omitempty" bson:"blue_strategist_user_id,omitempty"`
	StreamerUserID       *int64 `json:"streamer_user_id,omitempty" bson:"streamer_user_id,omitempty"`

	Mappool   Pool      `json:"mappool" bson:"mappool"`
	FirstPick *TeamSide `json:"first_pick,omitempty" bson:"first_pick,omitempty"`
	FirstBan  *TeamSide `json:"first_ban,omitempty" bson:"first_ban,omitempty"`

	RedPlayers  []int64 `json:"red_players" bson:"red_players"`
	BluePlayers []int64 `json:"blue_players" bson:"blue_players"`
	RedLeader   *int64  `json:"red_leader,omitempty" bson:"red_leader,omitempty"`
	BlueLeader  *int64  `json:"blue_leader,omitempty" bson:"blue_leader,omitempty"`

	MPLink     *string `json:"mp_link,omitempty" bson:"mp_link,omitempty"`
	StreamLink *string `json:"stream_link,omitempty" bson:"stream_link,omitempty"`
}

// CanStart reports whether the room has the minimum required settings to start a match.
// Requirements depend on room type:
//   - Casual/Match: strategists, BP order, players, and mplink must be set.
//   - Private: no strict requirements.
func (rs RoomSettings) CanStart(roomType RoomType) bool {
	return len(rs.MissingStartRequirements(roomType)) == 0
}

// MissingStartRequirements returns the wire-format paths of the settings that
// still block starting a match of the given type. An empty result means the
// room can start. Callers use this to tell the client exactly what is missing.
func (rs RoomSettings) MissingStartRequirements(roomType RoomType) []string {
	var missing []string
	require := func(field string, ok bool) {
		if !ok {
			missing = append(missing, field)
		}
	}
	switch roomType {
	case RoomTypeCasual, RoomTypeMatch:
		require("settings.red_strategist_user_id", rs.RedStrategistUserID != nil)
		require("settings.blue_strategist_user_id", rs.BlueStrategistUserID != nil)
		require("settings.first_pick", rs.FirstPick != nil)
		require("settings.first_ban", rs.FirstBan != nil)
		if roomType == RoomTypeMatch {
			require("settings.red_leader", rs.RedLeader != nil)
			require("settings.blue_leader", rs.BlueLeader != nil)
			require("settings.red_players", len(rs.RedPlayers) >= 4)
			require("settings.blue_players", len(rs.BluePlayers) >= 4)
			require("settings.mp_link", rs.MPLink != nil && *rs.MPLink != "")
		}
	case RoomTypePrivate:
		// No requirements.
	default:
		missing = append(missing, "type")
	}
	return missing
}

// Room represents a place where a match is configured and played.
type Room struct {
	ID      bson.ObjectID `json:"id" bson:"_id,omitempty"`
	Code    string        `json:"code" bson:"code"` // invite code
	Name    string        `json:"name" bson:"name"`
	Type    RoomType      `json:"type" bson:"type"`
	OwnerID int64         `json:"owner_id" bson:"owner_id"` // creator / referee / private room owner
	// RefereeUserID is the explicitly assigned referee for formal match rooms.
	// It is separate from OwnerID, which remains the room creator.
	RefereeUserID *int64         `json:"referee_user_id,omitempty" bson:"referee_user_id,omitempty"`
	Round         string         `json:"round,omitempty" bson:"round,omitempty"`
	ScheduledAt   *time.Time     `json:"scheduled_at,omitempty" bson:"scheduled_at,omitempty"`
	Settings      RoomSettings   `json:"settings" bson:"settings"`
	MatchID       *bson.ObjectID `json:"match_id,omitempty" bson:"match_id,omitempty"`
	CreatedAt     time.Time      `json:"created_at" bson:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at" bson:"updated_at"`
}
