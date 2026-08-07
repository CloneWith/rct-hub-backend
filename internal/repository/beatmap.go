package repository

import (
	"context"
	"errors"
	"time"

	"rctHubBackend/internal/domain"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/paginate"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type BeatmapRepository interface {
	Create(ctx context.Context, beatmap *domain.Beatmap) error
	Update(ctx context.Context, beatmap *domain.Beatmap) error
	UpsertOsuFields(ctx context.Context, osuID int64, fields bson.M) (*domain.Beatmap, error)
	ByID(ctx context.Context, id bson.ObjectID) (*domain.Beatmap, error)
	ByOsuID(ctx context.Context, osuID int64) (*domain.Beatmap, error)
	List(ctx context.Context, params paginate.Params) (paginate.Result[domain.Beatmap], error)
	Delete(ctx context.Context, id bson.ObjectID) error
}

type beatmapRepo struct {
	coll *mongo.Collection
}

func NewBeatmapRepository(db *mongo.Database) BeatmapRepository {
	return &beatmapRepo{coll: db.Collection("beatmaps")}
}

func (r *beatmapRepo) Create(ctx context.Context, beatmap *domain.Beatmap) error {
	now := time.Now().UTC()
	beatmap.CreatedAt = now
	beatmap.UpdatedAt = now
	_, err := r.coll.InsertOne(ctx, beatmap)
	if mongo.IsDuplicateKeyError(err) {
		return errs.ErrAlreadyExists
	}
	return err
}

func (r *beatmapRepo) Update(ctx context.Context, beatmap *domain.Beatmap) error {
	beatmap.UpdatedAt = time.Now().UTC()
	res, err := r.coll.UpdateOne(ctx, bson.M{"_id": beatmap.ID}, bson.M{"$set": beatmap})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// UpsertOsuFields atomically upserts a beatmap by osu! ID, setting only the
// provided API-owned fields via $set and defaulting local-only fields via
// $setOnInsert. It returns the full stored document after the operation.
func (r *beatmapRepo) UpsertOsuFields(ctx context.Context, osuID int64, fields bson.M) (*domain.Beatmap, error) {
	now := time.Now().UTC()
	setFields := bson.M{"updated_at": now}
	for k, v := range fields {
		setFields[k] = v
	}
	update := bson.M{
		"$set": setFields,
		"$setOnInsert": bson.M{
			"_id":        bson.NewObjectID(),
			"created_at": now,
		},
	}
	opts := options.FindOneAndUpdate().
		SetUpsert(true).
		SetReturnDocument(options.After)
	var b domain.Beatmap
	err := r.coll.FindOneAndUpdate(ctx, bson.M{"id": osuID}, update, opts).Decode(&b)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *beatmapRepo) ByID(ctx context.Context, id bson.ObjectID) (*domain.Beatmap, error) {
	var b domain.Beatmap
	err := r.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&b)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *beatmapRepo) ByOsuID(ctx context.Context, osuID int64) (*domain.Beatmap, error) {
	var b domain.Beatmap
	err := r.coll.FindOne(ctx, bson.M{"id": osuID}).Decode(&b)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *beatmapRepo) List(ctx context.Context, params paginate.Params) (paginate.Result[domain.Beatmap], error) {
	params.Normalize()
	total, err := r.coll.CountDocuments(ctx, bson.M{})
	if err != nil {
		return paginate.Result[domain.Beatmap]{}, err
	}
	opts := options.Find().
		SetSkip(params.Skip()).
		SetLimit(params.PerPage).
		SetSort(bson.D{{Key: "created_at", Value: -1}})
	cur, err := r.coll.Find(ctx, bson.M{}, opts)
	if err != nil {
		return paginate.Result[domain.Beatmap]{}, err
	}
	defer cur.Close(ctx)
	var beatmaps []domain.Beatmap
	if err := cur.All(ctx, &beatmaps); err != nil {
		return paginate.Result[domain.Beatmap]{}, err
	}
	return paginate.NewResult(beatmaps, params, total), nil
}

func (r *beatmapRepo) Delete(ctx context.Context, id bson.ObjectID) error {
	res, err := r.coll.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return errs.ErrNotFound
	}
	return nil
}
