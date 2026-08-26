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

// TeamRepository defines storage operations for team entities.
type TeamRepository interface {
	Create(ctx context.Context, t *domain.Team) error
	Update(ctx context.Context, t *domain.Team) error
	ByID(ctx context.Context, id bson.ObjectID) (*domain.Team, error)
	List(ctx context.Context, params paginate.Params, search string) (paginate.Result[domain.Team], error)
	Delete(ctx context.Context, id bson.ObjectID) error
	// RoomReferenceCount reports how many rooms link the team on either side.
	RoomReferenceCount(ctx context.Context, id bson.ObjectID) (int64, error)
}

type teamRepo struct {
	coll  *mongo.Collection
	rooms *mongo.Collection
}

func NewTeamRepository(db *mongo.Database) TeamRepository {
	return &teamRepo{
		coll:  db.Collection("teams"),
		rooms: db.Collection("rooms"),
	}
}

func (r *teamRepo) Create(ctx context.Context, t *domain.Team) error {
	if t.ID == bson.NilObjectID {
		t.ID = bson.NewObjectID()
	}
	now := time.Now().UTC()
	t.CreatedAt = now
	t.UpdatedAt = now
	_, err := r.coll.InsertOne(ctx, t)
	if mongo.IsDuplicateKeyError(err) {
		return errs.ErrAlreadyExists
	}
	return err
}

func (r *teamRepo) Update(ctx context.Context, t *domain.Team) error {
	t.UpdatedAt = time.Now().UTC()
	res, err := r.coll.UpdateOne(ctx, bson.M{"_id": t.ID}, bson.M{"$set": t})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (r *teamRepo) ByID(ctx context.Context, id bson.ObjectID) (*domain.Team, error) {
	var t domain.Team
	err := r.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&t)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *teamRepo) List(ctx context.Context, params paginate.Params, search string) (paginate.Result[domain.Team], error) {
	params.Normalize()
	filter := bson.M{}
	if search != "" {
		// Prefix search on name or seed keeps the query index-friendly.
		filter["$or"] = bson.A{
			bson.M{"name": bson.M{"$regex": regexp.QuoteMeta(search), "$options": "i"}},
			bson.M{"seed": bson.M{"$regex": regexp.QuoteMeta(search), "$options": "i"}},
		}
	}
	total, err := r.coll.CountDocuments(ctx, filter)
	if err != nil {
		return paginate.Result[domain.Team]{}, err
	}
	opts := options.Find().
		SetSkip(params.Skip()).
		SetLimit(params.PerPage).
		SetSort(bson.D{{Key: "created_at", Value: -1}})
	cur, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return paginate.Result[domain.Team]{}, err
	}
	defer cur.Close(ctx)
	var teams []domain.Team
	if err := cur.All(ctx, &teams); err != nil {
		return paginate.Result[domain.Team]{}, err
	}
	return paginate.NewResult(teams, params, total), nil
}

func (r *teamRepo) Delete(ctx context.Context, id bson.ObjectID) error {
	res, err := r.coll.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (r *teamRepo) RoomReferenceCount(ctx context.Context, id bson.ObjectID) (int64, error) {
	return r.rooms.CountDocuments(ctx, bson.M{
		"$or": bson.A{
			bson.M{"settings.red_team_id": id},
			bson.M{"settings.blue_team_id": id},
		},
	})
}
