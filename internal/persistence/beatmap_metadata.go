package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"rctHubBackend/internal/beatmapmetadata"
)

const BeatmapMetadataCollection = "beatmap_metadata_jobs"

type BeatmapMetadataStore struct{ collection *mongo.Collection }

type beatmapMetadataDocument struct {
	BeatmapID  int64                  `bson:"_id"`
	Status     beatmapmetadata.Status `bson:"status"`
	Attempts   int                    `bson:"attempts"`
	NextTryAt  time.Time              `bson:"next_try_at"`
	LastError  string                 `bson:"last_error,omitempty"`
	LeaseUntil *time.Time             `bson:"lease_until,omitempty"`
	LeaseToken string                 `bson:"lease_token,omitempty"`
	CreatedAt  time.Time              `bson:"created_at"`
	UpdatedAt  time.Time              `bson:"updated_at"`
}

func NewBeatmapMetadataStore(db *mongo.Database) *BeatmapMetadataStore {
	return &BeatmapMetadataStore{collection: db.Collection(BeatmapMetadataCollection)}
}

func (s *BeatmapMetadataStore) EnsureIndexes(ctx context.Context) error {
	_, err := s.collection.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "status", Value: 1}, {Key: "next_try_at", Value: 1}, {Key: "lease_until", Value: 1}}, Options: options.Index().SetName("beatmap_metadata_due")})
	return err
}
func (s *BeatmapMetadataStore) InstallValidator(ctx context.Context) error {
	if err := ensureCommandCollection(ctx, s.collection.Database(), BeatmapMetadataCollection); err != nil {
		return err
	}
	return s.collection.Database().RunCommand(ctx, bson.D{{Key: "collMod", Value: BeatmapMetadataCollection}, {Key: "validator", Value: BeatmapMetadataValidator()}, {Key: "validationLevel", Value: "strict"}, {Key: "validationAction", Value: "error"}}).Err()
}
func (s *BeatmapMetadataStore) VerifyValidator(ctx context.Context) error {
	return verifyCollectionValidator(ctx, s.collection.Database(), BeatmapMetadataCollection, BeatmapMetadataValidator())
}
func (s *BeatmapMetadataStore) Ensure(ctx context.Context, id int64, now time.Time) error {
	if id <= 0 {
		return fmt.Errorf("beatmap ID must be positive")
	}
	_, err := s.collection.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$setOnInsert": bson.M{"_id": id, "status": beatmapmetadata.StatusPending, "attempts": 0, "next_try_at": now.UTC(), "created_at": now.UTC(), "updated_at": now.UTC()}}, options.UpdateOne().SetUpsert(true))
	return err
}
func (s *BeatmapMetadataStore) Get(ctx context.Context, id int64) (beatmapmetadata.Record, error) {
	var document beatmapMetadataDocument
	if err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&document); errors.Is(err, mongo.ErrNoDocuments) {
		return beatmapmetadata.Record{}, beatmapmetadata.ErrRecordNotFound
	} else if err != nil {
		return beatmapmetadata.Record{}, err
	}
	return metadataRecord(document), nil
}
func (s *BeatmapMetadataStore) Claim(ctx context.Context, now, leaseUntil time.Time) (*beatmapmetadata.Record, error) {
	leaseToken := uuid.NewString()
	filter := bson.M{"status": bson.M{"$in": bson.A{beatmapmetadata.StatusPending, beatmapmetadata.StatusFailed}}, "next_try_at": bson.M{"$lte": now.UTC()}, "$or": bson.A{bson.M{"lease_until": bson.M{"$exists": false}}, bson.M{"lease_until": bson.M{"$lte": now.UTC()}}}}
	update := bson.M{"$set": bson.M{"status": beatmapmetadata.StatusPending, "lease_until": leaseUntil.UTC(), "lease_token": leaseToken, "updated_at": now.UTC()}}
	var document beatmapMetadataDocument
	err := s.collection.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetSort(bson.D{{Key: "next_try_at", Value: 1}}).SetReturnDocument(options.Before)).Decode(&document)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	record := metadataRecord(document)
	record.LeaseUntil = timePointer(leaseUntil.UTC())
	record.LeaseToken = leaseToken
	return &record, nil
}
func (s *BeatmapMetadataStore) MarkReady(ctx context.Context, id int64, leaseToken string, now time.Time) error {
	filter := bson.M{"_id": id}
	if leaseToken != "" {
		filter["status"] = beatmapmetadata.StatusPending
		filter["lease_token"] = leaseToken
	} else {
		filter["$or"] = bson.A{bson.M{"lease_token": bson.M{"$exists": false}}, bson.M{"lease_token": ""}}
	}
	result, err := s.collection.UpdateOne(ctx, filter, bson.M{"$set": bson.M{"status": beatmapmetadata.StatusReady, "updated_at": now.UTC()}, "$unset": bson.M{"lease_until": "", "lease_token": "", "last_error": ""}})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return beatmapmetadata.ErrLeaseLost
	}
	return nil
}
func (s *BeatmapMetadataStore) Fail(ctx context.Context, id int64, leaseToken, message string, retryAt, now time.Time) error {
	if leaseToken == "" {
		return fmt.Errorf("beatmap metadata lease token is required")
	}
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": id, "status": beatmapmetadata.StatusPending, "lease_token": leaseToken}, bson.M{"$set": bson.M{"status": beatmapmetadata.StatusFailed, "last_error": message, "next_try_at": retryAt.UTC(), "updated_at": now.UTC()}, "$unset": bson.M{"lease_until": "", "lease_token": ""}, "$inc": bson.M{"attempts": 1}})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return beatmapmetadata.ErrLeaseLost
	}
	return nil
}
func (s *BeatmapMetadataStore) Retry(ctx context.Context, id int64, now time.Time) error {
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": id, "status": beatmapmetadata.StatusFailed}, bson.M{"$set": bson.M{"status": beatmapmetadata.StatusPending, "next_try_at": now.UTC(), "updated_at": now.UTC()}, "$unset": bson.M{"lease_until": "", "lease_token": "", "last_error": ""}})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("beatmap metadata is not retryable")
	}
	return nil
}
func metadataRecord(d beatmapMetadataDocument) beatmapmetadata.Record {
	return beatmapmetadata.Record{BeatmapID: d.BeatmapID, Status: d.Status, Attempts: d.Attempts, NextTryAt: d.NextTryAt, LastError: d.LastError, LeaseUntil: d.LeaseUntil, LeaseToken: d.LeaseToken, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
}

func timePointer(value time.Time) *time.Time { return &value }
