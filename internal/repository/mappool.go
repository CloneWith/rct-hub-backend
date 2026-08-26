package repository

import (
	"context"
	"errors"
	"regexp"
	"time"

	"rctHubBackend/internal/domain"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/paginate"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MappoolRepository defines storage operations for mappool entities.
type MappoolRepository interface {
	Create(ctx context.Context, m *domain.Mappool) error
	Update(ctx context.Context, m *domain.Mappool) error
	ByID(ctx context.Context, id bson.ObjectID) (*domain.Mappool, error)
	List(ctx context.Context, params paginate.Params, search string) (paginate.Result[domain.Mappool], error)
	Delete(ctx context.Context, id bson.ObjectID) error
	// RoomReferenceCount reports how many rooms link the mappool.
	RoomReferenceCount(ctx context.Context, id bson.ObjectID) (int64, error)
}

type mappoolRepo struct {
	coll  *mongo.Collection
	rooms *mongo.Collection
}

func NewMappoolRepository(db *mongo.Database) MappoolRepository {
	return &mappoolRepo{
		coll:  db.Collection("mappools"),
		rooms: db.Collection("rooms"),
	}
}

func (r *mappoolRepo) Create(ctx context.Context, m *domain.Mappool) error {
	if m.ID == bson.NilObjectID {
		m.ID = bson.NewObjectID()
	}
	now := time.Now().UTC()
	m.CreatedAt = now
	m.UpdatedAt = now
	if m.Entries == nil {
		m.Entries = []domain.MappoolEntry{}
	}
	_, err := r.coll.InsertOne(ctx, m)
	if mongo.IsDuplicateKeyError(err) {
		return errs.ErrAlreadyExists
	}
	return err
}

func (r *mappoolRepo) Update(ctx context.Context, m *domain.Mappool) error {
	m.UpdatedAt = time.Now().UTC()
	if m.Entries == nil {
		m.Entries = []domain.MappoolEntry{}
	}
	res, err := r.coll.UpdateOne(ctx, bson.M{"_id": m.ID}, bson.M{"$set": m})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (r *mappoolRepo) ByID(ctx context.Context, id bson.ObjectID) (*domain.Mappool, error) {
	var m domain.Mappool
	err := r.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *mappoolRepo) List(ctx context.Context, params paginate.Params, search string) (paginate.Result[domain.Mappool], error) {
	params.Normalize()
	filter := bson.M{}
	if search != "" {
		filter["name"] = bson.M{"$regex": regexp.QuoteMeta(search), "$options": "i"}
	}
	total, err := r.coll.CountDocuments(ctx, filter)
	if err != nil {
		return paginate.Result[domain.Mappool]{}, err
	}
	opts := options.Find().
		SetSkip(params.Skip()).
		SetLimit(params.PerPage).
		SetSort(bson.D{{Key: "created_at", Value: -1}})
	cur, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return paginate.Result[domain.Mappool]{}, err
	}
	defer cur.Close(ctx)
	var pools []domain.Mappool
	if err := cur.All(ctx, &pools); err != nil {
		return paginate.Result[domain.Mappool]{}, err
	}
	return paginate.NewResult(pools, params, total), nil
}

func (r *mappoolRepo) Delete(ctx context.Context, id bson.ObjectID) error {
	res, err := r.coll.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (r *mappoolRepo) RoomReferenceCount(ctx context.Context, id bson.ObjectID) (int64, error) {
	return r.rooms.CountDocuments(ctx, bson.M{"settings.mappool_id": id})
}
