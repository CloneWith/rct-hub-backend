package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Beatmap holds metadata for an osu! beatmap used in the tournament.
type Beatmap struct {
	ID           bson.ObjectID `json:"_id" bson:"_id,omitempty"`
	OnlineID     int64         `json:"id" bson:"id"`
	BeatmapsetID int64         `json:"beatmapset_id" bson:"beatmapset_id"`

	// Metadata

	Title          string `json:"title" bson:"title"`
	Artist         string `json:"artist" bson:"artist"`
	DifficultyName string `json:"version" bson:"version"`
	AuthorID       int64  `json:"user_id" bson:"user_id"`
	RulesetID      int    `json:"mode_int" bson:"mode_int"`
	Status         string `json:"status" bson:"status"`

	StarRating  float64 `json:"difficulty_rating" bson:"difficulty_rating"`
	BPM         float64 `json:"bpm" bson:"bpm"`
	TotalLength int     `json:"total_length" bson:"total_length"`

	// Difficulty attributes

	DrainRate         float64 `json:"drain" bson:"drain"`
	CircleSize        float64 `json:"cs" bson:"cs"`
	ApproachRate      float64 `json:"ar" bson:"ar"`
	OverallDifficulty float64 `json:"accuracy" bson:"accuracy"`

	CoverURL string `json:"cover_url" bson:"cover_url"`

	CreatedAt time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
}
