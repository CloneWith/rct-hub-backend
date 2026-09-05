package graphql

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/errs"
)

func (r *Resolver) loadFormalMatch(ctx context.Context, id string) (*service.FormalMatch, error) {
	if r == nil || r.formal == nil {
		return nil, fmt.Errorf("formal match read service is unavailable")
	}
	matchID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid match ID: %w", err)
	}
	return r.formal.ByID(ctx, matchID)
}

// privateMatchContext loads the authenticated user and the room referenced
// by a match and enforces that the room's MatchID back-pointer identifies
// the same match we are about to authorize. The room type is intentionally
// NOT constrained here: BuildFormalMatchSeed (service/formal_match_factory.go)
// creates formal matches for casual and private rooms too, and the per-role
// authorization predicates below already gate the actual data exposure.
//
// The previous "room.Type == RoomTypeMatch" clause was a leftover from when
// only match rooms could host formal matches; that invariant no longer holds
// and the check caused `matchByCode(...).strategistView` (and the captain /
// referee views) to fail with "formal match room relationship is invalid"
// for casual rooms that had legitimately auto-started a match via
// MarkStrategistReady.
func (r *Resolver) privateMatchContext(ctx context.Context, id, roomID string) (*domain.Room, *domain.User, error) {
	user, err := r.privateViewer(ctx)
	if err != nil {
		return nil, nil, err
	}
	if r.rooms == nil {
		return nil, nil, fmt.Errorf("private match services are unavailable")
	}
	matchID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid match ID: %w", err)
	}
	parsedRoomID, err := bson.ObjectIDFromHex(roomID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid room ID: %w", err)
	}
	room, err := r.rooms.GetRoom(ctx, parsedRoomID)
	if err != nil {
		return nil, nil, err
	}
	if room.ID != parsedRoomID || room.MatchID == nil || *room.MatchID != matchID {
		return nil, nil, fmt.Errorf("formal match room relationship is invalid")
	}
	return room, user, nil
}

// privateViewer loads and validates the authenticated local user for private
// GraphQL reads and match views. The user record is checked on every request
// so revocations take effect without relying on stale JWT claims.
func (r *Resolver) privateViewer(ctx context.Context) (*domain.User, error) {
	claims, ok := ClaimsFromCtx(ctx)
	if !ok || claims == nil {
		return nil, fmt.Errorf("AUTH_REQUIRED: authentication required")
	}
	if r == nil || r.users == nil {
		return nil, fmt.Errorf("private viewer service is unavailable")
	}
	user, err := r.users.GetByOsuID(ctx, claims.OsuID)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return nil, fmt.Errorf("AUTH_REQUIRED: current user is unavailable")
		}
		return nil, fmt.Errorf("load current user: %w", err)
	}
	if err := validatePrivateViewer(user); err != nil {
		return nil, err
	}
	return user, nil
}

func validatePrivateViewer(user *domain.User) error {
	if user == nil || user.IsBanned || user.VerifyStatus != domain.Verified {
		return fmt.Errorf("ACTION_NOT_ALLOWED: current user is banned or unverified")
	}
	return nil
}

// adminViewer extends privateViewer with the admin role requirement. It gates
// the admin-panel GraphQL queries and mutations (teams / mappools management,
// userByOsuId). Like privateViewer it re-reads the user record on every
// request so role revocations take effect immediately.
func (r *Resolver) adminViewer(ctx context.Context) (*domain.User, error) {
	user, err := r.privateViewer(ctx)
	if err != nil {
		return nil, err
	}
	if !user.HasRole(domain.RoleAdmin) {
		return nil, fmt.Errorf("GLOBAL_ROLE_REQUIRED: admin role is required")
	}
	return user, nil
}

// engineSideFromDomain converts a domain side ("red"/"blue") into the
// matchengine side ("RED"/"BLUE").
func engineSideFromDomain(side domain.TeamSide) matchengine.TeamSide {
	if side == domain.TeamSideBlue {
		return matchengine.TeamBlue
	}
	return matchengine.TeamRed
}

// strategistViewerTeam returns the team side the viewer is acting as for a
// strategist-shaped read model. The check orders assignment *first*, because
// a user whose OnlineID is the actual StrategistID of one of the room's
// teams has full authority to read that side's view — even if the role
// table has drifted (e.g. admin-managed role lists, freshly-imported
// accounts) or the user only holds admin. Once assignment passes we hand
// the side straight to computeStrategistView.
//
// When the user is *not* the assigned strategist, we still want a useful
// error: admins get a dedicated message pointing at refereeView (which
// already accepts the admin role via authorizeRefereeViewer's admin
// override); users with the strategist role but no assignment learn that
// they are not assigned to *this* match; everyone else is told the role
// is required.
func strategistViewerTeam(user *domain.User, room *domain.Room, redTeam, blueTeam *domain.Team) (matchengine.TeamSide, error) {
	if user == nil || room == nil {
		return "", fmt.Errorf("ACTION_NOT_ALLOWED: user is required")
	}
	if side, ok := domain.StrategistSide(redTeam, blueTeam, user.OnlineID); ok {
		return engineSideFromDomain(side), nil
	}
	if user.HasRole(domain.RoleAdmin) {
		return "", fmt.Errorf("ACTION_NOT_ALLOWED: admin should read matchByCode.refereeView instead of strategistView")
	}
	if user.HasRole(domain.RoleStrategist) {
		return "", fmt.Errorf("ACTION_NOT_ALLOWED: user is not uniquely assigned to this match")
	}
	return "", fmt.Errorf("ACTION_NOT_ALLOWED: current strategist role is required")
}

// captainViewerTeam mirrors strategistViewerTeam for the captain read model:
// assignment to the leader of exactly one team wins over any role check.
// Admins fall through to a dedicated message pointing at refereeView;
// everyone else (whether a captain of some other match or simply not the
// leader of either room team) gets the standard "not a captain" message.
//
// Captain has no RBAC role of its own (the team LeaderID is the source of
// truth), so there is no separate "has role but not assigned" branch.
func captainViewerTeam(user *domain.User, room *domain.Room, redTeam, blueTeam *domain.Team) (matchengine.TeamSide, error) {
	if user == nil || room == nil {
		return "", fmt.Errorf("ACTION_NOT_ALLOWED: user is required")
	}
	if side, ok := domain.CaptainSide(redTeam, blueTeam, user.OnlineID); ok {
		return engineSideFromDomain(side), nil
	}
	if user.HasRole(domain.RoleAdmin) {
		return "", fmt.Errorf("ACTION_NOT_ALLOWED: admin should read matchByCode.refereeView instead of captainView")
	}
	return "", fmt.Errorf("ACTION_NOT_ALLOWED: user is not a captain for this match")
}

// authorizeRefereeViewer gates the referee-shaped read model. Admins always
// pass (they already drive referee commands through the orchestrator's admin
// override) and the assigned referee of the room passes via the room's
// RefereeUserID back-pointer. Anyone else is rejected with a structured
// error code the GraphQL layer maps to ACTION_NOT_ALLOWED.
func authorizeRefereeViewer(user *domain.User, room *domain.Room) error {
	if user == nil || room == nil {
		return fmt.Errorf("ACTION_NOT_ALLOWED: user is not the assigned referee for this match")
	}
	if user.HasRole(domain.RoleAdmin) {
		return nil
	}
	if user.HasRole(domain.RoleReferee) && room.RefereeUserID != nil && *room.RefereeUserID == user.OnlineID {
		return nil
	}
	return fmt.Errorf("ACTION_NOT_ALLOWED: user is not the assigned referee for this match")
}
