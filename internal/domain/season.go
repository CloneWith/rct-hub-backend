package domain

import (
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// SeasonConfig holds tunable parameters for a single season. Today the only
// tunable is the per-week point schedule; future tuning (e.g. custom
// ExtraPointRule thresholds) will hang off the same struct so existing
// documents can be migrated by bumping SeasonConfigSchemaVersion.
type SeasonConfig struct {
	// PointScheduleKey is a lookup into a Go-side table (see
	// internal/config/point_based_schedule.go) selecting which point
	// schedule variant to apply. The default "RCT_S1_STANDARD" matches the
	// rulebook published 2026-09-05; future seasons can publish new
	// schedules without touching the domain layer.
	PointScheduleKey string `json:"point_schedule_key" bson:"point_schedule_key"`
}

// SeasonConfigSchemaVersion is the version of the SeasonConfig struct that
// the application understands. Increment whenever a non-additive change is
// made to SeasonConfig so initdb can refuse to load documents that depend
// on fields the current binary does not know how to read.
const SeasonConfigSchemaVersion = 1

// Season represents one championship run. Rooms opt into a season via
// Room.SeasonID; standings queries scope by Season.ID. A Season is created
// once by an admin and lives for the duration of the tournament.
type Season struct {
	ID            bson.ObjectID `json:"id" bson:"_id,omitempty"`
	Name          string        `json:"name" bson:"name"`
	SchemaVersion int           `json:"schema_version" bson:"schema_version"`
	Config        SeasonConfig  `json:"config" bson:"config"`
	CreatedAt     time.Time     `json:"created_at" bson:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at" bson:"updated_at"`
}

// NewSeason constructs a Season with sensible defaults. The caller is
// expected to validate Name separately (e.g. via a service-layer rule).
func NewSeason(name string) *Season {
	now := time.Now().UTC()
	return &Season{
		ID:            bson.NewObjectID(),
		Name:          name,
		SchemaVersion: SeasonConfigSchemaVersion,
		Config: SeasonConfig{
			PointScheduleKey: DefaultPointScheduleKey,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// DefaultPointScheduleKey is the schedule key used when NewSeason is
// called without an explicit override. Keep aligned with the table key in
// internal/config/point_based_schedule.go (table registration happens at
// init time so a typo here will be caught by the service layer).
const DefaultPointScheduleKey = "RCT_S1_STANDARD"

// Validate performs structural validation of the Season in-memory. It is
// used by service-layer guards (e.g. before writing to MongoDB) and does
// NOT replace the MongoDB schema validator; the two layers are
// deliberately redundant so misuse in dev still surfaces an error.
func (s *Season) Validate() error {
	if s == nil {
		return errors.New("domain.Season: nil")
	}
	if s.Name == "" {
		return errors.New("domain.Season: Name is required")
	}
	if s.SchemaVersion == 0 {
		return fmt.Errorf("domain.Season: SchemaVersion is required (want >= %d)", SeasonConfigSchemaVersion)
	}
	if s.Config.PointScheduleKey == "" {
		return fmt.Errorf("domain.Season: Config.PointScheduleKey is required (default: %q)", DefaultPointScheduleKey)
	}
	return nil
}

// Touch updates UpdatedAt to now. Call before every write so the timestamp
// reflects the most recent edit.
func (s *Season) Touch() {
	s.UpdatedAt = time.Now().UTC()
}
