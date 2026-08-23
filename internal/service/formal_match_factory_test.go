package service

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/pkg/errs"
)

func TestBuildFormalMatchSeedCreatesReadyAuthoritativeState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	room := formalRoomFixture()
	seed, err := BuildFormalMatchSeed(room, now)
	if err != nil {
		t.Fatalf("BuildFormalMatchSeed: %v", err)
	}
	if seed.LegacyMatch.ID == bson.NilObjectID || seed.LegacyMatch.RoomID != room.ID {
		t.Fatalf("legacy match identity = id %s room %s", seed.LegacyMatch.ID, seed.LegacyMatch.RoomID)
	}
	if seed.LegacyMatch.Status != domain.MatchStatusPending || seed.LegacyMatch.StartedAt != nil {
		t.Fatalf("legacy shell must remain pending until formal StartMatch command: %+v", seed.LegacyMatch)
	}
	if seed.State.Lifecycle != matchengine.LifecycleReady || seed.State.Version != 0 {
		t.Fatalf("authoritative state = lifecycle %q version %d", seed.State.Lifecycle, seed.State.Version)
	}
	if err := matchengine.ValidateState(seed.State); err != nil {
		t.Fatalf("seed state is invalid: %v", err)
	}
	if seed.State.FirstBan != matchengine.TeamBlue || seed.State.FirstPick != matchengine.TeamRed {
		t.Fatalf("BP mapping = first ban %q first pick %q", seed.State.FirstBan, seed.State.FirstPick)
	}
	if _, ok := seed.State.PoolSlots["Shiro-1"]; !ok {
		t.Fatalf("Shiro slot missing from mapped pool: %+v", seed.State.PoolSlots)
	}
	if _, ok := seed.State.PoolSlots["TB-1"]; !ok {
		t.Fatalf("TB slot missing from mapped pool: %+v", seed.State.PoolSlots)
	}
	if !reflect.DeepEqual(seed.State.Rosters[matchengine.TeamRed].PlayerIDs, room.Settings.RedPlayers) {
		t.Fatalf("red roster changed during mapping")
	}
}

func TestBuildFormalMatchSeedRejectsAmbiguousOrInvalidConfiguration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*domain.Room)
	}{
		{name: "casual room", mutate: func(room *domain.Room) { room.Type = domain.RoomTypeCasual }},
		{name: "seven players", mutate: func(room *domain.Room) { room.Settings.RedPlayers = room.Settings.RedPlayers[:7] }},
		{name: "missing Shiro", mutate: func(room *domain.Room) { delete(room.Settings.Mappool.Slots, domain.PieceModShiro) }},
		{name: "duplicate player", mutate: func(room *domain.Room) { room.Settings.BluePlayers[7] = room.Settings.RedPlayers[0] }},
		{name: "pre-mutated pool", mutate: func(room *domain.Room) {
			pieces := room.Settings.Mappool.Slots[domain.PieceModNM]
			pieces[0].State = domain.PieceStateBanned
			room.Settings.Mappool.Slots[domain.PieceModNM] = pieces
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			room := formalRoomFixture()
			tt.mutate(&room)
			if _, err := BuildFormalMatchSeed(room, now); !errors.Is(err, errs.ErrInvalidInput) {
				t.Fatalf("error = %v, want invalid input", err)
			}
		})
	}
}

func formalRoomFixture() domain.Room {
	redStrategist, blueStrategist := int64(101), int64(201)
	redLeader, blueLeader := int64(1), int64(11)
	firstPick, firstBan := domain.TeamSideRed, domain.TeamSideBlue
	mpLink := "https://osu.ppy.sh/community/matches/1"
	pool := domain.NewMappool()
	pool.Slots[domain.PieceModNM] = []domain.Piece{{}, {}}
	pool.Slots[domain.PieceModHD] = []domain.Piece{{}}
	pool.Slots[domain.PieceModHR] = []domain.Piece{{}}
	pool.Slots[domain.PieceModDT] = []domain.Piece{{}}
	pool.Slots[domain.PieceModFM] = []domain.Piece{{}}
	pool.Slots[domain.PieceModShiro] = []domain.Piece{{}}
	pool.Slots[domain.PieceModTB] = []domain.Piece{{}}
	return domain.Room{
		ID:            bson.NewObjectID(),
		Code:          "FORMAL",
		Name:          "Formal Match",
		Type:          domain.RoomTypeMatch,
		OwnerID:       999,
		RefereeUserID: func() *int64 { v := int64(999); return &v }(),
		Settings: domain.RoomSettings{
			RedStrategistUserID:  &redStrategist,
			BlueStrategistUserID: &blueStrategist,
			Mappool:              pool,
			FirstPick:            &firstPick,
			FirstBan:             &firstBan,
			RedPlayers:           []int64{1, 2, 3, 4, 5, 6, 7, 8},
			BluePlayers:          []int64{11, 12, 13, 14, 15, 16, 17, 18},
			RedLeader:            &redLeader,
			BlueLeader:           &blueLeader,
			MPLink:               &mpLink,
		},
	}
}
