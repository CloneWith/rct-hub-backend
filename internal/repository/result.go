package repository

import (
	"context"
	"errors"
	"time"

	"rctHubBackend/internal/domain"
	"rctHubBackend/pkg/errs"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type ResultRepository interface {
	Create(ctx context.Context, result *domain.MatchResult) error
	Update(ctx context.Context, result *domain.MatchResult) error
	ByMatchID(ctx context.Context, matchID bson.ObjectID) (*domain.MatchResult, error)
	ByID(ctx context.Context, id bson.ObjectID) (*domain.MatchResult, error)
}

type resultRepo struct {
	coll *mongo.Collection
}

func NewResultRepository(db *mongo.Database) ResultRepository {
	return &resultRepo{coll: db.Collection("results")}
}

func (r *resultRepo) Create(ctx context.Context, result *domain.MatchResult) error {
	now := time.Now().UTC()
	result.CreatedAt = now
	result.UpdatedAt = now
	_, err := r.coll.InsertOne(ctx, result)
	if mongo.IsDuplicateKeyError(err) {
		return errs.ErrAlreadyExists
	}
	return err
}

func (r *resultRepo) Update(ctx context.Context, result *domain.MatchResult) error {
	result.UpdatedAt = time.Now().UTC()
	res, err := r.coll.UpdateOne(ctx, bson.M{"_id": result.ID}, bson.M{"$set": result})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (r *resultRepo) ByMatchID(ctx context.Context, matchID bson.ObjectID) (*domain.MatchResult, error) {
	var res domain.MatchResult
	err := r.coll.FindOne(ctx, bson.M{"match_id": matchID}).Decode(&res)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (r *resultRepo) ByID(ctx context.Context, id bson.ObjectID) (*domain.MatchResult, error) {
	var res domain.MatchResult
	err := r.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&res)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &res, nil
}
