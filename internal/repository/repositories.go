package repository

import (
	"go.mongodb.org/mongo-driver/v2/mongo"

	"rctHubBackend/internal/persistence"
)

// Repositories wires together all storage repositories.
type Repositories struct {
	Users          UserRepository
	Beatmaps       BeatmapRepository
	Rooms          RoomRepository
	Matches        MatchRepository
	Moves          MoveRepository
	Results        ResultRepository
	Announcements  AnnouncementRepository
	MatchSnapshots *persistence.SnapshotStore
	FormalMatches  *persistence.FormalMatchBootstrapStore
}

// NewRepositories creates all repository implementations from a MongoDB database.
func NewRepositories(client *mongo.Client, db *mongo.Database) *Repositories {
	return &Repositories{
		Users:          NewUserRepository(db),
		Beatmaps:       NewBeatmapRepository(db),
		Rooms:          NewRoomRepository(db),
		Matches:        NewMatchRepository(db),
		Moves:          NewMoveRepository(db),
		Results:        NewResultRepository(db),
		Announcements:  NewAnnouncementRepository(db),
		MatchSnapshots: persistence.NewSnapshotStore(db),
		FormalMatches:  persistence.NewFormalMatchBootstrapStore(client, db),
	}
}
