package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.uber.org/zap"

	"rctHubBackend/internal/config"
	"rctHubBackend/internal/database"
	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/logger"
)

func main() {
	var (
		drop = flag.Bool("drop", false, "drop existing collections before recreating")
		seed = flag.Bool("seed", false, "insert sample seed data")
	)
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	log, err := logger.New(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to setup logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = log.Sync() }()

	db, err := database.New(cfg)
	if err != nil {
		log.Error("failed to connect to database", zap.Error(err))
		os.Exit(1)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = db.Close(ctx)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	collections := []string{
		"users",
		"beatmaps",
		"rooms",
		"matches",
		"moves",
		"results",
		"announcements",
	}

	if *drop {
		for _, name := range collections {
			if err := db.MongoDB.Collection(name).Drop(ctx); err != nil {
				log.Error("failed to drop collection", zap.String("collection", name), zap.Error(err))
				os.Exit(1)
			}
			log.Info("dropped collection", zap.String("collection", name))
		}
	}

	// Ensure collections exist by creating indexes.
	if err := db.EnsureIndexes(ctx); err != nil {
		log.Error("failed to ensure indexes", zap.Error(err))
		os.Exit(1)
	}
	log.Info("ensured indexes")

	// Additional schema-level validations can be added here via collMod.
	if err := ensureSchemaValidation(ctx, db.MongoDB); err != nil {
		log.Error("failed to ensure schema validation", zap.Error(err))
		os.Exit(1)
	}
	log.Info("ensured schema validation rules")

	if *seed {
		if err := seedData(ctx, db.MongoDB, log); err != nil {
			log.Error("failed to seed data", zap.Error(err))
			os.Exit(1)
		}
		log.Info("seeded sample data")
	}

	log.Info("database initialization complete", zap.String("database", cfg.MongoDB.Name))
}

func ensureSchemaValidation(ctx context.Context, db *mongo.Database) error {
	userValidator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"id", "username", "roles", "created_at", "updated_at"},
			"properties": bson.M{
				"id":       bson.M{"bsonType": "long"},
				"username": bson.M{"bsonType": "string"},
				"roles": bson.M{
					"bsonType": "array",
					"items":    bson.M{"enum": []string{"player", "strategist", "referee", "streamer", "admin"}},
				},
				"is_banned": bson.M{"bsonType": "bool"},
			},
		},
	}
	if err := db.RunCommand(ctx, bson.D{
		{Key: "collMod", Value: "users"},
		{Key: "validator", Value: userValidator},
		{Key: "validationLevel", Value: "moderate"},
	}).Err(); err != nil {
		return fmt.Errorf("users validation: %w", err)
	}

	roomValidator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"code", "name", "type", "owner_id", "settings", "created_at", "updated_at"},
			"properties": bson.M{
				"code":     bson.M{"bsonType": "string"},
				"name":     bson.M{"bsonType": "string"},
				"type":     bson.M{"enum": []string{"private", "casual", "match"}},
				"owner_id": bson.M{"bsonType": "long"},
			},
		},
	}
	if err := db.RunCommand(ctx, bson.D{
		{Key: "collMod", Value: "rooms"},
		{Key: "validator", Value: roomValidator},
		{Key: "validationLevel", Value: "moderate"},
	}).Err(); err != nil {
		return fmt.Errorf("rooms validation: %w", err)
	}

	return nil
}

func seedData(ctx context.Context, db *mongo.Database, log *zap.Logger) error {
	now := time.Now().UTC()

	users := db.Collection("users")
	if _, err := users.InsertOne(ctx, domain.User{
		ID:          bson.NewObjectID(),
		OnlineID:    1,
		Username:    "admin_seed",
		AvatarURL:   "https://a.ppy.sh/1",
		CountryCode: "CN",
		Roles:       []domain.UserRole{domain.RoleAdmin},
		IsBanned:    false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			log.Info("seed admin user already exists, skipping")
		} else {
			return fmt.Errorf("seed admin user: %w", err)
		}
	}

	beatmaps := db.Collection("beatmaps")
	if _, err := beatmaps.InsertOne(ctx, domain.Beatmap{
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
		CircleSize:        4,
		DrainRate:         5,
		ApproachRate:      9,
		OverallDifficulty: 8,
		CoverURL:          "https://assets.ppy.sh/beatmaps/500000/covers/cover.jpg",
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			log.Info("seed beatmap already exists, skipping")
		} else {
			return fmt.Errorf("seed beatmap: %w", err)
		}
	}

	rooms := db.Collection("rooms")
	seedRoomID := bson.NewObjectID()
	if _, err := rooms.InsertOne(ctx, domain.Room{
		ID:      seedRoomID,
		Code:    "SEED-ROOM",
		Name:    "Seed Room",
		Type:    domain.RoomTypeCasual,
		OwnerID: 1,
		Settings: domain.RoomSettings{
			Mappool:              domain.NewMappool(),
			RedStrategistUserID:  int64Ptr(1),
			BlueStrategistUserID: int64Ptr(2),
			FirstPick:            teamSidePtr(domain.TeamSideRed),
			FirstBan:             teamSidePtr(domain.TeamSideBlue),
			RedPlayers:           []int64{1, 2},
			BluePlayers:          []int64{3, 4},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			log.Info("seed room already exists, skipping")
		} else {
			return fmt.Errorf("seed room: %w", err)
		}
	}

	matches := db.Collection("matches")
	if _, err := matches.InsertOne(ctx, domain.Match{
		ID:       bson.NewObjectID(),
		RoomID:   seedRoomID,
		Code:     "SEED-001",
		Name:     "Seed Match",
		RoomType: domain.RoomTypeCasual,
		TeamRed: domain.Team{
			ID:      bson.NewObjectID(),
			Side:    domain.TeamSideRed,
			Name:    "Red",
			Color:   "#ef4444",
			Players: []int64{1, 2},
		},
		TeamBlue: domain.Team{
			ID:      bson.NewObjectID(),
			Side:    domain.TeamSideBlue,
			Name:    "Blue",
			Color:   "#3b82f6",
			Players: []int64{3, 4},
		},
		Mappool:   domain.NewMappool(),
		Board:     domain.NewBoard(),
		BPOrder:   domain.BPOrder{FirstPick: domain.TeamSideRed, FirstBan: domain.TeamSideBlue},
		TurnState: domain.NewTurnState(),
		Timer:     domain.NewTimerState(0, 0),
		Status:    domain.MatchStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			log.Info("seed match already exists, skipping")
		} else {
			return fmt.Errorf("seed match: %w", err)
		}
	}

	announcements := db.Collection("announcements")
	if _, err := announcements.InsertOne(ctx, domain.Announcement{
		ID:          bson.NewObjectID(),
		Title:       "Welcome to RCT Hub",
		Content:     "This is a sample announcement seeded during database initialization.",
		AuthorID:    1,
		Pinned:      true,
		Visible:     true,
		PublishedAt: &now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		return fmt.Errorf("seed announcement: %w", err)
	}

	return nil
}

func int64Ptr(n int64) *int64 {
	return &n
}

func teamSidePtr(s domain.TeamSide) *domain.TeamSide {
	return &s
}
