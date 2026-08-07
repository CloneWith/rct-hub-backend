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

type MoveRepository interface {
	Create(ctx context.Context, move *domain.Move) error
	ByID(ctx context.Context, id bson.ObjectID) (*domain.Move, error)
	ByMatch(ctx context.Context, matchID bson.ObjectID, params paginate.Params) (paginate.Result[domain.Move], error)
	LatestByMatch(ctx context.Context, matchID bson.ObjectID, limit int64) ([]domain.Move, error)
}

type moveRepo struct {
	coll *mongo.Collection
}

func NewMoveRepository(db *mongo.Database) MoveRepository {
	return &moveRepo{coll: db.Collection("moves")}
}

func (r *moveRepo) Create(ctx context.Context, move *domain.Move) error {
	move.CreatedAt = time.Now().UTC()
	_, err := r.coll.InsertOne(ctx, move)
	return err
}

func (r *moveRepo) ByID(ctx context.Context, id bson.ObjectID) (*domain.Move, error) {
	var m domain.Move
	err := r.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *moveRepo) ByMatch(ctx context.Context, matchID bson.ObjectID, params paginate.Params) (paginate.Result[domain.Move], error) {
	params.Normalize()
	filter := bson.M{"match_id": matchID}
	total, err := r.coll.CountDocuments(ctx, filter)
	if err != nil {
		return paginate.Result[domain.Move]{}, err
	}
	opts := options.Find().
		SetSkip(params.Skip()).
		SetLimit(params.PerPage).
		SetSort(bson.D{{Key: "created_at", Value: 1}})
	cur, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return paginate.Result[domain.Move]{}, err
	}
	defer cur.Close(ctx)
	var moves []domain.Move
	if err := cur.All(ctx, &moves); err != nil {
		return paginate.Result[domain.Move]{}, err
	}
	return paginate.NewResult(moves, params, total), nil
}

func (r *moveRepo) LatestByMatch(ctx context.Context, matchID bson.ObjectID, limit int64) ([]domain.Move, error) {
	if limit <= 0 {
		limit = 50
	}
	opts := options.Find().
		SetLimit(limit).
		SetSort(bson.D{{Key: "created_at", Value: -1}})
	cur, err := r.coll.Find(ctx, bson.M{"match_id": matchID}, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var moves []domain.Move
	if err := cur.All(ctx, &moves); err != nil {
		return nil, err
	}
	return moves, nil
}
