package database

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"rctHubBackend/internal/config"
	"rctHubBackend/internal/persistence"
)

// DB holds all external data store clients.
type DB struct {
	Mongo   *mongo.Client
	MongoDB *mongo.Database
	Redis   *redis.Client
}

// New initializes MongoDB and Redis clients.
func New(cfg *config.Config) (*DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mongoClient, err := mongo.Connect(options.Client().ApplyURI(cfg.MongoDB.URI))
	if err != nil {
		return nil, fmt.Errorf("connect to mongodb: %w", err)
	}

	if err := mongoClient.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("ping mongodb: %w", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &DB{
		Mongo:   mongoClient,
		MongoDB: mongoClient.Database(cfg.MongoDB.Name),
		Redis:   rdb,
	}, nil
}

// Close gracefully shuts down connections.
func (db *DB) Close(ctx context.Context) error {
	if err := db.Mongo.Disconnect(ctx); err != nil {
		return err
	}
	return db.Redis.Close()
}

// EnsureIndexes creates indexes for core collections.
func (db *DB) EnsureIndexes(ctx context.Context) error {
	userColl := db.MongoDB.Collection("users")
	if _, err := userColl.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "id", Value: 1}},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		return fmt.Errorf("users id index: %w", err)
	}

	beatmapColl := db.MongoDB.Collection("beatmaps")
	if _, err := beatmapColl.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "id", Value: 1}},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		return fmt.Errorf("beatmaps id index: %w", err)
	}

	roomColl := db.MongoDB.Collection("rooms")
	if _, err := roomColl.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "code", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "owner_id", Value: 1}}},
		{Keys: bson.D{{Key: "type", Value: 1}, {Key: "created_at", Value: -1}}},
		{Keys: bson.D{{Key: "round", Value: 1}, {Key: "created_at", Value: -1}}},
		{Keys: bson.D{{Key: "match_id", Value: 1}}},
		{Keys: bson.D{{Key: "referee_user_id", Value: 1}}},
		{Keys: bson.D{{Key: "settings.red_strategist_user_id", Value: 1}}},
		{Keys: bson.D{{Key: "settings.blue_strategist_user_id", Value: 1}}},
		{Keys: bson.D{{Key: "settings.streamer_user_id", Value: 1}}},
	}); err != nil {
		return fmt.Errorf("rooms indexes: %w", err)
	}

	matchColl := db.MongoDB.Collection("matches")
	if _, err := matchColl.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "room_id", Value: 1}}},
		{Keys: bson.D{{Key: "code", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "started_at", Value: -1}}},
	}); err != nil {
		return fmt.Errorf("matches indexes: %w", err)
	}

	moveColl := db.MongoDB.Collection("moves")
	if _, err := moveColl.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "match_id", Value: 1}, {Key: "created_at", Value: 1}}},
		{Keys: bson.D{{Key: "room_id", Value: 1}, {Key: "created_at", Value: 1}}},
		{Keys: bson.D{{Key: "operator_id", Value: 1}}},
	}); err != nil {
		return fmt.Errorf("moves indexes: %w", err)
	}

	resultColl := db.MongoDB.Collection("results")
	if _, err := resultColl.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "match_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		return fmt.Errorf("results match_id index: %w", err)
	}

	announcementColl := db.MongoDB.Collection("announcements")
	if _, err := announcementColl.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "pinned", Value: -1}, {Key: "published_at", Value: -1}}},
	}); err != nil {
		return fmt.Errorf("announcements indexes: %w", err)
	}

	snapshotStore := persistence.NewSnapshotStore(db.MongoDB)
	if err := snapshotStore.EnsureIndexes(ctx); err != nil {
		return err
	}
	commandStore := persistence.NewCommandStore(db.Mongo, db.MongoDB)
	if err := commandStore.EnsureIndexes(ctx); err != nil {
		return err
	}
	if err := persistence.NewIRCJobStore(db.MongoDB).EnsureIndexes(ctx); err != nil {
		return fmt.Errorf("IRC job indexes: %w", err)
	}
	if err := persistence.NewIRCObservationStore(db.MongoDB).EnsureIndexes(ctx); err != nil {
		return fmt.Errorf("IRC observation indexes: %w", err)
	}
	if err := persistence.NewBeatmapMetadataStore(db.MongoDB).EnsureIndexes(ctx); err != nil {
		return fmt.Errorf("beatmap metadata indexes: %w", err)
	}

	return nil
}

func (db *DB) VerifySchema(ctx context.Context) error {
	if err := persistence.NewSnapshotStore(db.MongoDB).VerifyValidator(ctx); err != nil {
		return err
	}
	if err := persistence.NewCommandStore(db.Mongo, db.MongoDB).VerifyValidators(ctx); err != nil {
		return err
	}
	if err := persistence.NewIRCJobStore(db.MongoDB).VerifyValidator(ctx); err != nil {
		return err
	}
	if err := persistence.NewIRCObservationStore(db.MongoDB).VerifyValidator(ctx); err != nil {
		return err
	}
	return persistence.NewBeatmapMetadataStore(db.MongoDB).VerifyValidator(ctx)
}
