package repository

import (
	"context"
	"errors"
	"regexp"
	"strings"
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
	List(ctx context.Context, params paginate.Params, filter RoomListFilter) (paginate.Result[domain.Room], error)
	Delete(ctx context.Context, id bson.ObjectID) error
}

// RoomListFilter contains all server-side room directory filters. Lifecycle is
// read from the authoritative match snapshot, never from the legacy match
// shell.
type RoomListFilter struct {
	Type          *domain.RoomType
	Search        string
	Round         string
	Lifecycle     string
	RelatedUserID *int64
}

type roomRepo struct {
	coll  *mongo.Collection
	teams *mongo.Collection
}

func NewRoomRepository(db *mongo.Database) RoomRepository {
	return &roomRepo{coll: db.Collection("rooms"), teams: db.Collection("teams")}
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

func (r *roomRepo) List(ctx context.Context, params paginate.Params, filter RoomListFilter) (paginate.Result[domain.Room], error) {
	params.Normalize()
	match := bson.M{}
	var conditions bson.A
	if filter.Type != nil {
		match["type"] = *filter.Type
	}
	if filter.Search = strings.TrimSpace(filter.Search); filter.Search != "" {
		conditions = append(conditions, bson.M{"$or": bson.A{
			bson.M{"name": bson.M{"$regex": regexp.QuoteMeta(filter.Search), "$options": "i"}},
			bson.M{"code": bson.M{"$regex": regexp.QuoteMeta(filter.Search), "$options": "i"}},
		}})
	}
	if filter.Round = strings.TrimSpace(filter.Round); filter.Round != "" {
		match["round"] = filter.Round
	}
	if filter.RelatedUserID != nil {
		userID := *filter.RelatedUserID
		related := bson.M{"$or": bson.A{
			bson.M{"owner_id": userID},
			bson.M{"referee_user_id": userID},
			bson.M{"settings.streamer_user_id": userID},
		}}
		// Team membership (leader / strategist / player) is resolved through
		// the linked team entities rather than the room settings.
		teamIDs := r.userTeamIDs(ctx, userID)
		if len(teamIDs) > 0 {
			related["$or"] = append(related["$or"].(bson.A),
				bson.M{"settings.red_team_id": bson.M{"$in": teamIDs}},
				bson.M{"settings.blue_team_id": bson.M{"$in": teamIDs}},
			)
		}
		conditions = append(conditions, related)
	}
	if len(conditions) > 0 {
		match["$and"] = conditions
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
	}
	if filter.Lifecycle != "" {
		pipeline = append(pipeline,
			bson.D{{Key: "$lookup", Value: bson.M{
				"from":         "match_snapshots",
				"localField":   "match_id",
				"foreignField": "_id",
				"as":           "authoritative_snapshot",
			}}},
			bson.D{{Key: "$match", Value: bson.M{"authoritative_snapshot.state.lifecycle": filter.Lifecycle}}},
		)
	}
	pipeline = append(pipeline, bson.D{{Key: "$facet", Value: bson.M{
		"metadata": bson.A{bson.M{"$count": "total"}},
		"data": bson.A{
			bson.M{"$sort": bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}},
			bson.M{"$skip": params.Skip()},
			bson.M{"$limit": params.PerPage},
		},
	}}})

	cur, err := r.coll.Aggregate(ctx, pipeline)
	if err != nil {
		return paginate.Result[domain.Room]{}, err
	}
	defer cur.Close(ctx)
	var result []struct {
		Metadata []struct {
			Total int64 `bson:"total"`
		} `bson:"metadata"`
		Data []domain.Room `bson:"data"`
	}
	if err := cur.All(ctx, &result); err != nil {
		return paginate.Result[domain.Room]{}, err
	}
	if len(result) == 0 {
		return paginate.NewResult([]domain.Room{}, params, 0), nil
	}
	var total int64
	if len(result[0].Metadata) > 0 {
		total = result[0].Metadata[0].Total
	}
	return paginate.NewResult(result[0].Data, params, total), nil
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

// userTeamIDs returns the ids of the teams where the user is the leader, the
// strategist, or a player. Failures degrade to an empty list: the related-user
// filter then matches only the direct room assignments.
func (r *roomRepo) userTeamIDs(ctx context.Context, userID int64) []bson.ObjectID {
	cur, err := r.teams.Find(ctx, bson.M{"$or": bson.A{
		bson.M{"leader_id": userID},
		bson.M{"strategist_id": userID},
		bson.M{"players": userID},
	}}, options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil
	}
	defer cur.Close(ctx)
	var ids []bson.ObjectID
	for cur.Next(ctx) {
		var doc struct {
			ID bson.ObjectID `bson:"_id"`
		}
		if err := cur.Decode(&doc); err == nil {
			ids = append(ids, doc.ID)
		}
	}
	return ids
}
