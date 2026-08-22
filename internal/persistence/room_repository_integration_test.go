package persistence_test

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/persistence"
	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/paginate"
)

func TestMongoIntegrationRoomDirectoryFiltersBeforePagination(t *testing.T) {
	_, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	roomRepo := repository.NewRoomRepository(db)
	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	allFixtures := integrationLifecycleFixtures(t, now)
	fixtures := []lifecycleFixture{allFixtures[0], allFixtures[1], allFixtures[3], allFixtures[8]}
	rooms := make([]domain.Room, 0, len(fixtures))
	for index, fixture := range fixtures {
		matchID := bson.NewObjectID()
		room := domain.Room{
			ID: matchID, Code: "FILTER-" + string(rune('A'+index)), Name: "Needle " + fixture.name,
			Type: domain.RoomTypeMatch, OwnerID: int64(100 + index), RefereeUserID: ptrInt64(200 + index),
			Round: "quarterfinal", MatchID: &matchID,
			Settings: domain.RoomSettings{
				RedStrategistUserID: ptrInt64(301), BlueStrategistUserID: ptrInt64(302),
				StreamerUserID: ptrInt64(303), RedPlayers: []int64{401, 402}, BluePlayers: []int64{501, 502},
			},
			CreatedAt: now.Add(time.Duration(index) * time.Minute), UpdatedAt: now,
		}
		document, err := persistence.NewMatchSnapshotDocument(matchID, fixture.state, now)
		if err != nil {
			t.Fatalf("snapshot %s: %v", fixture.name, err)
		}
		if _, err := db.Collection(persistence.MatchSnapshotsCollection).InsertOne(ctx, document); err != nil {
			t.Fatalf("insert snapshot %s: %v", fixture.name, err)
		}
		if _, err := db.Collection("rooms").InsertOne(ctx, room); err != nil {
			t.Fatalf("insert room %s: %v", fixture.name, err)
		}
		rooms = append(rooms, room)
	}

	searchResult, err := roomRepo.List(ctx, paginate.Params{Page: 2, PerPage: 1}, repository.RoomListFilter{Search: "needle"})
	if err != nil {
		t.Fatalf("search rooms: %v", err)
	}
	if searchResult.Total != 4 || searchResult.TotalPages != 4 || len(searchResult.Data) != 1 {
		t.Fatalf("search pagination = page %d total %d pages %d items %d", searchResult.Page, searchResult.Total, searchResult.TotalPages, len(searchResult.Data))
	}

	for index := 2; index < 4; index++ {
		status := string(fixtures[index].state.Lifecycle)
		statusResult, err := roomRepo.List(ctx, paginate.Params{Page: 1, PerPage: 20}, repository.RoomListFilter{Lifecycle: status})
		if err != nil {
			t.Fatalf("status %s rooms: %v", status, err)
		}
		if statusResult.Total != 1 || len(statusResult.Data) != 1 || statusResult.Data[0].ID != rooms[index].ID {
			t.Fatalf("status %s = %+v, want room %s", status, statusResult, rooms[index].ID.Hex())
		}
	}

	for _, userID := range []int64{rooms[0].OwnerID, *rooms[0].RefereeUserID, 301, 303, 401, 501} {
		result, err := roomRepo.List(ctx, paginate.Params{Page: 1, PerPage: 20}, repository.RoomListFilter{RelatedUserID: &userID})
		if err != nil {
			t.Fatalf("related user %d: %v", userID, err)
		}
		if result.Total != 1 || len(result.Data) != 1 || result.Data[0].ID != rooms[0].ID {
			t.Fatalf("related user %d = %+v, want room %s", userID, result, rooms[0].ID.Hex())
		}
	}
}

func ptrInt64(value int) *int64 {
	converted := int64(value)
	return &converted
}
