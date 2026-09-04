package matchcommand

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/pkg/errs"
)

type UserReader interface {
	ByOsuID(context.Context, int64) (*domain.User, error)
}

type MatchReader interface {
	ByID(context.Context, bson.ObjectID) (*domain.Match, error)
}

type RoomReader interface {
	ByID(context.Context, bson.ObjectID) (*domain.Room, error)
}

// TeamReader loads Team entities so command authorization can resolve the
// strategist / captain assignments from the room's team references.
type TeamReader interface {
	ByID(context.Context, bson.ObjectID) (*domain.Team, error)
}

type Clock func() time.Time

type Orchestrator struct {
	store   TransactionStore
	users   UserReader
	matches MatchReader
	rooms   RoomReader
	teams   TeamReader
	now     Clock
	log     *zap.Logger
}

func NewOrchestrator(store TransactionStore, users UserReader, matches MatchReader, rooms RoomReader, teams TeamReader, now Clock, log *zap.Logger) *Orchestrator {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Orchestrator{store: store, users: users, matches: matches, rooms: rooms, teams: teams, now: now, log: log}
}

func (o *Orchestrator) Execute(ctx context.Context, request Request) (Result, error) {
	if o == nil || o.store == nil || o.users == nil || o.matches == nil || o.rooms == nil {
		return Result{}, NewError(CodeInternalError, "command orchestrator is not configured", nil)
	}
	if request.MatchID == bson.NilObjectID || request.Command == nil {
		return Result{}, NewError(CodeInvalidRequest, "match and command are required", nil)
	}
	if !request.System && (request.CallerOsuID <= 0) {
		return Result{}, NewError(CodeInvalidRequest, "caller is required for non-system commands", nil)
	}
	parsedCommandID, err := uuid.Parse(request.CommandID)
	if err != nil || parsedCommandID == uuid.Nil {
		return Result{}, NewError(CodeInvalidRequest, "commandId must be a non-zero UUID", err)
	}
	commandType, ok := commandType(request.Command)
	if !ok {
		return Result{}, NewError(CodeInvalidRequest, "unsupported match command", nil)
	}
	payload, err := json.Marshal(request.Command)
	if err != nil {
		return Result{}, NewError(CodeInvalidRequest, "command payload cannot be encoded", err)
	}
	requestHash, err := canonicalRequestHash(request, commandType, payload)
	if err != nil {
		return Result{}, NewError(CodeInvalidRequest, "command request cannot be encoded", err)
	}
	now := o.now().UTC()
	if now.IsZero() {
		return Result{}, NewError(CodeInternalError, "server clock returned zero time", nil)
	}

	o.log.Debug("executing match command",
		zap.String("match_id", request.MatchID.Hex()),
		zap.Int64("caller_osu_id", request.CallerOsuID),
		zap.String("command_type", commandType),
		zap.String("command_id", request.CommandID),
		zap.Uint64("expected_version", request.ExpectedVersion),
	)

	envelope := Envelope{
		MatchID: request.MatchID, ExpectedVersion: request.ExpectedVersion,
		CommandID: request.CommandID, CommandType: commandType,
		RequestHash: requestHash, PayloadJSON: payload, OccurredAt: now,
	}
	result, err := o.store.Apply(
		ctx,
		envelope,
		func(txCtx context.Context) (AuthorizedActor, error) {
			return o.authorize(txCtx, request)
		},
		func(state matchengine.State, actor AuthorizedActor) (matchengine.Transition, error) {
			transition, executeErr := matchengine.Execute(state, actor.EngineActor, request.Command, now)
			if executeErr != nil {
				var ruleErr *matchengine.RuleError
				if errors.As(executeErr, &ruleErr) {
					o.log.Warn("match command rule violation",
						zap.String("match_id", request.MatchID.Hex()),
						zap.String("command_type", commandType),
						zap.String("rule_error", ruleErr.Message),
						zap.String("rule_code", string(ruleErr.Code)),
					)
					return matchengine.Transition{}, NewError(ErrorCode(ruleErr.Code), ruleErr.Message, ruleErr)
				}
				o.log.Error("match command execution failed",
					zap.String("match_id", request.MatchID.Hex()),
					zap.String("command_type", commandType),
					zap.Error(executeErr),
				)
				return matchengine.Transition{}, NewError(CodeInternalError, "execute match command", executeErr)
			}
			return transition, nil
		},
	)
	if err != nil {
		o.log.Warn("match command failed",
			zap.String("match_id", request.MatchID.Hex()),
			zap.String("command_type", commandType),
			zap.Int64("caller_osu_id", request.CallerOsuID),
			zap.Error(err),
		)
		return result, err
	}
	o.log.Info("match command executed",
		zap.String("match_id", request.MatchID.Hex()),
		zap.String("command_type", commandType),
		zap.Int64("caller_osu_id", request.CallerOsuID),
		zap.Uint64("new_version", result.ResultingVersion),
	)
	return result, nil
}

func canonicalRequestHash(request Request, commandType string, payload []byte) (string, error) {
	canonical, err := json.Marshal(struct {
		MatchID         string          `json:"matchId"`
		ExpectedVersion uint64          `json:"expectedVersion"`
		CommandID       string          `json:"commandId"`
		CallerOsuID     int64           `json:"callerOsuId"`
		CommandType     string          `json:"commandType"`
		Payload         json.RawMessage `json:"payload"`
	}{
		MatchID: request.MatchID.Hex(), ExpectedVersion: request.ExpectedVersion,
		CommandID: request.CommandID, CallerOsuID: request.CallerOsuID,
		CommandType: commandType, Payload: payload,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return fmt.Sprintf("%x", sum[:]), nil
}

func (o *Orchestrator) authorize(ctx context.Context, request Request) (AuthorizedActor, error) {
	if request.System {
		return o.authorizeSystem(ctx, request)
	}
	user, err := o.users.ByOsuID(ctx, request.CallerOsuID)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			o.log.Warn("match command auth: user not found", zap.Int64("osu_id", request.CallerOsuID))
			return AuthorizedActor{}, NewError(CodeAuthRequired, "authenticated user no longer exists", err)
		}
		o.log.Error("match command auth: failed to load user", zap.Int64("osu_id", request.CallerOsuID), zap.Error(err))
		return AuthorizedActor{}, NewError(CodeInternalError, "load current user", err)
	}
	if user.IsBanned {
		o.log.Warn("match command auth: user is banned", zap.Int64("osu_id", request.CallerOsuID), zap.String("username", user.Username))
		return AuthorizedActor{}, NewError(CodeUserBanned, "user is banned from formal match operations", nil)
	}
	if user.VerifyStatus != domain.Verified {
		o.log.Warn("match command auth: user not verified", zap.Int64("osu_id", request.CallerOsuID), zap.String("verify_status", string(user.VerifyStatus)))
		return AuthorizedActor{}, NewError(CodeUserNotVerified, "user is not verified for formal match operations", nil)
	}
	return o.authorizeUser(ctx, user, request)
}

// authorizeUser is the human-caller authorization path. It reloads the
// match and room so the gate sees the latest persisted state and maps the
// user's roles onto a matchengine.Actor via actorForCommand.
func (o *Orchestrator) authorizeUser(ctx context.Context, user *domain.User, request Request) (AuthorizedActor, error) {
	match, err := o.matches.ByID(ctx, request.MatchID)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			o.log.Warn("match command auth: match not found", zap.String("match_id", request.MatchID.Hex()))
			return AuthorizedActor{}, NewError(CodeResourceNotFound, "formal match was not found", err)
		}
		o.log.Error("match command auth: failed to load match", zap.String("match_id", request.MatchID.Hex()), zap.Error(err))
		return AuthorizedActor{}, NewError(CodeInternalError, "load formal match", err)
	}
	room, err := o.rooms.ByID(ctx, match.RoomID)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			o.log.Warn("match command auth: room not found", zap.String("room_id", match.RoomID.Hex()))
			return AuthorizedActor{}, NewError(CodeResourceNotFound, "formal match room was not found", err)
		}
		o.log.Error("match command auth: failed to load room", zap.String("room_id", match.RoomID.Hex()), zap.Error(err))
		return AuthorizedActor{}, NewError(CodeInternalError, "load formal match room", err)
	}
	if match.RoomType != domain.RoomTypeMatch || room.Type != domain.RoomTypeMatch || room.ID != match.RoomID ||
		room.MatchID == nil || *room.MatchID != request.MatchID {
		o.log.Warn("match command auth: not an authoritative formal match",
			zap.String("match_id", request.MatchID.Hex()),
			zap.String("match_room_type", string(match.RoomType)),
			zap.String("room_type", string(room.Type)),
		)
		return AuthorizedActor{}, NewError(CodeResourceNotFound, "match is not an authoritative formal room match", nil)
	}

	redTeam, blueTeam, teamErr := o.roomTeams(ctx, room)
	if teamErr != nil {
		o.log.Error("match command auth: failed to load room teams",
			zap.String("room_id", room.ID.Hex()), zap.Error(teamErr))
		return AuthorizedActor{}, NewError(CodeInternalError, "load room teams", teamErr)
	}

	// Two-phase start gate (referee-triggered START_MATCH only): the human
	// referee can only confirm the start once both strategists have pressed
	// Ready (match.Status == Ready). The casual/private auto-start fires
	// through `authorizeSystem` instead and does not enter this branch.
	//
	// This is an authoritative backend guard: even if a stale UI somehow
	// surfaces START_MATCH before readiness completes, the orchestrator
	// rejects the command with a structured error.
	if _, isStart := request.Command.(matchengine.StartMatch); isStart {
		if match.Status != domain.MatchStatusReady {
			o.log.Warn("match command auth: start match blocked — strategists not yet ready",
				zap.String("match_id", request.MatchID.Hex()),
				zap.String("match_status", string(match.Status)),
			)
			return AuthorizedActor{}, NewError(
				CodeActionNotAllowed,
				"双方策略师尚未确认准备，无法开始比赛",
				nil,
			)
		}
	}

	actor, adminOverride, refereeOverride, err := actorForCommand(user, room, redTeam, blueTeam, request.Command)
	if err != nil {
		o.log.Warn("match command auth: role authorization failed",
			zap.Int64("osu_id", request.CallerOsuID),
			zap.String("username", user.Username),
			zap.Error(err),
		)
		return AuthorizedActor{}, err
	}
	roles := make([]string, len(user.Roles))
	for index, role := range user.Roles {
		roles[index] = string(role)
	}
	return AuthorizedActor{
		UserID: user.ID, OsuID: user.OnlineID, GlobalRoles: roles,
		EngineActor: actor, AdminOverride: adminOverride,
		RefereeOverride: refereeOverride, Reason: commandReason(request.Command),
	}, nil
}

// authorizeSystem handles orchestrator-driven commands (currently the
// casual auto-start path fired by RoomService.MarkStrategistReady). It only
// verifies that the match-shell ↔ room association is intact — there is no
// human caller to gate against — and synthesizes a SystemRefereeActor with
// AdminOverride=true so the engine rules (startMatch) can admit it.
func (o *Orchestrator) authorizeSystem(ctx context.Context, request Request) (AuthorizedActor, error) {
	match, err := o.matches.ByID(ctx, request.MatchID)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			o.log.Warn("match command auth (system): match not found", zap.String("match_id", request.MatchID.Hex()))
			return AuthorizedActor{}, NewError(CodeResourceNotFound, "formal match was not found", err)
		}
		o.log.Error("match command auth (system): failed to load match",
			zap.String("match_id", request.MatchID.Hex()), zap.Error(err))
		return AuthorizedActor{}, NewError(CodeInternalError, "load formal match", err)
	}
	room, err := o.rooms.ByID(ctx, match.RoomID)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			o.log.Warn("match command auth (system): room not found", zap.String("room_id", match.RoomID.Hex()))
			return AuthorizedActor{}, NewError(CodeResourceNotFound, "formal match room was not found", err)
		}
		o.log.Error("match command auth (system): failed to load room",
			zap.String("room_id", match.RoomID.Hex()), zap.Error(err))
		return AuthorizedActor{}, NewError(CodeInternalError, "load formal match room", err)
	}
	if room.ID != match.RoomID || room.MatchID == nil || *room.MatchID != request.MatchID {
		o.log.Warn("match command auth (system): shell not paired",
			zap.String("match_id", request.MatchID.Hex()),
			zap.String("room_id", room.ID.Hex()),
		)
		return AuthorizedActor{}, NewError(CodeResourceNotFound, "match shell is not paired with the owning room", nil)
	}
	return AuthorizedActor{
		EngineActor:   matchengine.SystemRefereeActor(),
		AdminOverride: true,
		Reason:        commandReason(request.Command),
	}, nil
}

func actorForCommand(user *domain.User, room *domain.Room, redTeam, blueTeam *domain.Team, command matchengine.Command) (matchengine.Actor, bool, bool, error) {
	if isRefereeCommand(command) || isRefereeProxyCommand(command) {
		isAdmin := user.HasRole(domain.RoleAdmin)
		hasRefereeRole := user.HasRole(domain.RoleReferee)
		isAssignedReferee := hasRefereeRole && room.RefereeUserID != nil && *room.RefereeUserID == user.OnlineID
		if !isAdmin && !isAssignedReferee {
			if !hasRefereeRole {
				return matchengine.Actor{}, false, false, NewError(CodeGlobalRoleRequired, "referee or administrator role is required", nil)
			}
			return matchengine.Actor{}, false, false, NewError(CodeRoomRoleRequired, "user is not the assigned referee for this room", nil)
		}
		return matchengine.RefereeActor(), isAdmin && !isAssignedReferee, isRefereeProxyCommand(command), nil
	}

	if isStrategistCommand(command) {
		if !user.HasRole(domain.RoleStrategist) {
			return matchengine.Actor{}, false, false, NewError(CodeGlobalRoleRequired, "strategist role is required", nil)
		}
		side, ok := assignedStrategistSide(redTeam, blueTeam, user.OnlineID)
		if !ok {
			return matchengine.Actor{}, false, false, NewError(CodeRoomRoleRequired, "user is not an assigned strategist for this room", nil)
		}
		return matchengine.StrategistActor(side), false, false, nil
	}

	if isCaptainCommand(command) {
		side, ok := assignedCaptainSide(redTeam, blueTeam, user.OnlineID)
		if !ok {
			return matchengine.Actor{}, false, false, NewError(CodeRoomRoleRequired, "user is not a team captain for this room", nil)
		}
		return matchengine.CaptainActor(side), false, false, nil
	}

	return matchengine.Actor{}, false, false, NewError(CodeInvalidRequest, "command has no authorization policy", nil)
}

// roomTeams resolves the red and blue team entities referenced by the room
// settings. Missing links (or a missing team reader) yield nil teams.
func (o *Orchestrator) roomTeams(ctx context.Context, room *domain.Room) (*domain.Team, *domain.Team, error) {
	if o.teams == nil || room == nil {
		return nil, nil, nil
	}
	var redTeam, blueTeam *domain.Team
	if room.Settings.RedTeamID != nil {
		team, err := o.teams.ByID(ctx, *room.Settings.RedTeamID)
		if err != nil {
			return nil, nil, err
		}
		redTeam = team
	}
	if room.Settings.BlueTeamID != nil {
		team, err := o.teams.ByID(ctx, *room.Settings.BlueTeamID)
		if err != nil {
			return nil, nil, err
		}
		blueTeam = team
	}
	return redTeam, blueTeam, nil
}

func assignedStrategistSide(redTeam, blueTeam *domain.Team, osuID int64) (matchengine.TeamSide, bool) {
	side, ok := domain.StrategistSide(redTeam, blueTeam, osuID)
	if !ok {
		return "", false
	}
	return engineTeamSide(side), true
}

func assignedCaptainSide(redTeam, blueTeam *domain.Team, osuID int64) (matchengine.TeamSide, bool) {
	side, ok := domain.CaptainSide(redTeam, blueTeam, osuID)
	if !ok {
		return "", false
	}
	return engineTeamSide(side), true
}

// engineTeamSide converts the lowercase domain side to the engine-side enum.
func engineTeamSide(side domain.TeamSide) matchengine.TeamSide {
	if side == domain.TeamSideBlue {
		return matchengine.TeamBlue
	}
	return matchengine.TeamRed
}

func isStrategistCommand(command matchengine.Command) bool {
	switch command.(type) {
	case matchengine.BanPoolSlot, matchengine.PlacePiece, matchengine.PlaceShiro, matchengine.RobPiece:
		return true
	default:
		return false
	}
}

func isCaptainCommand(command matchengine.Command) bool {
	switch command.(type) {
	case matchengine.RequestTB, matchengine.RespondTBRequest:
		return true
	default:
		return false
	}
}

func isRefereeProxyCommand(command matchengine.Command) bool {
	switch command.(type) {
	case matchengine.RefereeBanPoolSlot, matchengine.RefereePlacePiece,
		matchengine.RefereePlaceShiro, matchengine.RefereeRobPiece,
		matchengine.RefereeRequestTB, matchengine.RefereeRespondTBRequest:
		return true
	default:
		return false
	}
}

func isRefereeCommand(command matchengine.Command) bool {
	switch command.(type) {
	case matchengine.StartMatch, matchengine.GrantAdditionalTime,
		matchengine.CalibrateTimer, matchengine.PauseTimer, matchengine.ResumeTimer,
		matchengine.SuspendMatch, matchengine.ResumeMatch, matchengine.SkipCurrentAction,
		matchengine.AbortMatch, matchengine.StartTB, matchengine.ConfirmTBResult,
		matchengine.RecordSurrender, matchengine.ConfirmBeatmapResult:
		return true
	default:
		return false
	}
}

func commandReason(command matchengine.Command) string {
	switch typed := command.(type) {
	case matchengine.RefereeBanPoolSlot:
		return typed.Reason
	case matchengine.RefereePlacePiece:
		return typed.Reason
	case matchengine.RefereePlaceShiro:
		return typed.Reason
	case matchengine.RefereeRobPiece:
		return typed.Reason
	case matchengine.GrantAdditionalTime:
		return typed.Reason
	case matchengine.CalibrateTimer:
		return typed.Reason
	case matchengine.PauseTimer:
		return typed.Reason
	case matchengine.ResumeTimer:
		return typed.Reason
	case matchengine.SuspendMatch:
		return typed.Reason
	case matchengine.ResumeMatch:
		return typed.Reason
	case matchengine.SkipCurrentAction:
		return typed.Reason
	case matchengine.AbortMatch:
		return typed.Reason
	case matchengine.RefereeRequestTB:
		return typed.Reason
	case matchengine.RefereeRespondTBRequest:
		return typed.Reason
	case matchengine.StartTB:
		return typed.Reason
	case matchengine.RecordSurrender:
		return typed.Reason
	default:
		return ""
	}
}

func commandType(command matchengine.Command) (string, bool) {
	switch command.(type) {
	case matchengine.StartMatch:
		return "START_MATCH", true
	case matchengine.BanPoolSlot:
		return "BAN_POOL_SLOT", true
	case matchengine.RefereeBanPoolSlot:
		return "REFEREE_BAN_POOL_SLOT", true
	case matchengine.PlacePiece:
		return "PLACE_PIECE", true
	case matchengine.RefereePlacePiece:
		return "REFEREE_PLACE_PIECE", true
	case matchengine.PlaceShiro:
		return "PLACE_SHIRO", true
	case matchengine.RefereePlaceShiro:
		return "REFEREE_PLACE_SHIRO", true
	case matchengine.RobPiece:
		return "ROB_PIECE", true
	case matchengine.RefereeRobPiece:
		return "REFEREE_ROB_PIECE", true
	case matchengine.GrantAdditionalTime:
		return "GRANT_ADDITIONAL_TIME", true
	case matchengine.CalibrateTimer:
		return "CALIBRATE_TIMER", true
	case matchengine.PauseTimer:
		return "PAUSE_TIMER", true
	case matchengine.ResumeTimer:
		return "RESUME_TIMER", true
	case matchengine.SuspendMatch:
		return "SUSPEND_MATCH", true
	case matchengine.ResumeMatch:
		return "RESUME_MATCH", true
	case matchengine.SkipCurrentAction:
		return "SKIP_CURRENT_ACTION", true
	case matchengine.AbortMatch:
		return "ABORT_MATCH", true
	case matchengine.RequestTB:
		return "REQUEST_TB", true
	case matchengine.RefereeRequestTB:
		return "REFEREE_REQUEST_TB", true
	case matchengine.RespondTBRequest:
		return "RESPOND_TB_REQUEST", true
	case matchengine.RefereeRespondTBRequest:
		return "REFEREE_RESPOND_TB_REQUEST", true
	case matchengine.StartTB:
		return "START_TB", true
	case matchengine.ConfirmTBResult:
		return "CONFIRM_TB_RESULT", true
	case matchengine.RecordSurrender:
		return "RECORD_SURRENDER", true
	case matchengine.ConfirmBeatmapResult:
		return "CONFIRM_BEATMAP_RESULT", true
	default:
		return "", false
	}
}
