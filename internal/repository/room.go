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

// RoomRepository defines storage operations for rooms.
type RoomRepository interface {
	Create(ctx context.Context, room *domain.Room) error
	Update(ctx context.Context, room *domain.Room) error
	UpdateFields(ctx context.Context, id bson.ObjectID, fields bson.M, requireSetupOpen bool) error
	ByID(ctx context.Context, id bson.ObjectID) (*domain.Room, error)
	ByCode(ctx context.Context, code string) (*domain.Room, error)
	List(ctx context.Context, params paginate.Params, roomType *domain.RoomType) (paginate.Result[domain.Room], error)
	Delete(ctx context.Context, id bson.ObjectID) error
}

type roomRepo struct {
	coll *mongo.Collection
}

func NewRoomRepository(db *mongo.Database) RoomRepository {
	return &roomRepo{coll: db.Collection("rooms")}
}

func (r *roomRepo) Create(ctx context.Context, room *domain.Room) error {
	if room.ID == bson.NilObjectID {
		room.ID = bson.NewObjectID()
	}
	now := time.Now().UTC()
	room.CreatedAt = now
	room.UpdatedAt = now
	_, err := r.coll.InsertOne(ctx, room)
	if mongo.IsDuplicateKeyError(err) {
		return errs.ErrAlreadyExists
	}
	return err
}

func (r *roomRepo) Update(ctx context.Context, room *domain.Room) error {
	room.UpdatedAt = time.Now().UTC()
	res, err := r.coll.UpdateOne(ctx, bson.M{"_id": room.ID}, bson.M{"$set": room})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (r *roomRepo) UpdateFields(ctx context.Context, id bson.ObjectID, fields bson.M, requireSetupOpen bool) error {
	filter := bson.M{"_id": id}
	if requireSetupOpen {
		filter["match_id"] = nil
	}
	set := bson.M{"updated_at": time.Now().UTC()}
	for key, value := range fields {
		set[key] = value
	}
	result, err := r.coll.UpdateOne(ctx, filter, bson.M{"$set": set})
	if err != nil {
		return err
	}
	if result.MatchedCount > 0 {
		return nil
	}
	if _, err := r.ByID(ctx, id); err != nil {
		return err
	}
	if requireSetupOpen {
		return errs.ErrConflict
	}
	return errs.ErrNotFound
}

func (r *roomRepo) ByID(ctx context.Context, id bson.ObjectID) (*domain.Room, error) {
	var room domain.Room
	err := r.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&room)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &room, nil
}

func (r *roomRepo) ByCode(ctx context.Context, code string) (*domain.Room, error) {
	var room domain.Room
	err := r.coll.FindOne(ctx, bson.M{"code": code}).Decode(&room)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &room, nil
}

func (r *roomRepo) List(ctx context.Context, params paginate.Params, roomType *domain.RoomType) (paginate.Result[domain.Room], error) {
	params.Normalize()
	filter := bson.M{}
	if roomType != nil {
		filter["type"] = *roomType
	}
	total, err := r.coll.CountDocuments(ctx, filter)
	if err != nil {
		return paginate.Result[domain.Room]{}, err
	}
	opts := options.Find().
		SetSkip(params.Skip()).
		SetLimit(params.PerPage).
		SetSort(bson.D{{Key: "created_at", Value: -1}})
	cur, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return paginate.Result[domain.Room]{}, err
	}
	defer cur.Close(ctx)
	var rooms []domain.Room
	if err := cur.All(ctx, &rooms); err != nil {
		return paginate.Result[domain.Room]{}, err
	}
	return paginate.NewResult(rooms, params, total), nil
}

func (r *roomRepo) Delete(ctx context.Context, id bson.ObjectID) error {
	res, err := r.coll.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return errs.ErrNotFound
	}
	return nil
}
