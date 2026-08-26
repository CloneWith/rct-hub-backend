package service

import (
	"rctHubBackend/internal/domain"
)

// MissingStartRequirements returns the wire-format paths of the room
// configuration that still block starting a match of the given type. An empty
// result means the room can start. Callers use this to tell the client exactly
// what is missing.
//
// redTeam / blueTeam are the Team entities referenced by the room settings
// (nil when not linked); mappool is the linked Mappool entity. Team readiness
// (leader + strategist) applies to every room type (R1). Match rooms add the
// roster-size and MP-link requirements.
func MissingStartRequirements(room domain.Room, redTeam, blueTeam *domain.Team, mappool *domain.Mappool) []string {
	var missing []string
	require := func(field string, ok bool) {
		if !ok {
			missing = append(missing, field)
		}
	}
	teamReady := func(t *domain.Team) bool {
		return t != nil && t.IsReady()
	}
	playersAtLeast := func(t *domain.Team, n int) bool {
		return t != nil && len(t.Players) >= n
	}
	switch room.Type {
	case domain.RoomTypeCasual, domain.RoomTypeMatch:
		require("settings.red_team_id", teamReady(redTeam))
		require("settings.blue_team_id", teamReady(blueTeam))
		require("settings.first_pick", room.Settings.FirstPick != nil)
		require("settings.first_ban", room.Settings.FirstBan != nil)
		if room.Type == domain.RoomTypeMatch {
			require("settings.red_team_id", playersAtLeast(redTeam, 4))
			require("settings.blue_team_id", playersAtLeast(blueTeam, 4))
			require("settings.mappool_id", mappool != nil)
			require("settings.mp_link", room.Settings.MPLink != nil && *room.Settings.MPLink != "")
		}
	case domain.RoomTypePrivate:
		// No requirements.
	default:
		missing = append(missing, "type")
	}
	return missing
}
