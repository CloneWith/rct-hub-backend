package service

import (
	"errors"
	"reflect"
	"slices"
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
	room, redTeam, blueTeam, mappool := formalSeedFixture()
	seed, err := BuildFormalMatchSeed(room, &redTeam, &blueTeam, &mappool, now)
	if err != nil {
		t.Fatalf("BuildFormalMatchSeed: %v", err)
	}
	if seed.LegacyMatch.ID == bson.NilObjectID || seed.LegacyMatch.RoomID != room.ID {
		t.Fatalf("legacy match identity = id %s room %s", seed.LegacyMatch.ID, seed.LegacyMatch.RoomID)
	}
	if seed.LegacyMatch.Status != domain.MatchStatusPending || seed.LegacyMatch.StartedAt != nil {
		t.Fatalf("legacy shell must remain pending until formal StartMatch command: %+v", seed.LegacyMatch)
	}
	if seed.LegacyMatch.TeamRed.ID != redTeam.ID || seed.LegacyMatch.TeamBlue.ID != blueTeam.ID {
		t.Fatalf("legacy snapshots must carry the linked team identities")
	}
	if !reflect.DeepEqual(seed.LegacyMatch.TeamRed.Players, redTeam.Players) {
		t.Fatalf("red snapshot roster changed during mapping")
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
	if _, ok := seed.State.PoolSlots["SHIRO"]; !ok {
		t.Fatalf("Shiro slot missing from mapped pool: %+v", seed.State.PoolSlots)
	}
	if _, ok := seed.State.PoolSlots["TB-1"]; !ok {
		t.Fatalf("TB slot missing from mapped pool: %+v", seed.State.PoolSlots)
	}
	if !reflect.DeepEqual(seed.State.Rosters[matchengine.TeamRed].PlayerIDs, redTeam.Players) {
		t.Fatalf("red roster changed during mapping")
	}
}

func TestBuildFormalMatchSeedRejectsAmbiguousOrInvalidConfiguration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		mutate    func(room *domain.Room, red, blue *domain.Team, pool *domain.Mappool)
		wantError bool
	}{
		{name: "three players", mutate: func(room *domain.Room, red, _ *domain.Team, _ *domain.Mappool) {
			red.Players = red.Players[:3]
		}, wantError: false},
		{name: "leader not rostered", mutate: func(room *domain.Room, red, _ *domain.Team, _ *domain.Mappool) {
			red.Players = nil
		}, wantError: true},
		{name: "unready red team", mutate: func(room *domain.Room, red, _ *domain.Team, _ *domain.Mappool) {
			red.LeaderID = nil
		}, wantError: true},
		// Shiro is materialized by the factory from a fixed slot id
		// regardless of whether the mappool happens to declare a Shiro
		// entry, so deleting the entry no longer breaks the seed.
		{name: "missing Shiro entry in pool", mutate: func(room *domain.Room, _, _ *domain.Team, pool *domain.Mappool) {
			pool.Entries = slices.DeleteFunc(pool.Entries, func(e domain.MappoolEntry) bool {
				return e.Mod == domain.PieceModShiro
			})
		}, wantError: false},
		{name: "duplicate player", mutate: func(room *domain.Room, red, blue *domain.Team, _ *domain.Mappool) {
			blue.Players[7] = red.Players[0]
		}, wantError: true},
		{name: "casual room type", mutate: func(room *domain.Room, _, _ *domain.Team, _ *domain.Mappool) {
			room.Type = domain.RoomTypeCasual
		}, wantError: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			room, redTeam, blueTeam, mappool := formalSeedFixture()
			tt.mutate(&room, &redTeam, &blueTeam, &mappool)
			_, err := BuildFormalMatchSeed(room, &redTeam, &blueTeam, &mappool, now)
			if tt.wantError {
				if !errors.Is(err, errs.ErrInvalidInput) {
					t.Fatalf("error = %v, want invalid input", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
}

func TestBuildFormalMatchSeedReportsMissingFields(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(room *domain.Room, red, blue *domain.Team, pool *domain.Mappool)
		fields []string
	}{
		{
			name:   "missing first pick",
			mutate: func(room *domain.Room, _, _ *domain.Team, _ *domain.Mappool) { room.Settings.FirstPick = nil },
			fields: []string{"settings.first_pick"},
		},
		{
			name:   "missing mp link",
			mutate: func(room *domain.Room, _, _ *domain.Team, _ *domain.Mappool) { room.Settings.MPLink = nil },
			fields: []string{"settings.mp_link"},
		},
		{
			name: "missing multiple",
			mutate: func(room *domain.Room, red, _ *domain.Team, _ *domain.Mappool) {
				room.Settings.FirstPick = nil
				red.LeaderID = nil
			},
			fields: []string{"settings.red_team_id", "settings.first_pick"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			room, redTeam, blueTeam, mappool := formalSeedFixture()
			tt.mutate(&room, &redTeam, &blueTeam, &mappool)
			_, err := BuildFormalMatchSeed(room, &redTeam, &blueTeam, &mappool, now)
			if err == nil {
				t.Fatal("BuildFormalMatchSeed succeeded with missing settings")
			}
			valErr, ok := errs.AsValidationError(err)
			if !ok {
				t.Fatalf("error = %v, want *errs.ValidationError", err)
			}
			if len(valErr.Fields) != len(tt.fields) {
				t.Fatalf("fields = %+v, want %v", valErr.Fields, tt.fields)
			}
			for i, f := range valErr.Fields {
				if f.Field != tt.fields[i] || f.Rule != "required" {
					t.Fatalf("field %d = %+v, want field %q rule %q", i, f, tt.fields[i], "required")
				}
			}
		})
	}
}

// formalSeedFixture returns a fully configured tournament room plus the
// linked team and mappool entities it references.
func formalSeedFixture() (domain.Room, domain.Team, domain.Team, domain.Mappool) {
	redLeader, blueLeader := int64(1), int64(11)
	redStrategist, blueStrategist := int64(101), int64(201)
	firstPick, firstBan := domain.TeamSideRed, domain.TeamSideBlue
	mpLink := "https://osu.ppy.sh/community/matches/1"

	redTeam := domain.Team{
		ID:           bson.NewObjectID(),
		Name:         "Fixture Red",
		LeaderID:     &redLeader,
		StrategistID: &redStrategist,
		Players:      []int64{1, 2, 3, 4, 5, 6, 7, 8},
	}
	blueTeam := domain.Team{
		ID:           bson.NewObjectID(),
		Name:         "Fixture Blue",
		LeaderID:     &blueLeader,
		StrategistID: &blueStrategist,
		Players:      []int64{11, 12, 13, 14, 15, 16, 17, 18},
	}

	beatmap := int64(1000000)
	mappool := domain.Mappool{
		ID:   bson.NewObjectID(),
		Name: "Fixture Pool",
		Entries: []domain.MappoolEntry{
			{Mod: domain.PieceModNM, Index: 1, BeatmapID: &beatmap},
			{Mod: domain.PieceModNM, Index: 2, BeatmapID: &beatmap},
			{Mod: domain.PieceModHD, Index: 1, BeatmapID: &beatmap},
			{Mod: domain.PieceModHR, Index: 1, BeatmapID: &beatmap},
			{Mod: domain.PieceModDT, Index: 1, BeatmapID: &beatmap},
			{Mod: domain.PieceModFM, Index: 1, BeatmapID: &beatmap},
			{Mod: domain.PieceModShiro, Index: 1},
			{Mod: domain.PieceModTB, Index: 1, BeatmapID: &beatmap},
		},
	}

	room := domain.Room{
		ID:            bson.NewObjectID(),
		Code:          "FORMAL",
		Name:          "Formal Match",
		Type:          domain.RoomTypeMatch,
		OwnerID:       999,
		RefereeUserID: func() *int64 { v := int64(999); return &v }(),
		ScheduledAt:   func() *time.Time { v := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC); return &v }(),
		Settings: domain.RoomSettings{
			RedTeamID:  &redTeam.ID,
			BlueTeamID: &blueTeam.ID,
			MappoolID:  &mappool.ID,
			FirstPick:  &firstPick,
			FirstBan:   &firstBan,
			MPLink:     &mpLink,
		},
	}
	return room, redTeam, blueTeam, mappool
}
