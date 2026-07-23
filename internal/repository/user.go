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

// UserRepository defines storage operations for users.
type UserRepository interface {
	Create(ctx context.Context, user *domain.User) error
	Update(ctx context.Context, user *domain.User) error
	ByID(ctx context.Context, id bson.ObjectID) (*domain.User, error)
	ByOsuID(ctx context.Context, osuID int64) (*domain.User, error)
	List(ctx context.Context, params paginate.Params) (paginate.Result[domain.User], error)
}

// userRepo is the MongoDB implementation of UserRepository.
type userRepo struct {
	coll *mongo.Collection
}

func NewUserRepository(db *mongo.Database) UserRepository {
	return &userRepo{coll: db.Collection("users")}
}

func (r *userRepo) Create(ctx context.Context, user *domain.User) error {
	now := time.Now().UTC()
	user.CreatedAt = now
	user.UpdatedAt = now
	_, err := r.coll.InsertOne(ctx, user)
	if mongo.IsDuplicateKeyError(err) {
		return errs.ErrAlreadyExists
	}
	return err
}

func (r *userRepo) Update(ctx context.Context, user *domain.User) error {
	user.UpdatedAt = time.Now().UTC()
	filter := bson.M{"_id": user.ID}
	update := bson.M{"$set": user}
	res, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (r *userRepo) ByID(ctx context.Context, id bson.ObjectID) (*domain.User, error) {
	var user domain.User
	err := r.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&user)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *userRepo) ByOsuID(ctx context.Context, osuID int64) (*domain.User, error) {
	var user domain.User
	err := r.coll.FindOne(ctx, bson.M{"id": osuID}).Decode(&user)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *userRepo) List(ctx context.Context, params paginate.Params) (paginate.Result[domain.User], error) {
	params.Normalize()
	filter := bson.M{"is_banned": false}
	total, err := r.coll.CountDocuments(ctx, filter)
	if err != nil {
		return paginate.Result[domain.User]{}, err
	}
	opts := options.Find().
		SetSkip(params.Skip()).
		SetLimit(params.PerPage).
		SetSort(bson.M{"created_at": -1})
	cur, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return paginate.Result[domain.User]{}, err
	}
	defer cur.Close(ctx)
	var users []domain.User
	if err := cur.All(ctx, &users); err != nil {
		return paginate.Result[domain.User]{}, err
	}
	return paginate.NewResult(users, params, total), nil
}
