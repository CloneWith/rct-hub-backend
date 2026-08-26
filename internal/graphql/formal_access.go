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
	if room.ID != parsedRoomID || room.Type != domain.RoomTypeMatch || room.MatchID == nil || *room.MatchID != matchID {
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

func strategistViewerTeam(user *domain.User, room *domain.Room) (matchengine.TeamSide, error) {
	if user == nil || room == nil || !user.HasRole(domain.RoleStrategist) {
		return "", fmt.Errorf("ACTION_NOT_ALLOWED: current strategist role is required")
	}
	red := room.Settings.RedStrategistUserID != nil && *room.Settings.RedStrategistUserID == user.OnlineID
	blue := room.Settings.BlueStrategistUserID != nil && *room.Settings.BlueStrategistUserID == user.OnlineID
	if red == blue {
		return "", fmt.Errorf("ACTION_NOT_ALLOWED: user is not uniquely assigned to this match")
	}
	if red {
		return matchengine.TeamRed, nil
	}
	return matchengine.TeamBlue, nil
}

func captainViewerTeam(user *domain.User, room *domain.Room) (matchengine.TeamSide, error) {
	if user == nil || room == nil {
		return "", fmt.Errorf("ACTION_NOT_ALLOWED: user is not a captain for this match")
	}
	red := room.Settings.RedLeader != nil && *room.Settings.RedLeader == user.OnlineID
	blue := room.Settings.BlueLeader != nil && *room.Settings.BlueLeader == user.OnlineID
	if red == blue {
		return "", fmt.Errorf("ACTION_NOT_ALLOWED: user is not uniquely assigned as captain")
	}
	if red {
		return matchengine.TeamRed, nil
	}
	return matchengine.TeamBlue, nil
}

func authorizeRefereeViewer(user *domain.User, room *domain.Room) error {
	if user == nil || room == nil {
		return fmt.Errorf("ACTION_NOT_ALLOWED: user is not the assigned referee for this match")
	}
	if user.HasRole(domain.RoleAdmin) || user.HasRole(domain.RoleReferee) && room.RefereeUserID != nil && *room.RefereeUserID == user.OnlineID {
		return nil
	}
	return fmt.Errorf("ACTION_NOT_ALLOWED: user is not the assigned referee for this match")
}
