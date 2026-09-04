package main

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
)

// SeedData bundles the entities seeded by `initdb`. `seedData` inserts them
// as-is; the seed-consistency test consumes them so the seed cannot silently
// drift from the service-layer start requirements again.
type SeedData struct {
	Admin        domain.User
	Beatmap      domain.Beatmap
	RedTeam      domain.Team
	BlueTeam     domain.Team
	Mappool      domain.Mappool
	Room         domain.Room
	Match        domain.Match
	Announcement domain.Announcement
}

// BuildSeedData constructs the full seed set deterministically from a single
// timestamp. Identity links (room → teams/mappool, match → teams) share the
// generated ObjectIDs so the seed is internally consistent.
func BuildSeedData(now time.Time) SeedData {
	seedRedTeamID := bson.NewObjectID()
	seedBlueTeamID := bson.NewObjectID()
	seedMappoolID := bson.NewObjectID()
	seedRoomID := bson.NewObjectID()
	seedBeatmapID := int64(1000000)
	seedReferee := int64(1)

	admin := domain.User{
		ID:           bson.NewObjectID(),
		OnlineID:     1,
		Username:     "admin_seed",
		AvatarURL:    "https://a.ppy.sh/1",
		CountryCode:  "__",
		Roles:        []domain.UserRole{domain.RoleAdmin},
		VerifyStatus: domain.Verified,
		IsBanned:     false,
		GlobalRank:   1024,
		PP:           114.51,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	beatmap := domain.Beatmap{
		ID:                bson.NewObjectID(),
		OnlineID:          1000000,
		BeatmapsetID:      500000,
		Title:             "Seed Beatmap",
		Artist:            "Seed Artist",
		DifficultyName:    "Normal",
		AuthorID:          1000,
		RulesetID:         0,
		Status:            "ranked",
		StarRating:        4.5,
		BPM:               180,
		TotalLength:       120,
		DrainRate:         5,
		CircleSize:        4,
		ApproachRate:      9,
		OverallDifficulty: 8,
		CoverURL:          "https://assets.ppy.sh/beatmaps/500000/covers/cover.jpg",
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	redTeam := domain.Team{
		ID:           seedRedTeamID,
		Name:         "Seed Red",
		Description:  new(string("Seeded red team")),
		Seed:         new(string("1")),
		LeaderID:     new(int64(1)),
		StrategistID: new(int64(1)),
		Players:      []int64{1, 2},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	blueTeam := domain.Team{
		ID:           seedBlueTeamID,
		Name:         "Seed Blue",
		Description:  new(string("Seeded blue team")),
		Seed:         new(string("2")),
		LeaderID:     new(int64(3)),
		StrategistID: new(int64(2)),
		Players:      []int64{3, 4},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	mappool := domain.Mappool{
		ID:          seedMappoolID,
		Name:        "Seed Mappool",
		Description: new(string("Seeded demo mappool")),
		Entries: []domain.MappoolEntry{
			{Mod: domain.PieceModNM, Index: 1, BeatmapID: &seedBeatmapID, SelectorID: new(int64(1))},
			{Mod: domain.PieceModHD, Index: 1, BeatmapID: &seedBeatmapID, SelectorID: new(int64(1))},
			{Mod: domain.PieceModTB, Index: 1, BeatmapID: &seedBeatmapID, SelectorID: new(int64(1))},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	room := domain.Room{
		ID:            seedRoomID,
		Code:          "SEED-ROOM",
		Name:          "Seed Room",
		Type:          domain.RoomTypeCasual,
		OwnerID:       1,
		RefereeUserID: &seedReferee,
		ScheduledAt:   &now,
		Settings: domain.RoomSettings{
			RedTeamID:  &seedRedTeamID,
			BlueTeamID: &seedBlueTeamID,
			MappoolID:  &seedMappoolID,
			FirstPick:  new(domain.TeamSideRed),
			FirstBan:   new(domain.TeamSideBlue),
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	match := domain.Match{
		ID:       bson.NewObjectID(),
		RoomID:   seedRoomID,
		Code:     "SEED-001",
		Name:     "Seed Match",
		RoomType: domain.RoomTypeCasual,
		TeamRed: domain.TeamSnapshot{
			ID:           seedRedTeamID,
			Side:         domain.TeamSideRed,
			Name:         "Seed Red",
			Description:  "Seeded red team",
			Seed:         "1",
			Color:        "#ef4444",
			LeaderID:     1,
			StrategistID: 1,
			Players:      []int64{1, 2},
		},
		TeamBlue: domain.TeamSnapshot{
			ID:           seedBlueTeamID,
			Side:         domain.TeamSideBlue,
			Name:         "Seed Blue",
			Description:  "Seeded blue team",
			Seed:         "2",
			Color:        "#3b82f6",
			LeaderID:     3,
			StrategistID: 2,
			Players:      []int64{3, 4},
		},
		Mappool:   domain.NewPool(),
		Board:     domain.NewBoard(),
		BPOrder:   domain.BPOrder{FirstPick: domain.TeamSideRed, FirstBan: domain.TeamSideBlue},
		TurnState: domain.NewTurnState(),
		Timer:     domain.NewTimerState(0, 0),
		Status:    domain.MatchStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}

	announcement := domain.Announcement{
		ID:          bson.NewObjectID(),
		Title:       "Welcome to RCT Hub",
		Content:     "This is a sample announcement seeded during database initialization.",
		AuthorID:    1,
		Pinned:      true,
		Visible:     true,
		PublishedAt: &now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	return SeedData{
		Admin:        admin,
		Beatmap:      beatmap,
		RedTeam:      redTeam,
		BlueTeam:     blueTeam,
		Mappool:      mappool,
		Room:         room,
		Match:        match,
		Announcement: announcement,
	}
}
