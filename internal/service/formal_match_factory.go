package service

import (
	"fmt"
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
// pure MatchEngine model. Team rosters and the pool come from the Team /
// Mappool entities linked by the room settings. It does not start play;
// StartMatch remains a formal command handled by the M4 orchestrator.
func BuildFormalMatchSeed(room domain.Room, redTeam, blueTeam *domain.Team, mappool *domain.Mappool, now time.Time) (FormalMatchSeed, error) {
	if room.ID == bson.NilObjectID || room.Type != domain.RoomTypeMatch {
		return FormalMatchSeed{}, fmt.Errorf("%w: a persisted tournament room is required", errs.ErrInvalidInput)
	}
	if now.IsZero() {
		return FormalMatchSeed{}, fmt.Errorf("%w: creation timestamp is required", errs.ErrInvalidInput)
	}
	if missing := MissingStartRequirements(room, redTeam, blueTeam, mappool); len(missing) > 0 {
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
	if mappool == nil {
		return FormalMatchSeed{}, errs.NewValidationError(
			errs.FieldError{Field: "settings.mappool_id", Rule: "required", Message: "a mappool must be linked before starting the match"},
		)
	}

	configuration, err := engineConfigurationFromEntities(mappool, redTeam, blueTeam, room.Settings.FirstPick, room.Settings.FirstBan)
	if err != nil {
		return FormalMatchSeed{}, err
	}
	state, err := matchengine.NewReadyState(configuration)
	if err != nil {
		return FormalMatchSeed{}, fmt.Errorf("%w: invalid MatchEngine configuration: %v", errs.ErrInvalidInput, err)
	}

	legacy := domain.NewMatch(room, redTeam.Snapshot(domain.TeamSideRed), blueTeam.Snapshot(domain.TeamSideBlue), mappool.ToRuntime())
	legacy.ID = bson.NewObjectID()
	legacy.Code = room.Code
	legacy.BPOrder = domain.BPOrder{FirstPick: *room.Settings.FirstPick, FirstBan: *room.Settings.FirstBan}
	legacy.Status = domain.MatchStatusPending
	legacy.CreatedAt = now.UTC()
	legacy.UpdatedAt = now.UTC()

	return FormalMatchSeed{LegacyMatch: legacy, State: state}, nil
}

func engineConfigurationFromEntities(mappool *domain.Mappool, redTeam, blueTeam *domain.Team, firstPick, firstBan *domain.TeamSide) (matchengine.Configuration, error) {
	runtimePool := mappool.ToRuntime()
	poolSlots := make([]matchengine.PoolSlot, 0, len(mappool.Entries))
	// Canonical mod order: NM, HD, HR, DT, FM, Shiro, TB. Within a group the
	// runtime pieces are ordered by entry index, so the slot ID matches the
	// entity's per-mod numbering. Entity-derived pools are always fresh
	// (pieces start NORMAL), unlike the legacy inline pool.
	modOrder := []domain.PieceMod{
		domain.PieceModNM, domain.PieceModHD, domain.PieceModHR, domain.PieceModDT,
		domain.PieceModFM, domain.PieceModShiro, domain.PieceModTB,
	}
	for _, mod := range modOrder {
		pieces := runtimePool.Slots[mod]
		if len(pieces) == 0 {
			continue
		}
		engineMod, ok := engineModFromDomain(mod)
		if !ok {
			return matchengine.Configuration{}, fmt.Errorf("%w: unsupported pool mod %q", errs.ErrInvalidInput, mod)
		}
		for index := range pieces {
			poolSlots = append(poolSlots, matchengine.PoolSlot{
				ID:  domain.PoolSlot{Mod: mod, Index: index + 1}.String(),
				Mod: engineMod,
			})
		}
	}

	return matchengine.Configuration{
		FirstBan:  engineTeamFromDomain(*firstBan),
		FirstPick: engineTeamFromDomain(*firstPick),
		PoolSlots: poolSlots,
		Rosters: map[matchengine.TeamSide]matchengine.Roster{
			matchengine.TeamRed: {
				LeaderID:  domain.DerefInt64(redTeam.LeaderID, 0),
				PlayerIDs: append([]int64(nil), redTeam.Players...),
			},
			matchengine.TeamBlue: {
				LeaderID:  domain.DerefInt64(blueTeam.LeaderID, 0),
				PlayerIDs: append([]int64(nil), blueTeam.Players...),
			},
		},
		// Formal rooms currently expose no timer-preset setting, so the
		// organizer-confirmed RCTS1 preset is frozen into the new aggregate.
		Timers: matchengine.StandardTimerConfiguration(),
	}, nil
}

// engineTeamFromDomain converts a domain side ("red"/"blue") into the
// matchengine side ("RED"/"BLUE").
func engineTeamFromDomain(side domain.TeamSide) matchengine.TeamSide {
	if side == domain.TeamSideBlue {
		return matchengine.TeamBlue
	}
	return matchengine.TeamRed
}

// engineModFromDomain converts a domain pool mod into the matchengine mod.
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
