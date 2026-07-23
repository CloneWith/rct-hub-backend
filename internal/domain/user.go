package domain

import (
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// UserRole represents the RBAC role of a user.
type UserRole string

const (
	RolePlayer     UserRole = "player"
	RoleStrategist UserRole = "strategist"
	RoleReferee    UserRole = "referee"
	RoleStreamer   UserRole = "streamer"
	RoleAdmin      UserRole = "admin"
)

// User is the local representation of an osu! player or staff member.
type User struct {
	ID bson.ObjectID `json:"_id" bson:"_id,omitempty"`

	IsVerified bool `json:"is_verified" bson:"is_verified"`
	IsBanned   bool `json:"is_banned" bson:"is_banned"`

	OnlineID    int64      `json:"id" bson:"id"`
	Username    string     `json:"username" bson:"username"`
	AvatarURL   string     `json:"avatar_url" bson:"avatar_url"`
	CountryCode string     `json:"country_code" bson:"country_code"`
	Roles       []UserRole `json:"roles" bson:"roles"`

	CreatedAt time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
}

func (u *User) HasRole(role UserRole) bool {
	return slices.Contains(u.Roles, role)
}

func (u *User) HasAnyRole(roles ...UserRole) bool {
	return slices.ContainsFunc(roles, u.HasRole)
}
