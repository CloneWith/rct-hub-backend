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

type MatchRepository interface {
	Create(ctx context.Context, match *domain.Match) error
	Update(ctx context.Context, match *domain.Match) error
	ByID(ctx context.Context, id bson.ObjectID) (*domain.Match, error)
	ByCode(ctx context.Context, code string) (*domain.Match, error)
	List(ctx context.Context, params paginate.Params, status *domain.MatchStatus) (paginate.Result[domain.Match], error)
	ListFormal(ctx context.Context, params paginate.Params) (paginate.Result[domain.Match], error)
}

type matchRepo struct {
	coll *mongo.Collection
}

func NewMatchRepository(db *mongo.Database) MatchRepository {
	return &matchRepo{coll: db.Collection("matches")}
}

func (r *matchRepo) Create(ctx context.Context, match *domain.Match) error {
	if match.ID == bson.NilObjectID {
		match.ID = bson.NewObjectID()
	}
	now := time.Now().UTC()
	match.CreatedAt = now
	match.UpdatedAt = now
	if match.Status == "" {
		match.Status = domain.MatchStatusPending
	}
	_, err := r.coll.InsertOne(ctx, match)
	if mongo.IsDuplicateKeyError(err) {
		return errs.ErrAlreadyExists
	}
	return err
}

func (r *matchRepo) Update(ctx context.Context, match *domain.Match) error {
	match.UpdatedAt = time.Now().UTC()
	res, err := r.coll.UpdateOne(ctx, bson.M{"_id": match.ID}, bson.M{"$set": match})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (r *matchRepo) ByID(ctx context.Context, id bson.ObjectID) (*domain.Match, error) {
	var m domain.Match
	err := r.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *matchRepo) ByCode(ctx context.Context, code string) (*domain.Match, error) {
	var m domain.Match
	err := r.coll.FindOne(ctx, bson.M{"code": code}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *matchRepo) List(ctx context.Context, params paginate.Params, status *domain.MatchStatus) (paginate.Result[domain.Match], error) {
	params.Normalize()
	filter := bson.M{}
	if status != nil {
		filter["status"] = *status
	}
	total, err := r.coll.CountDocuments(ctx, filter)
	if err != nil {
		return paginate.Result[domain.Match]{}, err
	}
	opts := options.Find().
		SetSkip(params.Skip()).
		SetLimit(params.PerPage).
		SetSort(bson.D{{Key: "created_at", Value: -1}})
	cur, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return paginate.Result[domain.Match]{}, err
	}
	defer cur.Close(ctx)
	var matches []domain.Match
	if err := cur.All(ctx, &matches); err != nil {
		return paginate.Result[domain.Match]{}, err
	}
	return paginate.NewResult(matches, params, total), nil
}

// ListFormal pages only tournament-room match shells. Authoritative state is
// batch-loaded separately by FormalMatchReadService.
func (r *matchRepo) ListFormal(ctx context.Context, params paginate.Params) (paginate.Result[domain.Match], error) {
	params.Normalize()
	filter := bson.M{"room_type": domain.RoomTypeMatch}
	total, err := r.coll.CountDocuments(ctx, filter)
	if err != nil {
		return paginate.Result[domain.Match]{}, err
	}
	opts := options.Find().SetSkip(params.Skip()).SetLimit(params.PerPage).SetSort(bson.D{{Key: "created_at", Value: -1}})
	cursor, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return paginate.Result[domain.Match]{}, err
	}
	defer cursor.Close(ctx)
	var matches []domain.Match
	if err := cursor.All(ctx, &matches); err != nil {
		return paginate.Result[domain.Match]{}, err
	}
	return paginate.NewResult(matches, params, total), nil
}
