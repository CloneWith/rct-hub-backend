package service

import (
	"fmt"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/pkg/errs"
)

// FormalMatchSeed contains the legacy read-model shell and the authoritative
// READY aggregate that must be created atomically for a tournament room.
type FormalMatchSeed struct {
	LegacyMatch domain.Match
	State       matchengine.State
}

// BuildFormalMatchSeed maps organizer-confirmed room configuration into the
// pure MatchEngine model. It does not start play; StartMatch remains a formal
// command handled by the M4 orchestrator.
func BuildFormalMatchSeed(room domain.Room, now time.Time) (FormalMatchSeed, error) {
	if room.ID == bson.NilObjectID || room.Type != domain.RoomTypeMatch {
		return FormalMatchSeed{}, fmt.Errorf("%w: a persisted tournament room is required", errs.ErrInvalidInput)
	}
	if now.IsZero() {
		return FormalMatchSeed{}, fmt.Errorf("%w: creation timestamp is required", errs.ErrInvalidInput)
	}
	if missing := room.Settings.MissingStartRequirements(room.Type); len(missing) > 0 {
		fields := make([]errs.FieldError, 0, len(missing))
		for _, m := range missing {
			fields = append(fields, errs.FieldError{
				Field:   m,
				Rule:    "required",
				Message: fmt.Sprintf("%s is required before starting the match", m),
			})
		}
		return FormalMatchSeed{}, errs.NewValidationError(fields...)
	}

	configuration, err := engineConfigurationFromRoom(room)
	if err != nil {
		return FormalMatchSeed{}, err
	}
	state, err := matchengine.NewReadyState(configuration)
	if err != nil {
		return FormalMatchSeed{}, fmt.Errorf("%w: invalid MatchEngine configuration: %v", errs.ErrInvalidInput, err)
	}

	// RoomSettings has no team presentation fields. These defaults only fill
	// the temporary legacy read-model shell; they do not configure the engine.
	redTeam := domain.TeamSnapshot{
		ID:           bson.NewObjectID(),
		Side:         domain.TeamSideRed,
		Name:         "Red",
		Color:        "#ef4444",
		LeaderID:     *room.Settings.RedLeader,
		StrategistID: *room.Settings.RedStrategistUserID,
		Players:      append([]int64(nil), room.Settings.RedPlayers...),
	}
	blueTeam := domain.TeamSnapshot{
		ID:           bson.NewObjectID(),
		Side:         domain.TeamSideBlue,
		Name:         "Blue",
		Color:        "#3b82f6",
		LeaderID:     *room.Settings.BlueLeader,
		StrategistID: *room.Settings.BlueStrategistUserID,
		Players:      append([]int64(nil), room.Settings.BluePlayers...),
	}
	legacy := domain.NewMatch(room, redTeam, blueTeam)
	legacy.ID = bson.NewObjectID()
	legacy.Code = room.Code
	legacy.BPOrder = domain.BPOrder{FirstPick: *room.Settings.FirstPick, FirstBan: *room.Settings.FirstBan}
	legacy.Status = domain.MatchStatusPending
	legacy.CreatedAt = now.UTC()
	legacy.UpdatedAt = now.UTC()

	return FormalMatchSeed{LegacyMatch: legacy, State: state}, nil
}

func engineConfigurationFromRoom(room domain.Room) (matchengine.Configuration, error) {
	poolSlots := make([]matchengine.PoolSlot, 0)
	mods := make([]domain.PieceMod, 0, len(room.Settings.Mappool.Slots))
	for mod := range room.Settings.Mappool.Slots {
		mods = append(mods, mod)
	}
	slices.Sort(mods)
	for _, mod := range mods {
		engineMod, ok := engineModFromDomain(mod)
		if !ok {
			return matchengine.Configuration{}, fmt.Errorf("%w: unsupported pool mod %q", errs.ErrInvalidInput, mod)
		}
		for index, piece := range room.Settings.Mappool.Slots[mod] {
			if piece.IsRemoved() {
				continue
			}
			if piece.State != "" && piece.State != domain.PieceStateNormal {
				return matchengine.Configuration{}, fmt.Errorf(
					"%w: pool slot %s is already in state %q",
					errs.ErrInvalidInput,
					domain.PoolSlot{Mod: mod, Index: index + 1}.String(),
					piece.State,
				)
			}
			poolSlots = append(poolSlots, matchengine.PoolSlot{
				ID:  domain.PoolSlot{Mod: mod, Index: index + 1}.String(),
				Mod: engineMod,
			})
		}
	}

	return matchengine.Configuration{
		FirstBan:  engineTeamFromDomain(*room.Settings.FirstBan),
		FirstPick: engineTeamFromDomain(*room.Settings.FirstPick),
		PoolSlots: poolSlots,
		Rosters: map[matchengine.TeamSide]matchengine.Roster{
			matchengine.TeamRed: {
				LeaderID:  *room.Settings.RedLeader,
				PlayerIDs: append([]int64(nil), room.Settings.RedPlayers...),
			},
			matchengine.TeamBlue: {
				LeaderID:  *room.Settings.BlueLeader,
				PlayerIDs: append([]int64(nil), room.Settings.BluePlayers...),
			},
		},
		// Formal rooms currently expose no timer-preset setting, so the
		// organizer-confirmed RCTS1 preset is frozen into the new aggregate.
		Timers: matchengine.StandardTimerConfiguration(),
	}, nil
}

func engineTeamFromDomain(side domain.TeamSide) matchengine.TeamSide {
	if side == domain.TeamSideRed {
		return matchengine.TeamRed
	}
	if side == domain.TeamSideBlue {
		return matchengine.TeamBlue
	}
	return matchengine.TeamSide(side)
}

func engineModFromDomain(mod domain.PieceMod) (matchengine.Mod, bool) {
	switch mod {
	case domain.PieceModNM:
		return matchengine.ModNM, true
	case domain.PieceModHD:
		return matchengine.ModHD, true
	case domain.PieceModHR:
		return matchengine.ModHR, true
	case domain.PieceModDT:
		return matchengine.ModDT, true
	case domain.PieceModFM:
		return matchengine.ModFM, true
	case domain.PieceModShiro:
		return matchengine.ModShiro, true
	case domain.PieceModTB:
		return matchengine.ModTB, true
	default:
		return "", false
	}
}
