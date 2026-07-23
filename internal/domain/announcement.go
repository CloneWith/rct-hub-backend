package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Announcement is a CMS-managed notice shown in the frontend.
type Announcement struct {
	ID       bson.ObjectID `json:"id" bson:"_id,omitempty"`
	Pinned   bool          `json:"pinned" bson:"pinned"`
	Visible  bool          `json:"visible" bson:"visible"`
	Title    string        `json:"title" bson:"title"`
	Content  string        `json:"content" bson:"content"`
	AuthorID int64         `json:"author_id" bson:"author_id"`

	PublishedAt *time.Time `json:"published_at,omitempty" bson:"published_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at" bson:"updated_at"`
}
