package service

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/pkg/paginate"
)

type snapshotReaderStub struct {
	states        map[bson.ObjectID]matchengine.State
	loadManyCalls int
	loadedIDs     []bson.ObjectID
}

func (s *snapshotReaderStub) Load(_ context.Context, id bson.ObjectID) (matchengine.State, error) {
	return s.states[id], nil
}

func (s *snapshotReaderStub) LoadMany(_ context.Context, ids []bson.ObjectID) (map[bson.ObjectID]matchengine.State, error) {
	s.loadManyCalls++
	s.loadedIDs = append([]bson.ObjectID(nil), ids...)
	return s.states, nil
}

func TestFormalMatchReadUsesSnapshotAsStateSource(t *testing.T) {
	repo := newFakeMatchRepo()
	formalID, casualID := bson.NewObjectID(), bson.NewObjectID()
	beatmapID := int64(4_294_967_296)
	repo.matches[formalID] = &domain.Match{ID: formalID, Code: "FORMAL", RoomType: domain.RoomTypeMatch, Status: domain.MatchStatusFinished, Mappool: domain.Mappool{Slots: map[domain.PieceMod][]domain.Piece{domain.PieceModNM: {{BeatmapID: &beatmapID}}}}}
	repo.matches[casualID] = &domain.Match{ID: casualID, Code: "CASUAL", RoomType: domain.RoomTypeCasual}
	snapshots := &snapshotReaderStub{states: map[bson.ObjectID]matchengine.State{formalID: {Version: 42, Lifecycle: matchengine.LifecycleRunning, PoolSlots: map[string]matchengine.PoolSlot{"NM-1": {ID: "NM-1", Mod: matchengine.ModNM}}}}}
	reader := NewFormalMatchReadService(repo, snapshots)

	match, err := reader.ByID(context.Background(), formalID)
	if err != nil || match.State.Version != 42 || match.State.Lifecycle != matchengine.LifecycleRunning {
		t.Fatalf("formal match = %+v, %v", match, err)
	}
	if match.Pool["NM-1"] == nil || *match.Pool["NM-1"] != beatmapID {
		t.Fatalf("pool metadata = %+v", match.Pool)
	}
	page, err := reader.List(context.Background(), paginate.Params{Page: 1, PerPage: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != formalID || snapshots.loadManyCalls != 1 || len(snapshots.loadedIDs) != 1 {
		t.Fatalf("formal page = %+v, batch calls=%d ids=%v", page, snapshots.loadManyCalls, snapshots.loadedIDs)
	}
}
