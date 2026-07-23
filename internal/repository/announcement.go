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

type AnnouncementRepository interface {
	Create(ctx context.Context, a *domain.Announcement) error
	Update(ctx context.Context, a *domain.Announcement) error
	ByID(ctx context.Context, id bson.ObjectID) (*domain.Announcement, error)
	ListVisible(ctx context.Context, params paginate.Params) (paginate.Result[domain.Announcement], error)
	ListAll(ctx context.Context, params paginate.Params) (paginate.Result[domain.Announcement], error)
	Delete(ctx context.Context, id bson.ObjectID) error
}

type announcementRepo struct {
	coll *mongo.Collection
}

func NewAnnouncementRepository(db *mongo.Database) AnnouncementRepository {
	return &announcementRepo{coll: db.Collection("announcements")}
}

func (r *announcementRepo) Create(ctx context.Context, a *domain.Announcement) error {
	now := time.Now().UTC()
	a.CreatedAt = now
	a.UpdatedAt = now
	_, err := r.coll.InsertOne(ctx, a)
	return err
}

func (r *announcementRepo) Update(ctx context.Context, a *domain.Announcement) error {
	a.UpdatedAt = time.Now().UTC()
	res, err := r.coll.UpdateOne(ctx, bson.M{"_id": a.ID}, bson.M{"$set": a})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (r *announcementRepo) ByID(ctx context.Context, id bson.ObjectID) (*domain.Announcement, error) {
	var a domain.Announcement
	err := r.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&a)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *announcementRepo) ListVisible(ctx context.Context, params paginate.Params) (paginate.Result[domain.Announcement], error) {
	params.Normalize()
	filter := bson.M{"visible": true, "published_at": bson.M{"$lte": time.Now().UTC()}}
	total, err := r.coll.CountDocuments(ctx, filter)
	if err != nil {
		return paginate.Result[domain.Announcement]{}, err
	}
	opts := options.Find().
		SetSkip(params.Skip()).
		SetLimit(params.PerPage).
		SetSort(bson.M{"pinned": -1, "published_at": -1})
	cur, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return paginate.Result[domain.Announcement]{}, err
	}
	defer cur.Close(ctx)
	var list []domain.Announcement
	if err := cur.All(ctx, &list); err != nil {
		return paginate.Result[domain.Announcement]{}, err
	}
	return paginate.NewResult(list, params, total), nil
}

func (r *announcementRepo) ListAll(ctx context.Context, params paginate.Params) (paginate.Result[domain.Announcement], error) {
	params.Normalize()
	filter := bson.M{}
	total, err := r.coll.CountDocuments(ctx, filter)
	if err != nil {
		return paginate.Result[domain.Announcement]{}, err
	}
	opts := options.Find().
		SetSkip(params.Skip()).
		SetLimit(params.PerPage).
		SetSort(bson.M{"created_at": -1})
	cur, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return paginate.Result[domain.Announcement]{}, err
	}
	defer cur.Close(ctx)
	var list []domain.Announcement
	if err := cur.All(ctx, &list); err != nil {
		return paginate.Result[domain.Announcement]{}, err
	}
	return paginate.NewResult(list, params, total), nil
}

func (r *announcementRepo) Delete(ctx context.Context, id bson.ObjectID) error {
	res, err := r.coll.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return errs.ErrNotFound
	}
	return nil
}
