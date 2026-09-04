package matchcommand

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/pkg/errs"
)

const (
	refereeOsuID        = int64(9001)
	redStrategistOsuID  = int64(1001)
	blueStrategistOsuID = int64(2001)
)

var commandTestNow = time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)

func TestOrchestratorAppliesAndReplaysExactlyOnce(t *testing.T) {
	t.Parallel()

	fixture := newCommandFixture(t)
	commandID := "018f4f2c-8f4f-7fd0-a55e-34a7f1a09409"
	request := Request{
		MatchID: fixture.match.ID, ExpectedVersion: 0, CommandID: commandID,
		CallerOsuID: refereeOsuID, Command: matchengine.StartMatch{},
	}

	first, err := fixture.orchestrator.Execute(context.Background(), request)
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if first.Disposition != DispositionApplied || first.PreviousVersion != 0 || first.ResultingVersion != 1 {
		t.Fatalf("first result = %+v", first)
	}
	second, err := fixture.orchestrator.Execute(context.Background(), request)
	if err != nil {
		t.Fatalf("replay Execute: %v", err)
	}
	if second.Disposition != DispositionReplayed || second.ResultingVersion != first.ResultingVersion {
		t.Fatalf("replay result = %+v", second)
	}
	if fixture.store.applied != 1 || fixture.store.state.Version != 1 {
		t.Fatalf("store applied=%d version=%d, want 1/1", fixture.store.applied, fixture.store.state.Version)
	}
}

func TestOrchestratorReplaysCommittedResultAfterAuthorizationChanges(t *testing.T) {
	t.Parallel()

	fixture := newCommandFixture(t)
	request := Request{
		MatchID: fixture.match.ID, ExpectedVersion: 0,
		CommandID:   "018f4f2c-8f4f-7fd0-a55e-34a7f1a09409",
		CallerOsuID: refereeOsuID, Command: matchengine.StartMatch{},
	}
	first, err := fixture.orchestrator.Execute(context.Background(), request)
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	fixture.users[refereeOsuID].IsBanned = true
	fixture.room.RefereeUserID = func() *int64 { v := int64(9999); return &v }()
	replayed, err := fixture.orchestrator.Execute(context.Background(), request)
	if err != nil {
		t.Fatalf("replay after authorization change: %v", err)
	}
	if replayed.Disposition != DispositionReplayed || replayed.ResultingVersion != first.ResultingVersion || fixture.store.applied != 1 {
		t.Fatalf("replay=%+v applied=%d, want original result without another transition", replayed, fixture.store.applied)
	}
}

func TestOrchestratorRejectsDuplicateMismatchAndStaleVersion(t *testing.T) {
	t.Parallel()

	fixture := newCommandFixture(t)
	commandID := "018f4f2c-8f4f-7fd0-a55e-34a7f1a09409"
	_, err := fixture.orchestrator.Execute(context.Background(), Request{
		MatchID: fixture.match.ID, ExpectedVersion: 0, CommandID: commandID,
		CallerOsuID: refereeOsuID, Command: matchengine.StartMatch{},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = fixture.orchestrator.Execute(context.Background(), Request{
		MatchID: fixture.match.ID, ExpectedVersion: 1, CommandID: commandID,
		CallerOsuID: refereeOsuID, Command: matchengine.PauseTimer{Reason: "duplicate id"},
	})
	assertCommandError(t, err, CodeDuplicateCommandMismatch)

	_, err = fixture.orchestrator.Execute(context.Background(), Request{
		MatchID: fixture.match.ID, ExpectedVersion: 0,
		CommandID:   "018f4f2c-8f4f-7fd0-a55e-34a7f1a09410",
		CallerOsuID: refereeOsuID, Command: matchengine.PauseTimer{Reason: "stale page"},
	})
	commandErr := assertCommandError(t, err, CodeMatchVersionConflict)
	if commandErr.CurrentVersion == nil || *commandErr.CurrentVersion != 1 {
		t.Fatalf("current version = %v, want 1", commandErr.CurrentVersion)
	}
}

func TestOrchestratorUsesCurrentUserAndRoomAssignments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*commandFixture)
		caller int64
		want   ErrorCode
	}{
		{name: "banned referee", caller: refereeOsuID, mutate: func(f *commandFixture) { f.users[refereeOsuID].IsBanned = true }, want: CodeUserBanned},
		{name: "pending referee", caller: refereeOsuID, mutate: func(f *commandFixture) { f.users[refereeOsuID].VerifyStatus = domain.Pending }, want: CodeUserNotVerified},
		{name: "unassigned referee", caller: refereeOsuID, mutate: func(f *commandFixture) { f.room.RefereeUserID = func() *int64 { v := int64(9999); return &v }() }, want: CodeRoomRoleRequired},
		{name: "missing user", caller: 8888, mutate: func(*commandFixture) {}, want: CodeAuthRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCommandFixture(t)
			test.mutate(fixture)
			_, err := fixture.orchestrator.Execute(context.Background(), Request{
				MatchID: fixture.match.ID, ExpectedVersion: 0,
				CommandID:   "018f4f2c-8f4f-7fd0-a55e-34a7f1a09409",
				CallerOsuID: test.caller, Command: matchengine.StartMatch{},
			})
			assertCommandError(t, err, test.want)
		})
	}
}

func TestOrchestratorHidesMissingOrUnrelatedFormalResources(t *testing.T) {
	t.Parallel()

	fixture := newCommandFixture(t)
	request := Request{
		MatchID: fixture.match.ID, ExpectedVersion: 0,
		CommandID:   "018f4f2c-8f4f-7fd0-a55e-34a7f1a09409",
		CallerOsuID: refereeOsuID, Command: matchengine.StartMatch{},
	}

	missing := request
	missing.MatchID = bson.NewObjectID()
	_, err := fixture.orchestrator.Execute(context.Background(), missing)
	assertCommandError(t, err, CodeResourceNotFound)

	unrelatedMatchID := bson.NewObjectID()
	fixture.room.MatchID = &unrelatedMatchID
	_, err = fixture.orchestrator.Execute(context.Background(), request)
	assertCommandError(t, err, CodeResourceNotFound)
}

func TestActorForCommandSeparatesGlobalAndRoomRoles(t *testing.T) {
	t.Parallel()

	fixture := newCommandFixture(t)
	red := fixture.users[redStrategistOsuID]
	actor, admin, proxy, err := actorForCommand(red, fixture.room, fixture.redTeam, fixture.blueTeam, matchengine.BanPoolSlot{PoolSlotID: "NM-1"})
	if err != nil || actor.Team == nil || *actor.Team != matchengine.TeamRed || admin || proxy {
		t.Fatalf("red strategist actor = %+v admin=%v proxy=%v err=%v", actor, admin, proxy, err)
	}

	red.Roles = []domain.UserRole{domain.RolePlayer}
	_, _, _, err = actorForCommand(red, fixture.room, fixture.redTeam, fixture.blueTeam, matchengine.BanPoolSlot{PoolSlotID: "NM-1"})
	assertCommandError(t, err, CodeGlobalRoleRequired)

	captain := &domain.User{ID: bson.NewObjectID(), OnlineID: 1101, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RolePlayer}}
	actor, _, _, err = actorForCommand(captain, fixture.room, fixture.redTeam, fixture.blueTeam, matchengine.RequestTB{RequestID: "tb", Basis: matchengine.TBBasisCaptainAgreement})
	if err != nil || actor.Team == nil || *actor.Team != matchengine.TeamRed || actor.Capability != matchengine.CapabilityCaptain {
		t.Fatalf("captain actor = %+v err=%v", actor, err)
	}
}

func TestActorForCommandRecordsOverridesAccurately(t *testing.T) {
	t.Parallel()

	fixture := newCommandFixture(t)
	admin := &domain.User{ID: bson.NewObjectID(), OnlineID: 3001, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleAdmin}}
	fixture.room.RefereeUserID = func() *int64 { v := admin.OnlineID; return &v }()
	actor, adminOverride, refereeOverride, err := actorForCommand(admin, fixture.room, fixture.redTeam, fixture.blueTeam, matchengine.StartMatch{})
	if err != nil || actor.Capability != matchengine.CapabilityReferee || !adminOverride || refereeOverride {
		t.Fatalf("admin referee command = %+v admin=%v proxy=%v err=%v", actor, adminOverride, refereeOverride, err)
	}

	fixture.room.RefereeUserID = func() *int64 { v := refereeOsuID; return &v }()
	referee := fixture.users[refereeOsuID]
	actor, adminOverride, refereeOverride, err = actorForCommand(referee, fixture.room, fixture.redTeam, fixture.blueTeam, matchengine.StartMatch{})
	if err != nil || actor.Capability != matchengine.CapabilityReferee || adminOverride || refereeOverride {
		t.Fatalf("assigned referee command = %+v admin=%v proxy=%v err=%v", actor, adminOverride, refereeOverride, err)
	}

	fixture.room.RefereeUserID = func() *int64 { v := admin.OnlineID; return &v }()
	actor, adminOverride, refereeOverride, err = actorForCommand(admin, fixture.room, fixture.redTeam, fixture.blueTeam, matchengine.RefereeBanPoolSlot{PoolSlotID: "NM-1", Reason: "proxy"})
	if err != nil || actor.Capability != matchengine.CapabilityReferee || !adminOverride || !refereeOverride {
		t.Fatalf("admin proxy command = %+v admin=%v proxy=%v err=%v", actor, adminOverride, refereeOverride, err)
	}
}

func TestOrchestratorAllowsOnlyOneConcurrentCommandAtAVersion(t *testing.T) {
	t.Parallel()

	fixture := newCommandFixture(t)
	requests := []Request{
		{MatchID: fixture.match.ID, ExpectedVersion: 0, CommandID: "018f4f2c-8f4f-7fd0-a55e-34a7f1a09409", CallerOsuID: refereeOsuID, Command: matchengine.StartMatch{}},
		{MatchID: fixture.match.ID, ExpectedVersion: 0, CommandID: "018f4f2c-8f4f-7fd0-a55e-34a7f1a09410", CallerOsuID: refereeOsuID, Command: matchengine.StartMatch{}},
	}
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, request := range requests {
		wait.Go(func() {
			_, err := fixture.orchestrator.Execute(context.Background(), request)
			results <- err
		})
	}
	wait.Wait()
	close(results)

	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
			continue
		}
		if commandErr := ErrorOf(err); commandErr != nil && commandErr.Code == CodeMatchVersionConflict {
			conflict++
			continue
		}
		t.Fatalf("unexpected concurrent error: %v", err)
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent results success=%d conflict=%d", success, conflict)
	}
}

func TestOrchestratorRejectsNonUUIDCommandID(t *testing.T) {
	t.Parallel()
	fixture := newCommandFixture(t)
	_, err := fixture.orchestrator.Execute(context.Background(), Request{
		MatchID: fixture.match.ID, ExpectedVersion: 0, CommandID: "cmd1",
		CallerOsuID: refereeOsuID, Command: matchengine.StartMatch{},
	})
	assertCommandError(t, err, CodeInvalidRequest)
}

func TestOrchestratorPublishesEngineRuleCode(t *testing.T) {
	fixture := newCommandFixture(t)
	_, err := fixture.orchestrator.Execute(context.Background(), Request{
		MatchID: fixture.match.ID, ExpectedVersion: 0, CommandID: "018f4f2c-8f4f-7fd0-a55e-34a7f1a09409",
		CallerOsuID: redStrategistOsuID, Command: matchengine.BanPoolSlot{PoolSlotID: "NM-1"},
	})
	assertCommandError(t, err, ErrorCode(matchengine.CodeMatchLifecycleConflict))
}

func TestCommandPayloadUsesStableJSONContract(t *testing.T) {
	payload, err := json.Marshal(matchengine.RefereeRobPiece{
		ActingTeam: matchengine.TeamBlue, TargetPieceID: "target",
		SacrificeSets: [][]string{{"one", "two"}}, Reason: "captain disconnected",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"actingTeam":"BLUE","targetPieceId":"target","sacrificeSets":[["one","two"]],"reason":"captain disconnected"}`
	if string(payload) != want {
		t.Fatalf("payload = %s, want %s", payload, want)
	}
}

func TestEveryEngineCommandHasExactlyOneAuthorizationPolicy(t *testing.T) {
	commands := []matchengine.Command{
		matchengine.StartMatch{}, matchengine.BanPoolSlot{}, matchengine.RefereeBanPoolSlot{},
		matchengine.PlacePiece{}, matchengine.RefereePlacePiece{}, matchengine.PlaceShiro{}, matchengine.RefereePlaceShiro{},
		matchengine.RobPiece{}, matchengine.RefereeRobPiece{}, matchengine.ConfirmBeatmapResult{},
		matchengine.GrantAdditionalTime{}, matchengine.CalibrateTimer{}, matchengine.PauseTimer{}, matchengine.ResumeTimer{},
		matchengine.SuspendMatch{}, matchengine.ResumeMatch{}, matchengine.SkipCurrentAction{}, matchengine.AbortMatch{},
		matchengine.RequestTB{}, matchengine.RefereeRequestTB{}, matchengine.RespondTBRequest{}, matchengine.RefereeRespondTBRequest{},
		matchengine.StartTB{}, matchengine.ConfirmTBResult{}, matchengine.RecordSurrender{},
	}
	seenTypes := make(map[string]bool, len(commands))
	for _, command := range commands {
		commandName, ok := commandType(command)
		if !ok || commandName == "" || seenTypes[commandName] {
			t.Fatalf("command %T has invalid or duplicate command type %q", command, commandName)
		}
		seenTypes[commandName] = true
		policies := 0
		for _, matches := range []bool{isStrategistCommand(command), isCaptainCommand(command), isRefereeCommand(command), isRefereeProxyCommand(command)} {
			if matches {
				policies++
			}
		}
		if policies != 1 {
			t.Fatalf("command %T matches %d authorization policies", command, policies)
		}
	}
	if len(seenTypes) != 25 {
		t.Fatalf("covered %d commands, want 25", len(seenTypes))
	}
}

func TestOrchestratorRunsCompleteMatchWithRetryAndStaleClient(t *testing.T) {
	fixture := newCommandFixture(t)
	ctx := context.Background()
	version := uint64(0)
	execute := func(caller int64, command matchengine.Command) Result {
		t.Helper()
		result, err := fixture.orchestrator.Execute(ctx, Request{MatchID: fixture.match.ID, ExpectedVersion: version, CommandID: uuid.NewString(), CallerOsuID: caller, Command: command})
		if err != nil {
			t.Fatalf("version %d command %T: %v", version, command, err)
		}
		version = result.ResultingVersion
		return result
	}

	execute(refereeOsuID, matchengine.StartMatch{})
	for _, ban := range []struct {
		caller int64
		slot   string
	}{{redStrategistOsuID, "NM-1"}, {blueStrategistOsuID, "NM-2"}, {blueStrategistOsuID, "NM-3"}, {redStrategistOsuID, "NM-4"}} {
		execute(ban.caller, matchengine.BanPoolSlot{PoolSlotID: ban.slot})
	}
	opening := []struct {
		cell   matchengine.Cell
		winner matchengine.TeamSide
	}{{"A1", matchengine.TeamBlue}, {"D4", matchengine.TeamRed}, {"B1", matchengine.TeamBlue}, {"D2", matchengine.TeamBlue}, {"C1", matchengine.TeamBlue}, {"D3", matchengine.TeamBlue}}
	for index, placement := range opening {
		piece := fmt.Sprintf("piece-%d", index+1)
		caller := blueStrategistOsuID
		if fixture.store.state.ActiveTeam == matchengine.TeamRed {
			caller = redStrategistOsuID
		}
		execute(caller, matchengine.PlacePiece{PoolSlotID: fmt.Sprintf("NM-%d", index+5), PieceID: piece, Cell: placement.cell})
		execute(refereeOsuID, matchengine.ConfirmBeatmapResult{BoardPieceID: piece, WinningTeam: placement.winner})
	}

	pauseRequest := Request{MatchID: fixture.match.ID, ExpectedVersion: version, CommandID: uuid.NewString(), CallerOsuID: refereeOsuID, Command: matchengine.PauseTimer{Reason: "network verification"}}
	paused, err := fixture.orchestrator.Execute(ctx, pauseRequest)
	if err != nil {
		t.Fatal(err)
	}
	version = paused.ResultingVersion
	replayed, err := fixture.orchestrator.Execute(ctx, pauseRequest)
	if err != nil || replayed.Disposition != DispositionReplayed || replayed.ResultingVersion != version || len(replayed.Events) != len(paused.Events) || replayed.Events[0].EventID != paused.Events[0].EventID {
		t.Fatalf("pause replay=%+v err=%v", replayed, err)
	}
	_, err = fixture.orchestrator.Execute(ctx, Request{MatchID: fixture.match.ID, ExpectedVersion: version - 1, CommandID: uuid.NewString(), CallerOsuID: refereeOsuID, Command: matchengine.ResumeTimer{Reason: "stale tab"}})
	assertCommandError(t, err, CodeMatchVersionConflict)
	execute(refereeOsuID, matchengine.ResumeTimer{Reason: "network stable"})
	execute(blueStrategistOsuID, matchengine.RobPiece{TargetPieceID: "piece-2", SacrificeSets: [][]string{{"piece-1", "piece-3", "piece-5"}}})

	closing := []struct {
		cell   matchengine.Cell
		winner matchengine.TeamSide
	}{{"A2", matchengine.TeamRed}, {"B2", matchengine.TeamBlue}, {"C2", matchengine.TeamRed}, {"A3", matchengine.TeamRed}, {"B3", matchengine.TeamBlue}, {"C3", matchengine.TeamRed}}
	for index, placement := range closing {
		piece := fmt.Sprintf("piece-%d", index+7)
		caller := blueStrategistOsuID
		if fixture.store.state.ActiveTeam == matchengine.TeamRed {
			caller = redStrategistOsuID
		}
		execute(caller, matchengine.PlacePiece{PoolSlotID: fmt.Sprintf("NM-%d", index+11), PieceID: piece, Cell: placement.cell})
		execute(refereeOsuID, matchengine.ConfirmBeatmapResult{BoardPieceID: piece, WinningTeam: placement.winner})
	}
	execute(1101, matchengine.RequestTB{RequestID: "full-flow-tb", Basis: matchengine.TBBasisCaptainAgreement})
	execute(2101, matchengine.RespondTBRequest{RequestID: "full-flow-tb", Accept: true})
	execute(refereeOsuID, matchengine.StartTB{Reason: "lobby ready"})
	execute(refereeOsuID, matchengine.ConfirmTBResult{WinningTeam: matchengine.TeamRed})

	state := fixture.store.state
	if state.Lifecycle != matchengine.LifecycleFinished || state.Winner == nil || *state.Winner != matchengine.TeamRed || version != 36 {
		t.Fatalf("final state lifecycle=%s winner=%v version=%d", state.Lifecycle, state.Winner, version)
	}
	if fixture.store.applied != 36 || fixture.store.nextSequence == 0 {
		t.Fatalf("applied=%d sequence=%d", fixture.store.applied, fixture.store.nextSequence)
	}
	lateReplay, err := fixture.orchestrator.Execute(ctx, pauseRequest)
	if err != nil || lateReplay.Disposition != DispositionReplayed || lateReplay.ResultingVersion != paused.ResultingVersion || fixture.store.state.Version != 36 {
		t.Fatalf("late replay=%+v current=%d err=%v", lateReplay, fixture.store.state.Version, err)
	}
}

type commandFixture struct {
	orchestrator *Orchestrator
	store        *memoryTransactionStore
	users        map[int64]*domain.User
	match        *domain.Match
	room         *domain.Room
	redTeam      *domain.Team
	blueTeam     *domain.Team
}

func newCommandFixture(t *testing.T) *commandFixture {
	t.Helper()
	roomID, matchID := bson.NewObjectID(), bson.NewObjectID()
	redStrategist, blueStrategist := redStrategistOsuID, blueStrategistOsuID
	redLeader, blueLeader := int64(1101), int64(2101)
	redTeam := &domain.Team{
		ID:           bson.NewObjectID(),
		LeaderID:     &redLeader,
		StrategistID: &redStrategist,
		Players:      []int64{1101, 1102, 1103, 1104, 1105, 1106, 1107, 1108},
	}
	blueTeam := &domain.Team{
		ID:           bson.NewObjectID(),
		LeaderID:     &blueLeader,
		StrategistID: &blueStrategist,
		Players:      []int64{2101, 2102, 2103, 2104, 2105, 2106, 2107, 2108},
	}
	room := &domain.Room{
		ID: roomID, Type: domain.RoomTypeMatch, OwnerID: refereeOsuID, RefereeUserID: func() *int64 { v := refereeOsuID; return &v }(), MatchID: &matchID,
		Settings: domain.RoomSettings{
			RedTeamID:  &redTeam.ID,
			BlueTeamID: &blueTeam.ID,
		},
	}
	// Two-phase start: tests that exercise referee-triggered START_MATCH work
	// against a match whose strategists have already confirmed readiness.
	// Tests for the readiness gate itself override this back to Pending.
	match := &domain.Match{ID: matchID, RoomID: roomID, RoomType: domain.RoomTypeMatch, Status: domain.MatchStatusReady}
	users := map[int64]*domain.User{
		refereeOsuID:        {ID: bson.NewObjectID(), OnlineID: refereeOsuID, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}},
		redStrategistOsuID:  {ID: bson.NewObjectID(), OnlineID: redStrategistOsuID, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleStrategist}},
		blueStrategistOsuID: {ID: bson.NewObjectID(), OnlineID: blueStrategistOsuID, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleStrategist}},
		1101:                {ID: bson.NewObjectID(), OnlineID: 1101, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RolePlayer}},
		2101:                {ID: bson.NewObjectID(), OnlineID: 2101, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RolePlayer}},
	}
	store := &memoryTransactionStore{state: commandReadyState(t), receipts: make(map[string]memoryReceipt)}
	fixture := &commandFixture{store: store, users: users, match: match, room: room, redTeam: redTeam, blueTeam: blueTeam}
	fixture.orchestrator = NewOrchestrator(
		store,
		userMapReader(users),
		matchMapReader{matchID: match},
		roomMapReader{roomID: room},
		teamMapReader{redTeam.ID: redTeam, blueTeam.ID: blueTeam},
		func() time.Time { return commandTestNow },
		nil,
	)
	return fixture
}

type teamMapReader map[bson.ObjectID]*domain.Team

func (r teamMapReader) ByID(_ context.Context, id bson.ObjectID) (*domain.Team, error) {
	team, ok := r[id]
	if !ok {
		return nil, errs.ErrNotFound
	}
	clone := *team
	return &clone, nil
}

type userMapReader map[int64]*domain.User

func (r userMapReader) ByOsuID(_ context.Context, id int64) (*domain.User, error) {
	user, ok := r[id]
	if !ok {
		return nil, errs.ErrNotFound
	}
	clone := *user
	clone.Roles = append([]domain.UserRole(nil), user.Roles...)
	return &clone, nil
}

type matchMapReader map[bson.ObjectID]*domain.Match

func (r matchMapReader) ByID(_ context.Context, id bson.ObjectID) (*domain.Match, error) {
	match, ok := r[id]
	if !ok {
		return nil, errs.ErrNotFound
	}
	clone := *match
	return &clone, nil
}

type roomMapReader map[bson.ObjectID]*domain.Room

func (r roomMapReader) ByID(_ context.Context, id bson.ObjectID) (*domain.Room, error) {
	room, ok := r[id]
	if !ok {
		return nil, errs.ErrNotFound
	}
	clone := *room
	return &clone, nil
}

type memoryReceipt struct {
	hash   string
	result Result
}

type memoryTransactionStore struct {
	mu           sync.Mutex
	state        matchengine.State
	receipts     map[string]memoryReceipt
	applied      int
	nextSequence uint64
}

func (s *memoryTransactionStore) Apply(ctx context.Context, envelope Envelope, authorize AuthorizeFunc, execute ExecuteFunc) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if receipt, ok := s.receipts[envelope.CommandID]; ok {
		if receipt.hash != envelope.RequestHash {
			return Result{}, NewError(CodeDuplicateCommandMismatch, "commandId was already used for a different request", nil)
		}
		result := receipt.result
		result.Disposition = DispositionReplayed
		return result, nil
	}
	actor, err := authorize(ctx)
	if err != nil {
		return Result{}, err
	}
	if envelope.ExpectedVersion != s.state.Version {
		return Result{}, VersionConflict(envelope.ExpectedVersion, s.state.Version)
	}
	transition, err := execute(s.state.Clone(), actor)
	if err != nil {
		return Result{}, err
	}
	committedEvents := make([]CommittedEvent, 0, len(transition.Events))
	for index, event := range transition.Events {
		sequence := s.nextSequence + uint64(index) + 1
		committedEvents = append(committedEvents, CommittedEvent{
			EventID:  fmt.Sprintf("%s-event-%d", envelope.CommandID, index+1),
			Sequence: sequence, ResultingVersion: transition.State.Version,
			Type: event.Type, OccurredAt: envelope.OccurredAt, Payload: event,
		})
	}
	result := Result{
		CommandID: envelope.CommandID, Disposition: DispositionApplied,
		PreviousVersion: s.state.Version, ResultingVersion: transition.State.Version,
		State: transition.State.Clone(), Events: committedEvents,
	}
	s.state = transition.State.Clone()
	s.nextSequence += uint64(len(transition.Events))
	s.receipts[envelope.CommandID] = memoryReceipt{hash: envelope.RequestHash, result: result}
	s.applied++
	return result, nil
}

func commandReadyState(t *testing.T) matchengine.State {
	t.Helper()
	pool := make([]matchengine.PoolSlot, 0, 22)
	for index := 1; index <= 20; index++ {
		pool = append(pool, matchengine.PoolSlot{ID: fmt.Sprintf("NM-%d", index), Mod: matchengine.ModNM})
	}
	pool = append(pool, matchengine.PoolSlot{ID: "SHIRO-1", Mod: matchengine.ModShiro}, matchengine.PoolSlot{ID: "TB-1", Mod: matchengine.ModTB})
	state, err := matchengine.NewReadyState(matchengine.Configuration{
		FirstBan: matchengine.TeamRed, FirstPick: matchengine.TeamBlue, PoolSlots: pool,
		Rosters: map[matchengine.TeamSide]matchengine.Roster{
			matchengine.TeamRed:  {LeaderID: 1101, PlayerIDs: []int64{1101, 1102, 1103, 1104, 1105, 1106, 1107, 1108}},
			matchengine.TeamBlue: {LeaderID: 2101, PlayerIDs: []int64{2101, 2102, 2103, 2104, 2105, 2106, 2107, 2108}},
		},
		Timers: matchengine.StandardTimerConfiguration(),
	})
	if err != nil {
		t.Fatalf("NewReadyState: %v", err)
	}
	return state
}

func assertCommandError(t *testing.T, err error, code ErrorCode) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s", code)
	}
	var commandErr *Error
	if !errors.As(err, &commandErr) || commandErr.Code != code {
		t.Fatalf("error = %T %v, want %s", err, err, code)
	}
	return commandErr
}

func assertSameJSON(t *testing.T, left, right any) {
	t.Helper()
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	if string(leftJSON) != string(rightJSON) {
		t.Fatalf("JSON differs:\n%s\n%s", leftJSON, rightJSON)
	}
}
