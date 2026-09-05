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

// RoomSettings contains editable configuration for a room. Team rosters and
// the pool are referenced as Team / Mappool entities instead of being stored
// inline: redTeamID / blueTeamID / mappoolID point at the managed entities.
type RoomSettings struct {
	StreamerUserID *int64 `json:"streamer_user_id,omitempty" bson:"streamer_user_id,omitempty"`

	RedTeamID  *bson.ObjectID `json:"red_team_id,omitempty" bson:"red_team_id,omitempty"`
	BlueTeamID *bson.ObjectID `json:"blue_team_id,omitempty" bson:"blue_team_id,omitempty"`
	MappoolID  *bson.ObjectID `json:"mappool_id,omitempty" bson:"mappool_id,omitempty"`

	FirstPick *TeamSide `json:"first_pick,omitempty" bson:"first_pick,omitempty"`
	FirstBan  *TeamSide `json:"first_ban,omitempty" bson:"first_ban,omitempty"`

	MPLink     *string `json:"mp_link,omitempty" bson:"mp_link,omitempty"`
	StreamLink *string `json:"stream_link,omitempty" bson:"stream_link,omitempty"`
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
	RefereeUserID *int64 `json:"referee_user_id,omitempty" bson:"referee_user_id,omitempty"`
	// Round identifies the season stage this room belongs to. The empty
	// Round is permitted on non-match rooms (private / casual do not run
	// inside a season). For match rooms Round is required by the start
	// requirements validator; see internal/service/start_requirements.go.
	//
	// The BSON wire name stays "round" so legacy documents continue to
	// load without a data migration; legacy string values such as
	// "quarterfinal" are normalized to the closest current Round via
	// domain.Round.UnmarshalBSONValue.
	Round       Round          `json:"round" bson:"round"`
	ScheduledAt *time.Time     `json:"scheduled_at,omitempty" bson:"scheduled_at,omitempty"`
	Settings    RoomSettings   `json:"settings" bson:"settings"`
	MatchID     *bson.ObjectID `json:"match_id,omitempty" bson:"match_id,omitempty"`

	// SeasonID optionally ties the room to a domain.Season document so that
	// standings queries can scope by season. Match rooms SHOULD set this;
	// the start requirements validator enforces it for type=match.
	SeasonID *bson.ObjectID `json:"season_id,omitempty" bson:"season_id,omitempty"`

	// FixtureID optionally binds the room to a domain.Fixture document
	// recording the week's slot (e.g. "WM1") and the two competing team
	// IDs. The fixture binding is advisory for now (admin may create a
	// fixture and then forget to bind it) but it powers the standings
	// page's "who played who this week" query.
	FixtureID *bson.ObjectID `json:"fixture_id,omitempty" bson:"fixture_id,omitempty"`

	// WeekIndex is the 1-based week number within the season
	// (1=Warmup, 2=Assault, 3=Protracted, 4=Finals, 0=Qualifiers).
	// Zero is the default for rooms outside a season (private / casual)
	// and the canonical value for RoundQualifiers.
	WeekIndex int `json:"week_index,omitempty" bson:"week_index,omitempty"`

	CreatedAt time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
}

// Normalize applies domain invariants that need to hold after the Room is
// loaded from MongoDB (i.e. after Round.UnmarshalBSONValue has already
// rewritten legacy strings). Currently a no-op; kept as an extension point
// for future cross-field invariants such as "FixtureID requires Round".
func (r *Room) Normalize() {
	// intentionally empty; placeholder for invariants that span multiple
	// fields after deserialization (see comment above).
}
