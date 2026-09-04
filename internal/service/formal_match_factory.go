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
// pure MatchEngine model. All three room types (match, casual, private) flow
// through this single bootstrap boundary so that every room produces a
// code-resolvable match backed by an authoritative MatchEngine snapshot.
//
// The engine requires exactly one Shiro slot and one TB slot in every
// configuration (matchengine/model.go: NewReadyState). The factory
// materialises those two slots directly:
//
//   - Shiro is always supplied by the factory as a placeholder slot
//     (ID="SHIRO"). Any SHIRO entries the mappool happens to declare are
//     ignored — Shiro carries no beatmap by design.
//   - The TB slot is sourced from the mappool's first TB entry when one is
//     linked. For casual and private rooms without a linked mappool, the
//     factory substitutes a synthetic TB slot (ID="TB") so the engine can
//     still start. Match rooms must link a mappool that has at least one
//     TB entry; that constraint is enforced by MissingStartRequirements
//     below.
//
// Per-room-type requirements are still enforced up front by
// MissingStartRequirements; StartMatch remains the formal command that
// authorizes this seed.
func BuildFormalMatchSeed(room domain.Room, redTeam, blueTeam *domain.Team, mappool *domain.Mappool, now time.Time) (FormalMatchSeed, error) {
	if room.ID == bson.NilObjectID {
		return FormalMatchSeed{}, fmt.Errorf("%w: a persisted room is required", errs.ErrInvalidInput)
	}
	switch room.Type {
	case domain.RoomTypeMatch, domain.RoomTypeCasual, domain.RoomTypePrivate:
	default:
		return FormalMatchSeed{}, fmt.Errorf("%w: unsupported room type %q", errs.ErrInvalidInput, room.Type)
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
	if room.Type == domain.RoomTypeMatch && mappool == nil {
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

	pool := domain.NewPool()
	if mappool != nil {
		pool = mappool.ToRuntime()
	}
	legacy := domain.NewMatch(room, redTeam.Snapshot(domain.TeamSideRed), blueTeam.Snapshot(domain.TeamSideBlue), pool)
	legacy.ID = bson.NewObjectID()
	legacy.BPOrder = domain.BPOrder{FirstPick: *room.Settings.FirstPick, FirstBan: *room.Settings.FirstBan}
	legacy.Status = domain.MatchStatusPending
	legacy.CreatedAt = now.UTC()
	legacy.UpdatedAt = now.UTC()

	return FormalMatchSeed{LegacyMatch: legacy, State: state}, nil
}

// engineConfigurationFromEntities builds the engine-side Configuration from
// the optional mappool and the two team entities. The returned PoolSlots
// always contain exactly one Shiro and exactly one TB slot:
//
//   - Shiro is added unconditionally as ID="SHIRO"; any Shiro entries the
//     mappool declares are dropped (Shiro is a placeholder and must not
//     bind to a beatmap).
//   - TB comes from the first TB entry in the linked mappool, or — for
//     casual/private rooms without a mappool — a synthetic ID="TB" slot
//     with no beatmap. Match rooms that link a mappool without a TB entry
//     fail MissingStartRequirements upstream and never reach this branch.
func engineConfigurationFromEntities(mappool *domain.Mappool, redTeam, blueTeam *domain.Team, firstPick, firstBan *domain.TeamSide) (matchengine.Configuration, error) {
	modOrder := []domain.PieceMod{
		domain.PieceModNM, domain.PieceModHD, domain.PieceModHR, domain.PieceModDT,
		domain.PieceModFM,
	}
	poolSlots := make([]matchengine.PoolSlot, 0)
	if mappool != nil {
		runtimePool := mappool.ToRuntime()
		// Walk the canonical mod order so slot IDs are deterministic. SHIRO
		// entries are intentionally skipped: the factory always installs a
		// synthetic SHIRO slot below.
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
	}

	// Synthetic Shiro: a placeholder mod the engine uses for the white
	// neutral piece. It carries no beatmap reference; it is intentionally
	// not derived from any mappool entry.
	poolSlots = append(poolSlots, matchengine.PoolSlot{ID: "SHIRO", Mod: matchengine.ModShiro})

	// TB: prefer the first TB entry from the linked mappool. If no mappool
	// is linked (casual/private rooms without a beatmap), substitute a
	// synthetic TB slot so the engine can still start.
	tbSlot := matchengine.PoolSlot{ID: "TB", Mod: matchengine.ModTB}
	if mappool != nil {
		for _, entry := range mappool.SortedEntries() {
			if entry.Mod == domain.PieceModTB {
				tbSlot.ID = domain.PoolSlot{Mod: domain.PieceModTB, Index: entry.Index}.String()
				break
			}
		}
	}
	poolSlots = append(poolSlots, tbSlot)

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
		// Casual and private rooms (no mappool) reuse the same standard timer
		// configuration; the engine is happy to start with zero pool slots.
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
