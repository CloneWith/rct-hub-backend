package domain

import (
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Fixture is an admin-recorded pairing of two teams for a given (season,
// week, slot). It is advisory for now: rooms are not required to bind a
// Fixture, and the standings page uses Fixture purely to render "who
// played who this week". Future iterations may tighten this into a
// permission: "only the admin who created the Fixture may open the
// matching Room".
//
// The collection name is "fixtures"; the recommended unique index is
// (season, week_index, slot) so admin cannot accidentally create two
// "WM1" rows for the same season.
type Fixture struct {
	ID     bson.ObjectID `json:"id" bson:"_id,omitempty"`
	Season bson.ObjectID `json:"season" bson:"season"`

	// WeekIndex is the 1-based week number within the season
	// (1=Warmup, 2=Assault, 3=Protracted, 4=Finals, 0=Qualifiers).
	WeekIndex int `json:"week_index" bson:"week_index"`

	// Slot is the admin-supplied label for this pairing within the week
	// (e.g. "WM1", "AM2", "PM3", "FM1"). Free-form text today; a
	// future iteration could constrain it to a per-week enum so the
	// standings table always lists the same slots in the same order.
	Slot string `json:"slot" bson:"slot"`

	// TeamRedID / TeamBlueID are the two teams competing. Both must be
	// non-zero; cross-team ID equality is rejected by Validate.
	TeamRedID  bson.ObjectID `json:"team_red" bson:"team_red"`
	TeamBlueID bson.ObjectID `json:"team_blue" bson:"team_blue"`

	// RoomID is the Room eventually opened for this pairing. Nil until
	// admin creates + binds the room (via bindFixtureToRoom mutation).
	RoomID *bson.ObjectID `json:"room,omitempty" bson:"room,omitempty"`

	CreatedAt time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
}

// NewFixture creates an unbound Fixture for the given (season, week, slot,
// red, blue) tuple. Call BindRoom later to attach a Room.
func NewFixture(season bson.ObjectID, weekIndex int, slot string, red, blue bson.ObjectID) *Fixture {
	now := time.Now().UTC()
	return &Fixture{
		ID:         bson.NewObjectID(),
		Season:     season,
		WeekIndex:  weekIndex,
		Slot:       slot,
		TeamRedID:  red,
		TeamBlueID: blue,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

// BindRoom attaches a Room to this Fixture. Used by the
// bindFixtureToRoom mutation once admin has created the Room and wants
// standings queries to surface it. Passing nil clears the binding.
func (f *Fixture) BindRoom(roomID *bson.ObjectID) {
	f.RoomID = roomID
	f.UpdatedAt = time.Now().UTC()
}

// Validate runs structural validation. The fixture service calls this
// before upsert.
func (f *Fixture) Validate() error {
	if f == nil {
		return errors.New("domain.Fixture: nil")
	}
	if f.Season.IsZero() {
		return errors.New("domain.Fixture: Season is required")
	}
	if f.WeekIndex < 0 || f.WeekIndex > 4 {
		return errors.New("domain.Fixture: WeekIndex must be in [0,4]")
	}
	if f.Slot == "" {
		return errors.New("domain.Fixture: Slot is required")
	}
	if f.TeamRedID.IsZero() {
		return errors.New("domain.Fixture: TeamRedID is required")
	}
	if f.TeamBlueID.IsZero() {
		return errors.New("domain.Fixture: TeamBlueID is required")
	}
	if f.TeamRedID == f.TeamBlueID {
		return errors.New("domain.Fixture: TeamRedID and TeamBlueID must differ")
	}
	return nil
}
