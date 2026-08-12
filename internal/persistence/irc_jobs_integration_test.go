package persistence_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/beatmapmetadata"
	"rctHubBackend/internal/irc"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/internal/persistence"
)

func TestMongoIntegrationIRCJobsAndObservationsSurviveReplay(t *testing.T) {
	_, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	jobs := persistence.NewIRCJobStore(db)
	observations := persistence.NewIRCObservationStore(db)
	if err := jobs.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := observations.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := jobs.InstallValidator(ctx); err != nil {
		t.Fatal(err)
	}
	if err := observations.InstallValidator(ctx); err != nil {
		t.Fatal(err)
	}

	matchID := bson.NewObjectID()
	job := irc.Job{ID: "event-1-map", MatchID: matchID.Hex(), Channel: "#mp_42", Kind: "MAP", Payload: []byte("PRIVMSG #mp_42 :!mp map 123")}
	if err := jobs.Enqueue(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := jobs.Enqueue(ctx, job); err != nil {
		t.Fatalf("idempotent enqueue: %v", err)
	}
	mismatch := job
	mismatch.Payload = []byte("PRIVMSG #mp_42 :!mp map 999")
	if err := jobs.Enqueue(ctx, mismatch); err == nil {
		t.Fatal("same job ID accepted a different payload")
	}
	sequenceMismatch := job
	sequenceMismatch.Sequence = 9
	if err := jobs.Enqueue(ctx, sequenceMismatch); err == nil {
		t.Fatal("same job ID accepted a different event sequence")
	}
	claimed, err := jobs.Claim(ctx, time.Now().Add(time.Second), time.Now().Add(time.Minute))
	if err != nil || claimed == nil || claimed.ID != job.ID {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	sentAt := time.Now().UTC()
	if err := jobs.MarkSent(ctx, job.ID, claimed.LeaseToken, sentAt, sentAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := jobs.Retry(ctx, matchID, job.ID, job.Channel, sentAt); err == nil {
		t.Fatal("job still awaiting a Bancho acknowledgement was retryable")
	}
	if err := jobs.Retry(ctx, matchID, job.ID, "#mp_43", sentAt); err == nil {
		t.Fatal("job from a previous room channel was retryable")
	}
	if err := jobs.Ack(ctx, job.ID, claimed.LeaseToken, sentAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	listed, err := persistence.NewIRCJobStore(db).List(ctx, matchID, 10)
	if err != nil || len(listed) != 1 || listed[0].Status != irc.JobAcknowledged || listed[0].MatchID != matchID.Hex() {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}

	raw := ":ref!u PRIVMSG #mp_42 :!result RED piece-1"
	observation := irc.Observation{
		ID: "observation-1", Channel: "#mp_42", Sender: "ref!u", Command: ":!result RED piece-1",
		Raw: raw, Observed: sentAt, SuggestedResult: &irc.ResultSuggestion{WinningTeam: "RED", BoardPieceID: "piece-1"},
	}
	if err := observations.Save(ctx, observation); err != nil {
		t.Fatal(err)
	}
	if err := observations.Save(ctx, observation); err != nil {
		t.Fatalf("duplicate observation save: %v", err)
	}
	items, err := persistence.NewIRCObservationStore(db).List(ctx, "#mp_42", 10)
	if err != nil || len(items) != 1 || items[0].ReviewStatus != persistence.IRCReviewPending || items[0].SuggestedBoardPieceID != "piece-1" {
		t.Fatalf("observations=%+v err=%v", items, err)
	}
	if err := observations.Reject(ctx, observation.ID, "wrong result", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := observations.ClaimConfirmation(ctx, observation.ID, matchID, "confirm-rejected", "piece-1", matchengine.TeamRed, 100); err == nil {
		t.Fatal("rejected observation was later confirmed")
	}

	if _, err := db.Collection(persistence.IRCJobsCollection).InsertOne(ctx, bson.M{"_id": "invalid"}); err == nil {
		t.Fatal("strict IRC job validator accepted an incomplete document")
	}
}

func TestMongoIntegrationIRCReviewClaimRacesRejection(t *testing.T) {
	_, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store := persistence.NewIRCObservationStore(db)
	if err := store.InstallValidator(ctx); err != nil {
		t.Fatal(err)
	}
	observation := irc.Observation{ID: "review-race", Channel: "#mp_42", Sender: "ref", Command: ":!result RED piece-1", Raw: ":ref PRIVMSG #mp_42 :!result RED piece-1", Observed: time.Now().UTC()}
	if err := store.Save(ctx, observation); err != nil {
		t.Fatal(err)
	}
	matchID := bson.NewObjectID()
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		_, err := store.ClaimConfirmation(ctx, observation.ID, matchID, "00000000-0000-0000-0000-000000000001", "piece-1", matchengine.TeamRed, 100)
		results <- err
	}()
	go func() {
		defer wait.Done()
		<-start
		results <- store.Reject(ctx, observation.ID, "incorrect", 100)
	}()
	close(start)
	wait.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful competing reviews=%d, want 1", successes)
	}
}

func TestMongoIntegrationConfirmedIRCReviewIsIdempotentlyReclaimable(t *testing.T) {
	_, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store := persistence.NewIRCObservationStore(db)
	if err := store.InstallValidator(ctx); err != nil {
		t.Fatal(err)
	}
	observation := irc.Observation{ID: "confirmed-retry", Channel: "#mp_42", Sender: "ref", Command: ":!result RED piece-1", Raw: ":ref PRIVMSG #mp_42 :!result RED piece-1", Observed: time.Now().UTC()}
	if err := store.Save(ctx, observation); err != nil {
		t.Fatal(err)
	}
	matchID := bson.NewObjectID()
	const commandID = "00000000-0000-0000-0000-000000000002"
	claimed, err := store.ClaimConfirmation(ctx, observation.ID, matchID, commandID, "piece-1", matchengine.TeamRed, 100)
	if err != nil || claimed == nil {
		t.Fatalf("initial claim=%+v err=%v", claimed, err)
	}
	if err := store.FinalizeConfirmation(ctx, observation.ID, commandID); err != nil {
		t.Fatal(err)
	}
	retry, err := store.ClaimConfirmation(ctx, observation.ID, matchID, commandID, "piece-1", matchengine.TeamRed, 100)
	if err != nil || retry == nil || retry.ReviewStatus != persistence.IRCReviewConfirmed {
		t.Fatalf("idempotent claim=%+v err=%v", retry, err)
	}
	if _, err := store.ClaimConfirmation(ctx, observation.ID, matchID, commandID, "piece-2", matchengine.TeamRed, 100); err == nil {
		t.Fatal("same command accepted a different board piece")
	}
}

func TestMongoIntegrationExpiredIRCLeaseCannotOverwriteNewClaim(t *testing.T) {
	_, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store := persistence.NewIRCJobStore(db)
	if err := store.InstallValidator(ctx); err != nil {
		t.Fatal(err)
	}
	matchID := bson.NewObjectID()
	job := irc.Job{ID: "lease-race", MatchID: matchID.Hex(), Channel: "#mp_42", Kind: "MAP", Payload: []byte("PRIVMSG #mp_42 :!mp map 123")}
	if err := store.Enqueue(ctx, job); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	first, err := store.Claim(ctx, now, now.Add(time.Second))
	if err != nil || first == nil || first.LeaseToken == "" {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	second, err := store.Claim(ctx, now.Add(2*time.Second), now.Add(3*time.Second))
	if err != nil || second == nil || second.LeaseToken == "" || second.LeaseToken == first.LeaseToken {
		t.Fatalf("second claim=%+v err=%v", second, err)
	}
	if err := store.MarkSent(ctx, job.ID, first.LeaseToken, now.Add(2*time.Second), now.Add(3*time.Second)); err == nil {
		t.Fatal("expired IRC worker overwrote a newer claim")
	}
	if err := store.Fail(ctx, job.ID, first.LeaseToken, "late failure", now.Add(time.Minute)); err == nil {
		t.Fatal("expired IRC worker replaced a newer claim with failure")
	}
	if err := store.MarkSent(ctx, job.ID, second.LeaseToken, now.Add(2*time.Second), now.Add(3*time.Second)); err != nil {
		t.Fatalf("current IRC worker could not persist: %v", err)
	}
}

func TestMongoIntegrationIRCJobsStayOrderedPerChannel(t *testing.T) {
	_, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store := persistence.NewIRCJobStore(db)
	if err := store.InstallValidator(ctx); err != nil {
		t.Fatal(err)
	}
	matchID := bson.NewObjectID()
	for _, job := range []irc.Job{
		{ID: "second", MatchID: matchID.Hex(), Channel: "#mp_42", Sequence: 2, Kind: "MAP", Payload: []byte("PRIVMSG #mp_42 :!mp map 2")},
		{ID: "first", MatchID: matchID.Hex(), Channel: "#mp_42", Sequence: 1, Kind: "INVITE", Payload: []byte("PRIVMSG #mp_42 :!mp invite #1")},
		{ID: "other-room", MatchID: matchID.Hex(), Channel: "#mp_43", Sequence: 3, Kind: "MAP", Payload: []byte("PRIVMSG #mp_43 :!mp map 3")},
	} {
		if err := store.Enqueue(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Add(time.Second)
	first, err := store.Claim(ctx, now, now.Add(time.Minute))
	if err != nil || first == nil || first.ID != "first" {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	other, err := store.Claim(ctx, now, now.Add(time.Minute))
	if err != nil || other == nil || other.ID != "other-room" {
		t.Fatalf("parallel room claim=%+v err=%v", other, err)
	}
	if err := store.MarkSent(ctx, first.ID, first.LeaseToken, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.Ack(ctx, first.ID, first.LeaseToken, now); err != nil {
		t.Fatal(err)
	}
	second, err := store.Claim(ctx, now, now.Add(time.Minute))
	if err != nil || second == nil || second.ID != "second" {
		t.Fatalf("second claim=%+v err=%v", second, err)
	}
}

func TestMongoIntegrationParkedIRCChannelsDoNotStarveRunnableJobs(t *testing.T) {
	_, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store := persistence.NewIRCJobStore(db)
	if err := store.InstallValidator(ctx); err != nil {
		t.Fatal(err)
	}
	matchID := bson.NewObjectID()
	now := time.Now().UTC()
	for i := range 65 {
		channel := fmt.Sprintf("#mp_%d", i+1)
		job := irc.Job{
			ID:       fmt.Sprintf("parked-%02d", i),
			MatchID:  matchID.Hex(),
			Channel:  channel,
			Sequence: 1,
			Kind:     "MAP",
			Payload:  []byte(fmt.Sprintf("PRIVMSG %s :!mp map 123", channel)),
		}
		if err := store.Enqueue(ctx, job); err != nil {
			t.Fatal(err)
		}
		claimed, err := store.Claim(ctx, now.Add(time.Second), now.Add(time.Minute))
		if err != nil || claimed == nil || claimed.ID != job.ID {
			t.Fatalf("parked claim=%+v err=%v", claimed, err)
		}
		if err := store.Reject(ctx, job.ID, claimed.LeaseToken, "manual review required"); err != nil {
			t.Fatal(err)
		}
	}
	runnable := irc.Job{
		ID: "runnable-after-parked", MatchID: matchID.Hex(), Channel: "#mp_99", Sequence: 1,
		Kind: "MAP", Payload: []byte("PRIVMSG #mp_99 :!mp map 999"),
	}
	if err := store.Enqueue(ctx, runnable); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(ctx, now.Add(2*time.Second), now.Add(time.Minute))
	if err != nil || claimed == nil || claimed.ID != runnable.ID {
		t.Fatalf("runnable job behind parked channels was starved: claim=%+v err=%v", claimed, err)
	}
}

func TestMongoIntegrationIRCChannelOrderingSpansMatchReuse(t *testing.T) {
	_, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store := persistence.NewIRCJobStore(db)
	if err := store.InstallValidator(ctx); err != nil {
		t.Fatal(err)
	}
	oldMatchID, newMatchID := bson.NewObjectID(), bson.NewObjectID()
	for _, job := range []irc.Job{
		{ID: "old-room-job", MatchID: oldMatchID.Hex(), Channel: "#mp_42", Sequence: 1, Kind: "MAP", Payload: []byte("PRIVMSG #mp_42 :!mp map 1")},
		{ID: "new-room-job", MatchID: newMatchID.Hex(), Channel: "#mp_42", Sequence: 1, Kind: "MAP", Payload: []byte("PRIVMSG #mp_42 :!mp map 2")},
	} {
		if err := store.Enqueue(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Add(time.Second)
	claimed, err := store.Claim(ctx, now, now.Add(time.Minute))
	if err != nil || claimed == nil || claimed.ID != "old-room-job" {
		t.Fatalf("channel reuse bypassed the older job: claim=%+v err=%v", claimed, err)
	}
}

func TestMongoIntegrationConcurrentIRCWorkersClaimOneJobPerChannel(t *testing.T) {
	_, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	firstStore := persistence.NewIRCJobStore(db)
	secondStore := persistence.NewIRCJobStore(db)
	if err := firstStore.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := firstStore.InstallValidator(ctx); err != nil {
		t.Fatal(err)
	}
	matchID := bson.NewObjectID()
	for _, job := range []irc.Job{
		{ID: "concurrent-first", MatchID: matchID.Hex(), Channel: "#mp_42", Sequence: 1, Kind: "INVITE", Payload: []byte("PRIVMSG #mp_42 :!mp invite player")},
		{ID: "concurrent-second", MatchID: matchID.Hex(), Channel: "#mp_42", Sequence: 2, Kind: "MAP", Payload: []byte("PRIVMSG #mp_42 :!mp map 2")},
	} {
		if err := firstStore.Enqueue(ctx, job); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().UTC().Add(time.Second)
	start := make(chan struct{})
	results := make(chan *irc.Job, 2)
	errors := make(chan error, 2)
	for _, store := range []*persistence.IRCJobStore{firstStore, secondStore} {
		go func(store *persistence.IRCJobStore) {
			<-start
			job, err := store.Claim(ctx, now, now.Add(time.Minute))
			results <- job
			errors <- err
		}(store)
	}
	close(start)

	claimed := 0
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
		if job := <-results; job != nil {
			claimed++
			if job.ID != "concurrent-first" {
				t.Fatalf("claimed %q before the first job", job.ID)
			}
		}
	}
	if claimed != 1 {
		t.Fatalf("concurrent claims=%d, want exactly one", claimed)
	}
}

func TestMongoIntegrationBanchoRejectionRequiresManualRetry(t *testing.T) {
	_, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store := persistence.NewIRCJobStore(db)
	if err := store.InstallValidator(ctx); err != nil {
		t.Fatal(err)
	}
	matchID := bson.NewObjectID()
	job := irc.Job{ID: "rejected", MatchID: matchID.Hex(), Channel: "#mp_42", Sequence: 1, Kind: "MAP", Payload: []byte("PRIVMSG #mp_42 :!mp map 123")}
	if err := store.Enqueue(ctx, job); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	claimed, err := store.Claim(ctx, now, now.Add(time.Minute))
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if err := store.MarkSent(ctx, job.ID, claimed.LeaseToken, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.Reject(ctx, job.ID, claimed.LeaseToken, "permission denied"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Claim(ctx, now.Add(24*time.Hour), now.Add(25*time.Hour)); err != nil || got != nil {
		t.Fatalf("rejected job auto-retried: job=%+v err=%v", got, err)
	}
	if err := store.Retry(ctx, matchID, job.ID, job.Channel, now); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Claim(ctx, now.Add(time.Second), now.Add(time.Minute)); err != nil || got == nil || got.ID != job.ID {
		t.Fatalf("manual retry claim=%+v err=%v", got, err)
	}
}

func TestMongoIntegrationBeatmapMetadataSurvivesWorkerRestart(t *testing.T) {
	_, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store := persistence.NewBeatmapMetadataStore(db)
	if err := store.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.InstallValidator(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.Ensure(ctx, 123, now); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(ctx, now, now.Add(-time.Second))
	if err != nil || claimed == nil || claimed.BeatmapID != 123 {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	restarted := persistence.NewBeatmapMetadataStore(db)
	reclaimed, err := restarted.Claim(ctx, now, now.Add(time.Minute))
	if err != nil || reclaimed == nil || reclaimed.BeatmapID != 123 {
		t.Fatalf("restart claim=%+v err=%v", reclaimed, err)
	}
	if err := restarted.Fail(ctx, 123, reclaimed.LeaseToken, "osu unavailable", now.Add(time.Minute), now); err != nil {
		t.Fatal(err)
	}
	record, err := restarted.Get(ctx, 123)
	if err != nil || record.Status != beatmapmetadata.StatusFailed || record.Attempts != 1 {
		t.Fatalf("record=%+v err=%v", record, err)
	}
}

func TestMongoIntegrationExpiredBeatmapLeaseCannotOverwriteNewClaim(t *testing.T) {
	_, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store := persistence.NewBeatmapMetadataStore(db)
	if err := store.InstallValidator(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.Ensure(ctx, 456, now); err != nil {
		t.Fatal(err)
	}
	first, err := store.Claim(ctx, now, now.Add(time.Second))
	if err != nil || first == nil || first.LeaseToken == "" {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	second, err := store.Claim(ctx, now.Add(2*time.Second), now.Add(3*time.Second))
	if err != nil || second == nil || second.LeaseToken == "" || second.LeaseToken == first.LeaseToken {
		t.Fatalf("second claim=%+v err=%v", second, err)
	}
	if err := store.MarkReady(ctx, 456, first.LeaseToken, now.Add(2*time.Second)); !errors.Is(err, beatmapmetadata.ErrLeaseLost) {
		t.Fatalf("expired metadata worker error=%v, want ErrLeaseLost", err)
	}
	if err := store.MarkReady(ctx, 456, second.LeaseToken, now.Add(2*time.Second)); err != nil {
		t.Fatalf("current metadata worker could not persist: %v", err)
	}
}
