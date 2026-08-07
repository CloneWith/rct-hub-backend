package graphql

import (
	"sort"
	"strconv"
	"time"

	"rctHubBackend/internal/matchengine"
	"rctHubBackend/internal/service"
)

func mapFormalMatch(value *service.FormalMatch) *Match {
	if value == nil {
		return nil
	}
	state := value.State.Clone()
	pool := make([]*MatchPoolSlotMetadata, 0, len(state.PoolSlots))
	for id := range state.PoolSlots {
		pool = append(pool, &MatchPoolSlotMetadata{PoolSlotID: id, BeatmapID: int64PtrToStringPtr(value.Pool[id])})
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].PoolSlotID < pool[j].PoolSlotID })
	return &Match{
		ID: value.ID.Hex(), Code: value.Code, Name: value.Name,
		RoomType: mapRoomType(value.RoomType), RoomID: value.RoomID.Hex(),
		CreatedAt: value.CreatedAt, Pool: pool,
		Snapshot: mapMatchSnapshot(state), State: state,
	}
}

func mapMatchSnapshot(state matchengine.State) *MatchSnapshot {
	analysis := matchengine.Analyze(state)
	slots := make([]*FormalPoolSlot, 0, len(state.PoolSlots))
	for _, slot := range state.PoolSlots {
		slots = append(slots, &FormalPoolSlot{ID: slot.ID, Mod: PieceMod(slot.Mod), State: PoolSlotState(slot.State)})
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].ID < slots[j].ID })

	active := gqlTeamPtr(state.ActiveTeam)
	pendingPieceID := optionalString(state.PendingPieceID)
	result := &MatchSnapshot{
		Version: strconv.FormatUint(state.Version, 10), Lifecycle: MatchLifecycle(state.Lifecycle),
		Phase: FormalMatchPhase(state.Phase), FirstBan: gqlTeam(state.FirstBan), FirstPick: gqlTeam(state.FirstPick),
		Turn: state.Turn, ActiveTeam: active, PoolSlots: slots, Board: mapFormalBoard(state.Board),
		WonCounts:      &TeamCounts{Red: analysis.WonCounts[matchengine.TeamRed], Blue: analysis.WonCounts[matchengine.TeamBlue]},
		Timer:          mapFormalTimer(state.Timer),
		RobberyUsed:    &TeamFlags{Red: state.RobberyUsed[matchengine.TeamRed], Blue: state.RobberyUsed[matchengine.TeamBlue]},
		TeamPauseUsed:  &TeamFlags{Red: state.TeamPauseUsed[matchengine.TeamRed], Blue: state.TeamPauseUsed[matchengine.TeamBlue]},
		Rosters:        &FormalRosters{Red: mapFormalRoster(state.Rosters[matchengine.TeamRed]), Blue: mapFormalRoster(state.Rosters[matchengine.TeamBlue])},
		PendingPieceID: pendingPieceID, Winner: gqlTeamPtrValue(state.Winner),
	}
	if state.PendingTBRequest != nil {
		result.PendingTBRequest = &PendingTBRequest{ID: state.PendingTBRequest.ID, RequestedBy: gqlTeam(state.PendingTBRequest.RequestedBy), Basis: TBBasis(state.PendingTBRequest.Basis)}
	}
	if state.TBEntry != nil {
		result.TbEntry = &TBEntry{Basis: TBBasis(state.TBEntry.Basis), RequestID: optionalString(state.TBEntry.RequestID), RequestedBy: gqlTeamPtr(state.TBEntry.RequestedBy)}
	}
	if state.Result != nil {
		playerIDs := make([]string, len(state.Result.ConfirmingPlayerIDs))
		for i, id := range state.Result.ConfirmingPlayerIDs {
			playerIDs[i] = strconv.FormatInt(id, 10)
		}
		result.Result = &FormalMatchResult{
			Winner: gqlTeam(state.Result.Winner), Reason: MatchResultReason(state.Result.Reason),
			SurrenderingTeam: gqlTeamPtrValue(state.Result.SurrenderingTeam), ConfirmingPlayerIDs: playerIDs,
			WonCounts: &TeamCounts{Red: state.Result.RedWonCount, Blue: state.Result.BlueWonCount},
		}
	}
	if state.Stalemate != nil {
		result.Stalemate = &StalemateEvidence{WonCounts: &TeamCounts{Red: state.Stalemate.RedWonCount, Blue: state.Stalemate.BlueWonCount}}
	}
	return result
}

func mapFormalBoard(board matchengine.Board) *FormalBoard {
	pieces := board.Pieces()
	cells := make([]*FormalBoardCell, 0, 16)
	for row := 0; row < 4; row++ {
		for col := 0; col < 4; col++ {
			cellID := matchengine.Cell(string([]byte{byte('A' + col), byte('1' + row)}))
			zone, _ := board.ZoneAt(cellID)
			cell := &FormalBoardCell{Cell: string(cellID), Row: row, Col: col, Zone: FormalBoardZone(zone)}
			if piece, exists := pieces[cellID]; exists {
				cell.Piece = &FormalBoardPiece{
					ID: piece.ID, SourcePoolSlotID: piece.SourcePoolSlotID, Mod: PieceMod(piece.Mod),
					ForceMod: gqlForceMod(piece.ForceMod), SelectedBy: gqlTeam(piece.SelectedBy),
					Owner: gqlTeamPtrValue(piece.Owner), Outcome: BoardPieceOutcome(piece.Outcome),
				}
			}
			cells = append(cells, cell)
		}
	}
	return &FormalBoard{Cells: cells}
}

func mapFormalTimer(timer matchengine.Timer) *FormalTimer {
	result := &FormalTimer{DurationMilliseconds: durationToMilliseconds(timer.Duration), Paused: timer.Paused}
	if !timer.StartedAt.IsZero() {
		started := timer.StartedAt.UTC()
		result.StartedAt = &started
	}
	if timer.Paused {
		remaining := durationToMilliseconds(timer.RemainingAtPause)
		result.RemainingAtPauseMilliseconds = &remaining
	}
	return result
}

func mapFormalRoster(roster matchengine.Roster) *FormalRoster {
	players := make([]string, len(roster.PlayerIDs))
	for index, id := range roster.PlayerIDs {
		players[index] = strconv.FormatInt(id, 10)
	}
	return &FormalRoster{LeaderID: strconv.FormatInt(roster.LeaderID, 10), PlayerIDs: players}
}

func mapActorAnalysis(analysis matchengine.ActorAnalysis) *MatchActorAnalysis {
	actions := make([]MatchAction, len(analysis.AllowedActions))
	for index, action := range analysis.AllowedActions {
		actions[index] = MatchAction(action)
	}
	placements := make([]*LegalPlacement, len(analysis.LegalPlacements))
	for index, placement := range analysis.LegalPlacements {
		placements[index] = &LegalPlacement{PoolSlotID: placement.PoolSlotID, Cell: string(placement.Cell), ForceMod: gqlForceMod(placement.ForceMod)}
	}
	shiroCells := make([]string, len(analysis.ShiroCells))
	for index, cell := range analysis.ShiroCells {
		shiroCells[index] = string(cell)
	}
	plans := make([]*RobberyPlan, len(analysis.RobberyPlans))
	for index, plan := range analysis.RobberyPlans {
		sets := make([][]string, len(plan.SacrificeSets))
		for setIndex := range plan.SacrificeSets {
			sets[setIndex] = append([]string(nil), plan.SacrificeSets[setIndex]...)
		}
		plans[index] = &RobberyPlan{TargetPieceID: plan.TargetPieceID, SacrificeSets: sets}
	}
	return &MatchActorAnalysis{
		AllowedActions: actions, BanPoolSlotIDs: append([]string(nil), analysis.BanPoolSlotIDs...),
		LegalPlacements: placements, ShiroCells: shiroCells, RobberyPlans: plans, PendingTBRequestID: optionalString(analysis.PendingTBRequestID),
		CanAcceptTBRequest: analysis.CanAcceptTBRequest, CanRejectTBRequest: analysis.CanRejectTBRequest,
		TbRequestTeams: mapEngineTeams(analysis.TBRequestTeams), TbResponseTeams: mapEngineTeams(analysis.TBResponseTeams),
	}
}

func mapEngineTeams(teams []matchengine.TeamSide) []TeamSide {
	result := make([]TeamSide, len(teams))
	for index, team := range teams {
		result[index] = gqlTeam(team)
	}
	return result
}

func gqlTeam(side matchengine.TeamSide) TeamSide { return TeamSide(side) }

func gqlTeamPtr(side matchengine.TeamSide) *TeamSide {
	if side != matchengine.TeamRed && side != matchengine.TeamBlue {
		return nil
	}
	value := gqlTeam(side)
	return &value
}

func gqlTeamPtrValue(side *matchengine.TeamSide) *TeamSide {
	if side == nil {
		return nil
	}
	return gqlTeamPtr(*side)
}

func gqlForceMod(value *matchengine.ForceMod) *ForceMod {
	if value == nil {
		return nil
	}
	result := ForceMod(*value)
	return &result
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func durationToMilliseconds(value time.Duration) int { return int(value / time.Millisecond) }
