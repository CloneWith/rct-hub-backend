package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/fatih/color"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.uber.org/zap"

	"rctHubBackend/internal/config"
	"rctHubBackend/internal/database"
	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/logger"
	"rctHubBackend/internal/persistence"
)

func main() {
	color.Blue("=== RCT Database Initialisation ===")
	var (
		drop      = flag.Bool("drop", false, "drop existing collections before recreating")
		seed      = flag.Bool("seed", false, "insert sample seed data")
		adminID   = flag.Int64("admin-id", 0, "osu! user ID of the admin to create (enables admin creation when > 0)")
		adminName = flag.String("admin-name", "", "username for the admin user (defaults to \"admin\")")
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
	defer func() { _ = logger.Close() }()

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
	log.Info("seeding data")
	defer cancel()

	collections := []string{
		"users",
		"beatmaps",
		"rooms",
		"matches",
		"match_snapshots",
		"match_command_receipts",
		"match_action_log",
		"match_outbox",
		persistence.IRCJobsCollection,
		persistence.IRCJobsCollection + "_locks",
		persistence.IRCObservationsCollection,
		persistence.BeatmapMetadataCollection,
		"moves",
		"results",
		"announcements",
		"teams",
		"mappools",
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
	if err := backfillFormalRoomReferees(ctx, db.MongoDB, log); err != nil {
		log.Error("failed to backfill formal room referees", zap.Error(err))
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

	if *adminID > 0 {
		name := *adminName
		if name == "" {
			name = "admin"
		}
		if err := createAdminUser(ctx, db.MongoDB, log, *adminID, name); err != nil {
			log.Error("failed to create admin user", zap.Error(err))
			os.Exit(1)
		}
	} else {
		color.Yellow("Not going to create an admin user.")
		color.Yellow("You may add one manually before accessing the admin panel.")
	}

	log.Info("database initialization complete", zap.String("database", cfg.MongoDB.Name))
}

// backfillFormalRoomReferees makes the explicit referee relationship safe for
// existing data. Historical formal rooms used owner_id as their referee.
// The update is idempotent and only touches formal rooms without a referee.
func backfillFormalRoomReferees(ctx context.Context, db *mongo.Database, log *zap.Logger) error {
	result, err := db.Collection("rooms").UpdateMany(ctx,
		bson.M{"type": string(domain.RoomTypeMatch), "referee_user_id": bson.M{"$exists": false}},
		bson.A{bson.M{"$set": bson.M{"referee_user_id": "$owner_id"}}},
	)
	if err != nil {
		return fmt.Errorf("backfill formal room referees: %w", err)
	}
	if result.ModifiedCount > 0 {
		log.Info("backfilled formal room referees", zap.Int64("count", result.ModifiedCount))
	}
	return nil
}

func ensureSchemaValidation(ctx context.Context, db *mongo.Database) error {
	userValidator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"id", "username", "roles", "verify_status", "created_at", "updated_at"},
			"properties": bson.M{
				"id":       bson.M{"bsonType": "long"},
				"username": bson.M{"bsonType": "string"},
				"verify_status": bson.M{
					"bsonType": "string",
					"enum":     []string{"verified", "pending", "unverified"},
				},
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
				"code":            bson.M{"bsonType": "string"},
				"name":            bson.M{"bsonType": "string"},
				"type":            bson.M{"enum": []string{"private", "casual", "match"}},
				"owner_id":        bson.M{"bsonType": "long"},
				"referee_user_id": bson.M{"bsonType": []string{"long", "null"}},
				"round":           bson.M{"bsonType": "string"},
				"scheduled_at":    bson.M{"bsonType": []string{"date", "null"}},
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

	teamValidator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"name", "created_at", "updated_at"},
			"properties": bson.M{
				"name":          bson.M{"bsonType": "string"},
				"leader_id":     bson.M{"bsonType": []string{"long", "null"}},
				"strategist_id": bson.M{"bsonType": []string{"long", "null"}},
				"players":       bson.M{"bsonType": "array"},
			},
		},
	}
	if err := db.RunCommand(ctx, bson.D{
		{Key: "collMod", Value: "teams"},
		{Key: "validator", Value: teamValidator},
		{Key: "validationLevel", Value: "moderate"},
	}).Err(); err != nil {
		return fmt.Errorf("teams validation: %w", err)
	}

	mappoolValidator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"name", "entries", "created_at", "updated_at"},
			"properties": bson.M{
				"name": bson.M{"bsonType": "string"},
				"entries": bson.M{
					"bsonType": "array",
					"items": bson.M{
						"bsonType": "object",
						"required": []string{"mod", "index"},
						"properties": bson.M{
							"mod":   bson.M{"enum": []string{"NM", "HD", "HR", "DT", "FM", "Shiro", "TB"}},
							"index": bson.M{"bsonType": "int"},
						},
					},
				},
			},
		},
	}
	if err := db.RunCommand(ctx, bson.D{
		{Key: "collMod", Value: "mappools"},
		{Key: "validator", Value: mappoolValidator},
		{Key: "validationLevel", Value: "moderate"},
	}).Err(); err != nil {
		return fmt.Errorf("mappools validation: %w", err)
	}

	if err := persistence.NewSnapshotStore(db).InstallValidator(ctx); err != nil {
		return err
	}
	if err := persistence.NewCommandStore(db.Client(), db).InstallValidators(ctx); err != nil {
		return err
	}
	if err := persistence.NewIRCJobStore(db).InstallValidator(ctx); err != nil {
		return err
	}
	if err := persistence.NewIRCObservationStore(db).InstallValidator(ctx); err != nil {
		return err
	}
	if err := persistence.NewBeatmapMetadataStore(db).InstallValidator(ctx); err != nil {
		return err
	}

	return nil
}

// createAdminUser inserts a user document with the admin role.
// If a user with the same osu! ID already exists, it logs and skips.
func createAdminUser(ctx context.Context, db *mongo.Database, log *zap.Logger, osuID int64, username string) error {
	now := time.Now().UTC()
	user := domain.User{
		ID:           bson.NewObjectID(),
		OnlineID:     osuID,
		Username:     username,
		AvatarURL:    fmt.Sprintf("https://a.ppy.sh/%d", osuID),
		CountryCode:  "__",
		Roles:        []domain.UserRole{domain.RoleAdmin},
		VerifyStatus: domain.Verified,
		IsBanned:     false,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	log.Info("creating admin user",
		zap.Int64("id", osuID),
		zap.String("username", username),
	)

	if _, err := db.Collection("users").InsertOne(ctx, user); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			log.Info("admin user already exists, skipping",
				zap.Int64("id", osuID),
				zap.String("username", username),
			)
			return nil
		}
		return fmt.Errorf("create admin user: %w", err)
	}

	color.Green("Admin user created (osu! ID: %d, username: %s)", osuID, username)
	log.Info("admin user created",
		zap.Int64("id", osuID),
		zap.String("username", username),
		zap.String("user_id", user.ID.Hex()),
	)
	return nil
}

func seedData(ctx context.Context, db *mongo.Database, log *zap.Logger) error {
	seed := BuildSeedData(time.Now().UTC())

	users := db.Collection("users")
	if _, err := users.InsertOne(ctx, seed.Admin); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			log.Info("seed admin user already exists, skipping")
		} else {
			return fmt.Errorf("seed admin user: %w", err)
		}
	}

	beatmaps := db.Collection("beatmaps")
	if _, err := beatmaps.InsertOne(ctx, seed.Beatmap); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			log.Info("seed beatmap already exists, skipping")
		} else {
			return fmt.Errorf("seed beatmap: %w", err)
		}
	}

	teams := db.Collection("teams")
	if _, err := teams.InsertOne(ctx, seed.RedTeam); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			log.Info("seed red team already exists, skipping")
		} else {
			return fmt.Errorf("seed red team: %w", err)
		}
	}
	if _, err := teams.InsertOne(ctx, seed.BlueTeam); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			log.Info("seed blue team already exists, skipping")
		} else {
			return fmt.Errorf("seed blue team: %w", err)
		}
	}

	mappools := db.Collection("mappools")
	if _, err := mappools.InsertOne(ctx, seed.Mappool); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			log.Info("seed mappool already exists, skipping")
		} else {
			return fmt.Errorf("seed mappool: %w", err)
		}
	}

	rooms := db.Collection("rooms")
	if _, err := rooms.InsertOne(ctx, seed.Room); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			log.Info("seed room already exists, skipping")
		} else {
			return fmt.Errorf("seed room: %w", err)
		}
	}

	matches := db.Collection("matches")
	if _, err := matches.InsertOne(ctx, seed.Match); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			log.Info("seed match already exists, skipping")
		} else {
			return fmt.Errorf("seed match: %w", err)
		}
	}

	announcements := db.Collection("announcements")
	if _, err := announcements.InsertOne(ctx, seed.Announcement); err != nil {
		return fmt.Errorf("seed announcement: %w", err)
	}

	return nil
}
