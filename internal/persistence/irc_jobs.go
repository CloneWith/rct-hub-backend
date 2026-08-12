package persistence

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"rctHubBackend/internal/irc"
	"rctHubBackend/internal/matchengine"
)

const IRCJobsCollection = "irc_jobs"
const IRCJobLocksCollection = IRCJobsCollection + "_locks"
const IRCObservationsCollection = "irc_observations"

const (
	IRCReviewPending    = "PENDING"
	IRCReviewConfirming = "CONFIRMING"
	IRCReviewConfirmed  = "CONFIRMED"
	IRCReviewRejected   = "REJECTED"
)

type IRCObservationStore struct{ collection *mongo.Collection }

type IRCObservation struct {
	ID                    string               `bson:"_id"`
	Channel               string               `bson:"channel"`
	Sender                string               `bson:"sender"`
	Command               string               `bson:"command"`
	Raw                   string               `bson:"raw"`
	ObservedAt            time.Time            `bson:"observed_at"`
	ReviewStatus          string               `bson:"review_status"`
	ReviewReason          string               `bson:"review_reason,omitempty"`
	SuggestedWinningTeam  string               `bson:"suggested_winning_team,omitempty"`
	SuggestedBoardPieceID string               `bson:"suggested_board_piece_id,omitempty"`
	MatchID               *bson.ObjectID       `bson:"match_id,omitempty"`
	ConfirmationCommandID string               `bson:"confirmation_command_id,omitempty"`
	ConfirmationPieceID   string               `bson:"confirmation_board_piece_id,omitempty"`
	ConfirmationWinner    matchengine.TeamSide `bson:"confirmation_winning_team,omitempty"`
	ReviewerOsuID         int64                `bson:"reviewer_osu_id,omitempty"`
	ReviewStartedAt       *time.Time           `bson:"review_started_at,omitempty"`
}

func NewIRCObservationStore(db *mongo.Database) *IRCObservationStore {
	return &IRCObservationStore{collection: db.Collection(IRCObservationsCollection)}
}

func (s *IRCObservationStore) Save(ctx context.Context, observation irc.Observation) error {
	if observation.ID == "" || observation.Raw == "" || !irc.MatchChannel(observation.Channel) {
		return fmt.Errorf("IRC observation is incomplete")
	}
	document := bson.M{
		"_id": observation.ID, "channel": observation.Channel, "sender": observation.Sender,
		"command": observation.Command, "raw": observation.Raw, "observed_at": observation.Observed,
		"review_status": IRCReviewPending,
	}
	if observation.SuggestedResult != nil {
		document["suggested_winning_team"] = observation.SuggestedResult.WinningTeam
		document["suggested_board_piece_id"] = observation.SuggestedResult.BoardPieceID
	}
	_, err := s.collection.UpdateOne(ctx, bson.M{"_id": observation.ID}, bson.M{"$setOnInsert": document}, options.UpdateOne().SetUpsert(true))
	return err
}

func (s *IRCObservationStore) EnsureIndexes(ctx context.Context) error {
	_, err := s.collection.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "channel", Value: 1}, {Key: "observed_at", Value: -1}}, Options: options.Index().SetName("irc_observations_channel_time")})
	return err
}

func (s *IRCObservationStore) InstallValidator(ctx context.Context) error {
	if err := ensureCommandCollection(ctx, s.collection.Database(), IRCObservationsCollection); err != nil {
		return err
	}
	if err := s.collection.Database().RunCommand(ctx, bson.D{{Key: "collMod", Value: IRCObservationsCollection}, {Key: "validator", Value: IRCObservationValidator()}, {Key: "validationLevel", Value: "strict"}, {Key: "validationAction", Value: "error"}}).Err(); err != nil {
		return fmt.Errorf("install IRC observation validator: %w", err)
	}
	return nil
}

func (s *IRCObservationStore) VerifyValidator(ctx context.Context) error {
	return verifyCollectionValidator(ctx, s.collection.Database(), IRCObservationsCollection, IRCObservationValidator())
}

func (s *IRCObservationStore) List(ctx context.Context, channel string, limit int64) ([]IRCObservation, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	cur, err := s.collection.Find(ctx, bson.M{"channel": channel}, options.Find().SetSort(bson.D{{Key: "observed_at", Value: -1}}).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var result []IRCObservation
	if err := cur.All(ctx, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *IRCObservationStore) ByID(ctx context.Context, id string) (*IRCObservation, error) {
	var result IRCObservation
	if err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *IRCObservationStore) Reject(ctx context.Context, id, reason string, reviewer int64) error {
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": id, "review_status": IRCReviewPending}, bson.M{"$set": bson.M{"review_status": IRCReviewRejected, "review_reason": reason, "reviewer_osu_id": reviewer, "reviewed_at": time.Now().UTC()}})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("IRC observation is unavailable")
	}
	return nil
}

func (s *IRCObservationStore) ClaimConfirmation(ctx context.Context, id string, matchID bson.ObjectID, commandID, boardPieceID string, winningTeam matchengine.TeamSide, reviewer int64) (*IRCObservation, error) {
	if id == "" || matchID.IsZero() || commandID == "" || boardPieceID == "" || (winningTeam != matchengine.TeamRed && winningTeam != matchengine.TeamBlue) || reviewer <= 0 {
		return nil, fmt.Errorf("IRC confirmation claim is incomplete")
	}
	now := time.Now().UTC()
	filter := bson.M{"_id": id, "$or": bson.A{
		bson.M{"review_status": IRCReviewPending},
		bson.M{"review_status": IRCReviewConfirming, "match_id": matchID, "confirmation_command_id": commandID,
			"confirmation_board_piece_id": boardPieceID, "confirmation_winning_team": winningTeam},
	}}
	update := bson.M{"$set": bson.M{
		"review_status": IRCReviewConfirming, "match_id": matchID, "confirmation_command_id": commandID,
		"confirmation_board_piece_id": boardPieceID, "confirmation_winning_team": winningTeam,
		"reviewer_osu_id": reviewer, "review_started_at": now,
	}}
	var observation IRCObservation
	err := s.collection.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&observation)
	if errors.Is(err, mongo.ErrNoDocuments) {
		// A request may be retried after the authoritative command committed
		// but before the client received its response. Return the exact
		// confirmed claim without updating its audit fields, so the command
		// store can safely replay the original result.
		err = s.collection.FindOne(ctx, bson.M{"_id": id, "review_status": IRCReviewConfirmed, "match_id": matchID,
			"confirmation_command_id": commandID, "confirmation_board_piece_id": boardPieceID,
			"confirmation_winning_team": winningTeam}).Decode(&observation)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("IRC observation is unavailable")
		}
	}
	if err != nil {
		return nil, err
	}
	return &observation, nil
}

func (s *IRCObservationStore) FinalizeConfirmation(ctx context.Context, id, commandID string) error {
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": id, "review_status": IRCReviewConfirming, "confirmation_command_id": commandID}, bson.M{
		"$set":   bson.M{"review_status": IRCReviewConfirmed, "reviewed_at": time.Now().UTC()},
		"$unset": bson.M{"review_started_at": ""},
	})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("IRC observation is unavailable")
	}
	return nil
}

func (s *IRCObservationStore) ReleaseConfirmation(ctx context.Context, id, commandID string) error {
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": id, "review_status": IRCReviewConfirming, "confirmation_command_id": commandID}, bson.M{
		"$set":   bson.M{"review_status": IRCReviewPending},
		"$unset": bson.M{"match_id": "", "confirmation_command_id": "", "confirmation_board_piece_id": "", "confirmation_winning_team": "", "reviewer_osu_id": "", "review_started_at": ""},
	})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("IRC observation confirmation claim is unavailable")
	}
	return nil
}

func (s *IRCObservationStore) ListConfirming(ctx context.Context, limit int64) ([]IRCObservation, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	cursor, err := s.collection.Find(ctx, bson.M{"review_status": IRCReviewConfirming}, options.Find().SetSort(bson.D{{Key: "review_started_at", Value: 1}}).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var result []IRCObservation
	if err := cursor.All(ctx, &result); err != nil {
		return nil, err
	}
	return result, nil
}

type IRCJobStore struct {
	collection *mongo.Collection
	locks      *mongo.Collection
}

func NewIRCJobStore(db *mongo.Database) *IRCJobStore {
	return &IRCJobStore{collection: db.Collection(IRCJobsCollection), locks: db.Collection(IRCJobLocksCollection)}
}

type ircJobDocument struct {
	ID             string        `bson:"_id"`
	MatchID        bson.ObjectID `bson:"match_id"`
	Channel        string        `bson:"channel"`
	Kind           string        `bson:"kind"`
	AckTarget      string        `bson:"ack_target"`
	Sequence       uint64        `bson:"sequence,omitempty"`
	Payload        []byte        `bson:"payload"`
	Attempts       int           `bson:"attempts"`
	NextTryAt      time.Time     `bson:"next_try_at"`
	Status         irc.JobStatus `bson:"status"`
	AutomaticRetry bool          `bson:"automatic_retry"`
	LastError      string        `bson:"last_error,omitempty"`
	SentAt         *time.Time    `bson:"sent_at,omitempty"`
	AckDeadline    *time.Time    `bson:"ack_deadline,omitempty"`
	AcknowledgedAt *time.Time    `bson:"acknowledged_at,omitempty"`
	LeaseUntil     *time.Time    `bson:"lease_until,omitempty"`
	LeaseToken     string        `bson:"lease_token,omitempty"`
	CreatedAt      time.Time     `bson:"created_at"`
	UpdatedAt      time.Time     `bson:"updated_at"`
}

func (s *IRCJobStore) EnsureIndexes(ctx context.Context) error {
	_, err := s.collection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "next_try_at", Value: 1}}, Options: options.Index().SetName("irc_jobs_pending")},
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "lease_until", Value: 1}}, Options: options.Index().SetName("irc_jobs_lease")},
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "ack_deadline", Value: 1}}, Options: options.Index().SetName("irc_jobs_ack_deadline")},
		{Keys: bson.D{{Key: "match_id", Value: 1}, {Key: "created_at", Value: -1}}, Options: options.Index().SetName("irc_jobs_match_created")},
	})
	if err != nil {
		return err
	}
	_, err = s.locks.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "lease_until", Value: 1}}, Options: options.Index().SetName("irc_channel_lock_lease")})
	return err
}

func (s *IRCJobStore) InstallValidator(ctx context.Context) error {
	if err := ensureCommandCollection(ctx, s.collection.Database(), IRCJobsCollection); err != nil {
		return err
	}
	if err := s.collection.Database().RunCommand(ctx, bson.D{{Key: "collMod", Value: IRCJobsCollection}, {Key: "validator", Value: IRCJobValidator()}, {Key: "validationLevel", Value: "strict"}, {Key: "validationAction", Value: "error"}}).Err(); err != nil {
		return fmt.Errorf("install IRC job validator: %w", err)
	}
	return nil
}

func (s *IRCJobStore) VerifyValidator(ctx context.Context) error {
	return verifyCollectionValidator(ctx, s.collection.Database(), IRCJobsCollection, IRCJobValidator())
}

// Enqueue is idempotent by job ID. Callers should derive the ID from the
// committed command/event that requested the external side effect.
func (s *IRCJobStore) Enqueue(ctx context.Context, job irc.Job) error {
	matchID, err := bson.ObjectIDFromHex(job.MatchID)
	if err != nil || job.ID == "" || job.Kind == "" || len(job.Payload) == 0 || !irc.MatchChannel(job.Channel) {
		return fmt.Errorf("IRC job is incomplete")
	}
	next := job.NextTryAt.UTC()
	if next.IsZero() {
		next = time.Now().UTC()
	}
	now := time.Now().UTC()
	_, err = s.collection.UpdateOne(ctx, bson.M{"_id": job.ID}, bson.M{"$setOnInsert": bson.M{
		"_id": job.ID, "match_id": matchID, "channel": job.Channel, "kind": job.Kind, "ack_target": job.AckTarget, "sequence": job.Sequence,
		"payload": job.Payload, "attempts": 0, "next_try_at": next, "automatic_retry": true,
		"status": irc.JobPending, "created_at": now, "updated_at": now,
	}}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return err
	}
	var stored ircJobDocument
	if err := s.collection.FindOne(ctx, bson.M{"_id": job.ID}).Decode(&stored); err != nil {
		return err
	}
	if stored.MatchID != matchID || stored.Channel != job.Channel || stored.Kind != job.Kind || stored.AckTarget != job.AckTarget || stored.Sequence != job.Sequence || !bytes.Equal(stored.Payload, job.Payload) {
		return fmt.Errorf("IRC job %q was already enqueued with a different payload", job.ID)
	}
	return nil
}

func (s *IRCJobStore) Claim(ctx context.Context, now, leaseUntil time.Time) (*irc.Job, error) {
	if s == nil || s.collection == nil || s.locks == nil {
		return nil, fmt.Errorf("IRC job store is not configured")
	}
	// Select the earliest non-terminal job from each channel, then reserve one
	// due channel atomically. A parked or in-flight job blocks only its own
	// room; unrelated matches continue to make progress.
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"status": bson.M{"$in": bson.A{irc.JobPending, irc.JobFailed, irc.JobSending, irc.JobSent}}}}},
		{{Key: "$sort", Value: bson.D{{Key: "channel", Value: 1}, {Key: "sequence", Value: 1}, {Key: "created_at", Value: 1}}}},
		{{Key: "$group", Value: bson.M{"_id": "$channel", "job": bson.M{"$first": "$$ROOT"}}}},
		{{Key: "$replaceRoot", Value: bson.M{"newRoot": "$job"}}},
		{{Key: "$match", Value: bson.M{"$or": bson.A{
			bson.M{"status": irc.JobPending, "next_try_at": bson.M{"$lte": now.UTC()}},
			bson.M{"status": irc.JobFailed, "automatic_retry": true, "next_try_at": bson.M{"$lte": now.UTC()}},
			bson.M{"status": irc.JobSending, "lease_until": bson.M{"$lte": now.UTC()}},
			bson.M{"status": irc.JobSent, "ack_deadline": bson.M{"$lte": now.UTC()}},
		}}}},
		{{Key: "$sort", Value: bson.D{{Key: "next_try_at", Value: 1}, {Key: "created_at", Value: 1}}}},
		{{Key: "$limit", Value: int64(64)}},
	}
	cursor, err := s.collection.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var candidates []ircJobDocument
	if err := cursor.All(ctx, &candidates); err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		lockToken := uuid.NewString()
		lockFilter := bson.M{"_id": candidate.Channel, "$or": bson.A{
			bson.M{"lease_until": bson.M{"$lte": now.UTC()}}, bson.M{"lease_token": lockToken}, bson.M{"lease_token": bson.M{"$exists": false}},
		}}
		lockUpdate := bson.M{"$set": bson.M{"lease_token": lockToken, "lease_until": leaseUntil.UTC(), "updated_at": now.UTC()}}
		var lock struct {
			LeaseToken string `bson:"lease_token"`
		}
		if err := s.locks.FindOneAndUpdate(ctx, lockFilter, lockUpdate, options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&lock); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				continue
			}
			return nil, err
		}
		var document ircJobDocument
		earliestFilter := bson.M{"channel": candidate.Channel, "status": bson.M{"$in": bson.A{irc.JobPending, irc.JobFailed, irc.JobSending, irc.JobSent}}}
		if err := s.collection.FindOne(ctx, earliestFilter, options.FindOne().SetSort(bson.D{{Key: "sequence", Value: 1}, {Key: "created_at", Value: 1}})).Decode(&document); err != nil || document.ID != candidate.ID {
			_ = s.releaseLock(ctx, candidate.Channel, lockToken)
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, nil
			}
			continue
		}
		leaseToken := lockToken
		updated, err := s.collection.UpdateOne(ctx, bson.M{"_id": document.ID, "status": document.Status}, bson.M{"$set": bson.M{"status": irc.JobSending, "lease_until": leaseUntil.UTC(), "lease_token": leaseToken, "updated_at": now.UTC()}})
		if err != nil {
			_ = s.releaseLock(ctx, candidate.Channel, lockToken)
			return nil, err
		}
		if updated.MatchedCount == 0 {
			_ = s.releaseLock(ctx, candidate.Channel, lockToken)
			continue
		}
		job := jobFromDocument(document)
		job.LeaseToken = leaseToken
		return job, nil
	}
	return nil, nil
}

func (s *IRCJobStore) releaseLock(ctx context.Context, channel, token string) error {
	if channel == "" || token == "" {
		return nil
	}
	_, err := s.locks.DeleteOne(ctx, bson.M{"_id": channel, "lease_token": token})
	return err
}

func (s *IRCJobStore) MarkSent(ctx context.Context, id, leaseToken string, sentAt, deadline time.Time) error {
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": id, "status": irc.JobSending, "lease_token": leaseToken}, bson.M{
		"$set":   bson.M{"status": irc.JobSent, "sent_at": sentAt.UTC(), "ack_deadline": deadline.UTC(), "updated_at": sentAt.UTC()},
		"$unset": bson.M{"lease_until": "", "last_error": ""},
	})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("claimed IRC job %q is unavailable", id)
	}
	return nil
}

func (s *IRCJobStore) Ack(ctx context.Context, id, leaseToken string, at time.Time) error {
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": id, "status": bson.M{"$in": bson.A{irc.JobSending, irc.JobSent}}, "lease_token": leaseToken}, bson.M{
		"$set":   bson.M{"status": irc.JobAcknowledged, "acknowledged_at": at.UTC(), "updated_at": at.UTC()},
		"$unset": bson.M{"lease_until": "", "lease_token": "", "ack_deadline": "", "last_error": ""},
	})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("sent IRC job %q is unavailable", id)
	}
	var job ircJobDocument
	if err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&job); err == nil {
		_ = s.releaseLock(ctx, job.Channel, leaseToken)
	}
	return nil
}

func (s *IRCJobStore) Fail(ctx context.Context, id, leaseToken, message string, retryAt time.Time) error {
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": id, "status": bson.M{"$in": bson.A{irc.JobSending, irc.JobSent}}, "lease_token": leaseToken}, bson.M{
		"$set":   bson.M{"status": irc.JobFailed, "automatic_retry": true, "last_error": message, "next_try_at": retryAt.UTC(), "updated_at": time.Now().UTC()},
		"$unset": bson.M{"lease_until": "", "lease_token": "", "ack_deadline": ""}, "$inc": bson.M{"attempts": 1},
	})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("IRC job %q lease is no longer owned", id)
	}
	var job ircJobDocument
	if err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&job); err == nil {
		_ = s.releaseLock(ctx, job.Channel, leaseToken)
	}
	return nil
}

// Reject parks an explicit Bancho rejection until a referee retries it.
func (s *IRCJobStore) Reject(ctx context.Context, id, leaseToken, message string) error {
	if id == "" || leaseToken == "" || message == "" {
		return fmt.Errorf("IRC job rejection is incomplete")
	}
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": id, "status": bson.M{"$in": bson.A{irc.JobSending, irc.JobSent}}, "lease_token": leaseToken}, bson.M{
		"$set":   bson.M{"status": irc.JobFailed, "automatic_retry": false, "last_error": message, "updated_at": time.Now().UTC()},
		"$unset": bson.M{"lease_until": "", "lease_token": "", "ack_deadline": ""}, "$inc": bson.M{"attempts": 1},
	})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("IRC job %q lease is no longer owned", id)
	}
	var job ircJobDocument
	if err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&job); err == nil {
		_ = s.releaseLock(ctx, job.Channel, leaseToken)
	}
	return nil
}

func (s *IRCJobStore) Cancel(ctx context.Context, id, leaseToken, message string) error {
	if id == "" || leaseToken == "" || message == "" {
		return fmt.Errorf("IRC job cancellation is incomplete")
	}
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": id, "status": irc.JobSending, "lease_token": leaseToken}, bson.M{
		"$set":   bson.M{"status": irc.JobCancelled, "last_error": message, "updated_at": time.Now().UTC()},
		"$unset": bson.M{"lease_until": "", "lease_token": "", "ack_deadline": ""},
	})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("IRC job %q lease is no longer owned", id)
	}
	var job ircJobDocument
	if err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&job); err == nil {
		_ = s.releaseLock(ctx, job.Channel, leaseToken)
	}
	return nil
}

func (s *IRCJobStore) List(ctx context.Context, matchID bson.ObjectID, limit int64) ([]irc.Job, error) {
	if matchID.IsZero() {
		return nil, fmt.Errorf("match ID is required")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	cursor, err := s.collection.Find(ctx, bson.M{"match_id": matchID}, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var documents []ircJobDocument
	if err := cursor.All(ctx, &documents); err != nil {
		return nil, err
	}
	jobs := make([]irc.Job, len(documents))
	for index, document := range documents {
		jobs[index] = *jobFromDocument(document)
	}
	return jobs, nil
}

func (s *IRCJobStore) Retry(ctx context.Context, matchID bson.ObjectID, id, channel string, at time.Time) error {
	if matchID.IsZero() || id == "" || !irc.MatchChannel(channel) {
		return fmt.Errorf("IRC job retry is incomplete")
	}
	result, err := s.collection.UpdateOne(ctx, bson.M{
		"_id": id, "match_id": matchID, "channel": channel,
		"status": irc.JobFailed,
	}, bson.M{
		"$set":   bson.M{"status": irc.JobPending, "automatic_retry": true, "next_try_at": at.UTC(), "updated_at": at.UTC()},
		"$unset": bson.M{"sent_at": "", "ack_deadline": "", "lease_until": "", "lease_token": "", "last_error": ""},
	})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("IRC job is not retryable")
	}
	return nil
}

func jobFromDocument(document ircJobDocument) *irc.Job {
	job := &irc.Job{
		ID: document.ID, MatchID: document.MatchID.Hex(), Channel: document.Channel, Kind: document.Kind, AckTarget: document.AckTarget,
		Payload: append([]byte(nil), document.Payload...), Status: document.Status, Attempts: document.Attempts,
		NextTryAt: document.NextTryAt, LastError: document.LastError,
		LeaseToken: document.LeaseToken, AutomaticRetry: document.AutomaticRetry,
	}
	if document.SentAt != nil {
		job.SentAt = *document.SentAt
	}
	if document.AckDeadline != nil {
		job.AckDeadline = *document.AckDeadline
	}
	if document.AcknowledgedAt != nil {
		job.AcknowledgedAt = *document.AcknowledgedAt
	}
	return job
}
