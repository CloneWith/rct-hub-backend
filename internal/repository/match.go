package repository

import (
	"context"
	"errors"
	"fmt"
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
	// UpdateReadiness atomically flips the named team's readiness bit on
	// the StrategistReadiness subdocument. The caller can attach a
	// requiredStatus filter so the update only applies when the match is
	// still in the expected lifecycle (e.g. pending). When the underlying
	// document is missing entirely the call returns ErrNotFound; when the
	// document exists but does not match the lifecycle filter, it returns
	// ErrConflict.
	UpdateReadiness(ctx context.Context, matchID bson.ObjectID, side domain.TeamSide, requireStatus domain.MatchStatus) (*domain.Match, error)
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

// UpdateReadiness atomically sets the named side's readiness bit on the
// strategist_readiness sub-document. The filter pins the expected status so
// a concurrent state transition (e.g. a referee firing START_MATCH) cannot
// silently resurrect a stale readiness write. The returned Match reflects
// the post-update state so callers can immediately read the partner side's
// bit and decide whether to trigger the auto-start path.
func (r *matchRepo) UpdateReadiness(ctx context.Context, matchID bson.ObjectID, side domain.TeamSide, requireStatus domain.MatchStatus) (*domain.Match, error) {
	field := ""
	switch side {
	case domain.TeamSideRed:
		field = "strategist_readiness.red_ready"
	case domain.TeamSideBlue:
		field = "strategist_readiness.blue_ready"
	default:
		return nil, fmt.Errorf("%w: readiness side must be red or blue", errs.ErrInvalidInput)
	}
	filter := bson.M{"_id": matchID}
	if requireStatus != "" {
		filter["status"] = requireStatus
	}
	update := bson.M{
		"$set": bson.M{
			field:        true,
			"updated_at": time.Now().UTC(),
		},
	}
	after := options.After
	res := r.coll.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(after).SetUpsert(false))
	var m domain.Match
	if err := res.Decode(&m); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			// Distinguish "match is gone" from "match is in the wrong
			// status" so callers can surface the right error to the
			// strategist UI.
			if _, lookupErr := r.ByID(ctx, matchID); lookupErr != nil {
				return nil, lookupErr
			}
			return nil, errs.ErrConflict
		}
		return nil, err
	}
	return &m, nil
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
