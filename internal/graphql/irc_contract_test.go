package graphql

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/irc"
	"rctHubBackend/internal/matchcommand"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/internal/persistence"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/jwtutil"
	"rctHubBackend/pkg/paginate"
)

type ircFormalReader struct{ match *service.FormalMatch }

func (r ircFormalReader) ByID(context.Context, bson.ObjectID) (*service.FormalMatch, error) {
	return r.match, nil
}
func (r ircFormalReader) ByCode(context.Context, string) (*service.FormalMatch, error) {
	return r.match, nil
}
func (r ircFormalReader) List(context.Context, paginate.Params) (paginate.Result[service.FormalMatch], error) {
	return paginate.Result[service.FormalMatch]{}, nil
}

type ircUserReader struct{ user *domain.User }

func (r ircUserReader) GetByOsuID(context.Context, int64) (*domain.User, error) { return r.user, nil }

type ircRoomReader struct{ room *domain.Room }

func (r ircRoomReader) GetRoom(context.Context, bson.ObjectID) (*domain.Room, error) {
	return r.room, nil
}

type ircJobReader struct {
	jobs           []irc.Job
	retriedID      string
	retriedChannel string
}

type automationIssueReader struct {
	events       []persistence.MatchOutboxDocument
	retriedEvent string
}

func (r *automationIssueReader) ListFailedEvents(context.Context, bson.ObjectID, int64) ([]persistence.MatchOutboxDocument, error) {
	return append([]persistence.MatchOutboxDocument(nil), r.events...), nil
}

func (r *automationIssueReader) RetryFailedEvent(_ context.Context, _ bson.ObjectID, eventID string) error {
	r.retriedEvent = eventID
	return nil
}

func (r *ircJobReader) List(context.Context, bson.ObjectID, int64) ([]irc.Job, error) {
	return append([]irc.Job(nil), r.jobs...), nil
}
func (r *ircJobReader) Retry(_ context.Context, _ bson.ObjectID, id, channel string, _ time.Time) error {
	for _, job := range r.jobs {
		if job.ID == id && job.Channel == channel {
			r.retriedID, r.retriedChannel = id, channel
			return nil
		}
	}
	return fmt.Errorf("IRC job is not retryable in the current match channel")
}

type ircStatusReader struct{ status irc.ConnectionStatus }

func (r ircStatusReader) Status() irc.ConnectionStatus { return r.status }

type ircObservationReader struct {
	observation persistence.IRCObservation
	claimed     bool
	finalized   bool
	released    bool
}

func (r *ircObservationReader) List(context.Context, string, int64) ([]persistence.IRCObservation, error) {
	return []persistence.IRCObservation{r.observation}, nil
}
func (r *ircObservationReader) ByID(context.Context, string) (*persistence.IRCObservation, error) {
	copy := r.observation
	return &copy, nil
}
func (r *ircObservationReader) Reject(context.Context, string, string, int64) error { return nil }
func (r *ircObservationReader) ClaimConfirmation(_ context.Context, _ string, matchID bson.ObjectID, commandID, boardPieceID string, winner matchengine.TeamSide, _ int64) (*persistence.IRCObservation, error) {
	r.claimed = true
	copy := r.observation
	copy.MatchID, copy.ConfirmationCommandID, copy.ReviewStatus = &matchID, commandID, persistence.IRCReviewConfirming
	copy.ConfirmationPieceID, copy.ConfirmationWinner = boardPieceID, winner
	return &copy, nil
}
func (r *ircObservationReader) FinalizeConfirmation(context.Context, string, string) error {
	r.finalized = true
	return nil
}
func (r *ircObservationReader) ReleaseConfirmation(context.Context, string, string) error {
	r.released = true
	return nil
}

func TestIRCRoomAssociationIsExactAndRefereeOnly(t *testing.T) {
	matchID, roomID := bson.NewObjectID(), bson.NewObjectID()
	mpLink := "https://osu.ppy.sh/community/matches/42"
	user := &domain.User{OnlineID: 100, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}}
	refereeID := int64(100)
	room := &domain.Room{ID: roomID, Type: domain.RoomTypeMatch, OwnerID: 100, RefereeUserID: &refereeID, MatchID: &matchID, Settings: domain.RoomSettings{MPLink: &mpLink}}
	resolver := NewResolver(nil).WithFormalMatchReader(ircFormalReader{&service.FormalMatch{ID: matchID, RoomID: roomID}}).WithPrivateReaders(ircUserReader{user}, ircRoomReader{room})
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 100})
	if err := resolver.authorizeIRCObservation(ctx, matchID.Hex(), "#mp_42"); err != nil {
		t.Fatalf("linked channel was rejected: %v", err)
	}
	for _, channel := range []string{"42", "#mp_0042", "#mp_43", "#other"} {
		if err := resolver.authorizeIRCObservation(ctx, matchID.Hex(), channel); err == nil {
			t.Fatalf("channel %q was accepted", channel)
		}
	}
	user.OnlineID = 101
	if err := resolver.authorizeIRCObservation(ctx, matchID.Hex(), "#mp_42"); err == nil {
		t.Fatal("unassigned referee was accepted")
	}
}

func TestIRCStatusIsVisibleOnlyToAssignedReferee(t *testing.T) {
	matchID, roomID := bson.NewObjectID(), bson.NewObjectID()
	mpLink := "https://osu.ppy.sh/community/matches/42"
	user := &domain.User{OnlineID: 100, VerifyStatus: domain.Pending, Roles: []domain.UserRole{domain.RoleReferee}}
	refereeID := int64(100)
	room := &domain.Room{ID: roomID, Type: domain.RoomTypeMatch, OwnerID: 100, RefereeUserID: &refereeID, MatchID: &matchID, Settings: domain.RoomSettings{MPLink: &mpLink}}
	resolver := NewResolver(nil).
		WithFormalMatchReader(ircFormalReader{&service.FormalMatch{ID: matchID, RoomID: roomID}}).
		WithPrivateReaders(ircUserReader{user}, ircRoomReader{room}).
		WithIRCStatus(ircStatusReader{irc.ConnectionStatus{Configured: true, Connected: true}})
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 100})
	if _, err := resolver.Query().IrcConnectionStatus(ctx, matchID.Hex()); err == nil {
		t.Fatal("unverified user received private IRC status")
	}
	user.VerifyStatus, user.IsBanned = domain.Verified, true
	if _, err := resolver.Query().IrcConnectionStatus(ctx, matchID.Hex()); err == nil {
		t.Fatal("banned user received private IRC status")
	}
	user.IsBanned, user.OnlineID = false, 101
	if _, err := resolver.Query().IrcConnectionStatus(ctx, matchID.Hex()); err == nil {
		t.Fatal("unassigned referee received private IRC status")
	}
	user.OnlineID = 100
	status, err := resolver.Query().IrcConnectionStatus(ctx, matchID.Hex())
	if err != nil || !status.Configured || !status.Connected {
		t.Fatalf("assigned referee status=%+v err=%v", status, err)
	}
}

func TestIRCJobsAreVisibleAndRetryableForAssignedReferee(t *testing.T) {
	matchID, roomID := bson.NewObjectID(), bson.NewObjectID()
	mpLink := "https://osu.ppy.sh/community/matches/42"
	user := &domain.User{OnlineID: 100, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}}
	refereeID := int64(100)
	room := &domain.Room{ID: roomID, Type: domain.RoomTypeMatch, OwnerID: 100, RefereeUserID: &refereeID, MatchID: &matchID, Settings: domain.RoomSettings{MPLink: &mpLink}}
	jobs := &ircJobReader{jobs: []irc.Job{{ID: "job-1", MatchID: matchID.Hex(), Channel: "#mp_42", Kind: "MAP", Payload: []byte("command"), Status: irc.JobFailed, AutomaticRetry: false, NextTryAt: time.Now(), LastError: "permission denied"}}}
	resolver := NewResolver(nil).WithFormalMatchReader(ircFormalReader{&service.FormalMatch{ID: matchID, RoomID: roomID}}).WithPrivateReaders(ircUserReader{user}, ircRoomReader{room}).WithIRCJobs(jobs)
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 100})
	listed, err := resolver.Query().IrcJobs(ctx, matchID.Hex())
	if err != nil || len(listed) != 1 || listed[0].Status != IRCJobStatusFailed || listed[0].AutomaticRetry || listed[0].NextTryAt != nil || listed[0].LastError == nil {
		t.Fatalf("jobs=%+v err=%v", listed, err)
	}
	ok, err := resolver.Mutation().RetryIRCJob(ctx, RetryIRCJobInput{MatchID: matchID.Hex(), JobID: "job-1"})
	if err != nil || !ok || jobs.retriedID != "job-1" || jobs.retriedChannel != "#mp_42" {
		t.Fatalf("retry ok=%v id=%q channel=%q err=%v", ok, jobs.retriedID, jobs.retriedChannel, err)
	}
}

func TestIRCJobFromPreviousRoomChannelCannotBeRetried(t *testing.T) {
	matchID, roomID := bson.NewObjectID(), bson.NewObjectID()
	mpLink := "https://osu.ppy.sh/community/matches/43"
	user := &domain.User{OnlineID: 100, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}}
	refereeID := int64(100)
	room := &domain.Room{ID: roomID, Type: domain.RoomTypeMatch, OwnerID: 100, RefereeUserID: &refereeID, MatchID: &matchID, Settings: domain.RoomSettings{MPLink: &mpLink}}
	jobs := &ircJobReader{jobs: []irc.Job{{ID: "old-job", MatchID: matchID.Hex(), Channel: "#mp_42", Status: irc.JobFailed}}}
	resolver := NewResolver(nil).
		WithFormalMatchReader(ircFormalReader{&service.FormalMatch{ID: matchID, RoomID: roomID}}).
		WithPrivateReaders(ircUserReader{user}, ircRoomReader{room}).
		WithIRCJobs(jobs)
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 100})
	ok, err := resolver.Mutation().RetryIRCJob(ctx, RetryIRCJobInput{MatchID: matchID.Hex(), JobID: "old-job"})
	if err == nil || ok || jobs.retriedID != "" {
		t.Fatalf("stale job retry ok=%v retried=%q err=%v", ok, jobs.retriedID, err)
	}
}

func TestAutomationPlanningFailuresAreVisibleAndRetryableForAssignedReferee(t *testing.T) {
	matchID, roomID := bson.NewObjectID(), bson.NewObjectID()
	user := &domain.User{OnlineID: 100, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}}
	refereeID := int64(100)
	room := &domain.Room{ID: roomID, Type: domain.RoomTypeMatch, OwnerID: 100, RefereeUserID: &refereeID, MatchID: &matchID}
	automation := &automationIssueReader{events: []persistence.MatchOutboxDocument{{
		EventID: "event-1", MatchID: matchID, Sequence: 4, Type: matchengine.EventPiecePlaced,
		Status: persistence.OutboxFailed, Attempts: 1, LastError: "match has no multiplayer link",
	}}}
	resolver := NewResolver(nil).
		WithFormalMatchReader(ircFormalReader{&service.FormalMatch{ID: matchID, RoomID: roomID}}).
		WithPrivateReaders(ircUserReader{user}, ircRoomReader{room}).
		WithAutomationIssues(automation)
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 100})
	view := &RefereeView{MatchID: matchID.Hex()}
	issues, err := resolver.RefereeView().AutomationIssues(ctx, view, nil)
	if err != nil || len(issues) != 1 || issues[0].EventID != "event-1" || issues[0].LastError == "" {
		t.Fatalf("issues=%+v err=%v", issues, err)
	}
	ok, err := resolver.Mutation().RetryMatchAutomation(ctx, RetryMatchAutomationInput{MatchID: matchID.Hex(), EventID: "event-1"})
	if err != nil || !ok || automation.retriedEvent != "event-1" {
		t.Fatalf("retry ok=%v event=%q err=%v", ok, automation.retriedEvent, err)
	}
}

func TestConfirmIRCResultReleasesEvidenceWhenCommandFails(t *testing.T) {
	matchID, roomID := bson.NewObjectID(), bson.NewObjectID()
	mpLink := "https://osu.ppy.sh/community/matches/42"
	user := &domain.User{OnlineID: 100, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}}
	refereeID := int64(100)
	room := &domain.Room{ID: roomID, Type: domain.RoomTypeMatch, OwnerID: 100, RefereeUserID: &refereeID, MatchID: &matchID, Settings: domain.RoomSettings{MPLink: &mpLink}}
	observations := &ircObservationReader{observation: persistence.IRCObservation{ID: "observation", Channel: "#mp_42", Command: ":!result RED piece-1", ReviewStatus: persistence.IRCReviewPending}}
	commands := &commandExecutorStub{err: &matchcommand.Error{Code: matchcommand.CodeActionNotAllowed, Message: "wrong phase"}}
	resolver := NewResolver(nil, commands).WithFormalMatchReader(ircFormalReader{&service.FormalMatch{ID: matchID, RoomID: roomID}}).WithPrivateReaders(ircUserReader{user}, ircRoomReader{room}).WithIRCReader(observations)
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 100})
	result, err := resolver.Mutation().ConfirmIRCResult(ctx, ConfirmIRCResultInput{MatchID: matchID.Hex(), ExpectedVersion: "3", CommandID: graphqlCommandID, ObservationID: "observation", BoardPieceID: "piece-1", WinningTeam: TeamSideRed})
	if err != nil || result.Success || !observations.claimed || !observations.released || observations.finalized {
		t.Fatalf("result=%+v claim=%v release=%v finalize=%v err=%v", result, observations.claimed, observations.released, observations.finalized, err)
	}
}

func TestConfirmIRCResultFinalizesCommittedEvidence(t *testing.T) {
	matchID, roomID := bson.NewObjectID(), bson.NewObjectID()
	mpLink := "https://osu.ppy.sh/community/matches/42"
	user := &domain.User{OnlineID: 100, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}}
	refereeID := int64(100)
	room := &domain.Room{ID: roomID, Type: domain.RoomTypeMatch, OwnerID: 100, RefereeUserID: &refereeID, MatchID: &matchID, Settings: domain.RoomSettings{MPLink: &mpLink}}
	observations := &ircObservationReader{observation: persistence.IRCObservation{ID: "observation", Channel: "#mp_42", Command: ":!result RED piece-1", ReviewStatus: persistence.IRCReviewPending}}
	commands := &commandExecutorStub{result: matchcommand.Result{CommandID: graphqlCommandID, Disposition: matchcommand.DispositionApplied, PreviousVersion: 3, ResultingVersion: 4, State: matchengine.State{Version: 4}}}
	resolver := NewResolver(nil, commands).WithFormalMatchReader(ircFormalReader{&service.FormalMatch{ID: matchID, RoomID: roomID}}).WithPrivateReaders(ircUserReader{user}, ircRoomReader{room}).WithIRCReader(observations)
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 100})
	result, err := resolver.Mutation().ConfirmIRCResult(ctx, ConfirmIRCResultInput{MatchID: matchID.Hex(), ExpectedVersion: "3", CommandID: graphqlCommandID, ObservationID: "observation", BoardPieceID: "piece-1", WinningTeam: TeamSideRed})
	if err != nil || !result.Success || !observations.claimed || !observations.finalized || observations.released {
		t.Fatalf("result=%+v claim=%v release=%v finalize=%v err=%v", result, observations.claimed, observations.released, observations.finalized, err)
	}
}
