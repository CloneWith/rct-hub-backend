package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/pkg/errs"
)

var (
	ErrFormalMatchAlreadyStarted   = errs.ErrFormalMatchAlreadyStarted
	ErrFormalMatchBootstrapInvalid = errors.New("invalid formal match bootstrap")
)

// FormalMatchBootstrapStore owns the transaction that creates the temporary
// legacy read-model shell and the authoritative MatchEngine snapshot together.
// M4 will invoke this boundary after authorization and command orchestration.
type FormalMatchBootstrapStore struct {
	client    *mongo.Client
	rooms     *mongo.Collection
	matches   *mongo.Collection
	snapshots *mongo.Collection
}

func NewFormalMatchBootstrapStore(client *mongo.Client, db *mongo.Database) *FormalMatchBootstrapStore {
	return &FormalMatchBootstrapStore{
		client:    client,
		rooms:     db.Collection("rooms"),
		matches:   db.Collection(legacyMatchesCollection),
		snapshots: db.Collection(MatchSnapshotsCollection),
	}
}

func (s *FormalMatchBootstrapStore) Create(
	ctx context.Context,
	roomID bson.ObjectID,
	legacyMatch domain.Match,
	state matchengine.State,
	now time.Time,
) error {
	if roomID == bson.NilObjectID || legacyMatch.ID == bson.NilObjectID || legacyMatch.RoomID != roomID ||
		(legacyMatch.RoomType != domain.RoomTypeMatch && legacyMatch.RoomType != domain.RoomTypeCasual && legacyMatch.RoomType != domain.RoomTypePrivate) || legacyMatch.Code == "" || now.IsZero() {
		return fmt.Errorf("%w: valid room, match, and timestamp are required", ErrFormalMatchBootstrapInvalid)
	}
	if legacyMatch.Status != domain.MatchStatusPending || state.Lifecycle != matchengine.LifecycleReady || state.Version != 0 {
		return fmt.Errorf("%w: bootstrap requires pending legacy shell and READY version 0 state", ErrFormalMatchBootstrapInvalid)
	}
	document, err := NewMatchSnapshotDocument(legacyMatch.ID, state, now)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrFormalMatchBootstrapInvalid, err)
	}
	legacyMatch.CreatedAt = now.UTC()
	legacyMatch.UpdatedAt = now.UTC()

	session, err := s.client.StartSession()
	if err != nil {
		return fmt.Errorf("start formal match transaction: %w", err)
	}
	defer session.EndSession(ctx)

	_, err = session.WithTransaction(ctx, func(txCtx context.Context) (any, error) {
		result, updateErr := s.rooms.UpdateOne(
			txCtx,
			bson.M{"_id": roomID, "match_id": nil},
			bson.M{"$set": bson.M{"match_id": legacyMatch.ID, "updated_at": now.UTC()}},
		)
		if updateErr != nil {
			return nil, updateErr
		}
		if result.MatchedCount == 0 {
			var room struct {
				ID      bson.ObjectID   `bson:"_id"`
				Type    domain.RoomType `bson:"type"`
				MatchID *bson.ObjectID  `bson:"match_id"`
			}
			findErr := s.rooms.FindOne(
				txCtx,
				bson.M{"_id": roomID},
				options.FindOne().SetProjection(bson.M{"_id": 1, "type": 1, "match_id": 1}),
			).Decode(&room)
			if errors.Is(findErr, mongo.ErrNoDocuments) {
				return nil, errs.ErrNotFound
			}
			if findErr != nil {
				return nil, findErr
			}
			if room.Type != domain.RoomTypeMatch && room.Type != domain.RoomTypeCasual && room.Type != domain.RoomTypePrivate {
				return nil, ErrFormalMatchBootstrapInvalid
			}
			return nil, ErrFormalMatchAlreadyStarted
		}
		if _, insertErr := s.matches.InsertOne(txCtx, legacyMatch); insertErr != nil {
			return nil, insertErr
		}
		if _, insertErr := s.snapshots.InsertOne(txCtx, document); insertErr != nil {
			return nil, insertErr
		}
		return nil, nil
	})
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrFormalMatchAlreadyStarted
		}
		return fmt.Errorf("create formal match atomically: %w", err)
	}
	return nil
}
