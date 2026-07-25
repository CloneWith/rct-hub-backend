package graphql

import (
	"fmt"
	"strings"
	"time"
)

// ============================================================================
// 客户端视图计算 (Phase 2 — Read Model §9.10)
//
// 所有视图从 GraphQL *Match 类型计算，不重新查库。
// domain 逻辑在此层重新实现（简单判断），避免额外 DB 查询。
// ============================================================================

// --- StrategistView ---

// computeStrategistView 从 Match + JWT claims 计算策略师视图。
// claims 可能为 nil（@requireRole directive 已保证非 nil，但防御性检查）。
func computeStrategistView(match *Match, osuID int64) *StrategistView {
	view := &StrategistView{
		AllowedActions:       []string{},
		DisallowedActions:    []string{},
		SelectablePoolSlots:  []string{},
		SelectableBoardCells: []string{},
	}

	// 确定用户所属队伍
	myTeam := determineMyTeam(match, osuID)
	view.MyTeam = myTeam

	// isMyTurn: 用户是某队策略师且当前是该队回合
	view.IsMyTurn = myTeam != nil && match.ActiveTeam != nil && *match.ActiveTeam == *myTeam

	// allowedActions / disallowedActions
	view.AllowedActions, view.DisallowedActions = computeActions(match)

	// selectablePoolSlots: 可选的图池槽位 (state == NORMAL)
	view.SelectablePoolSlots = computeSelectableSlots(match)

	// selectableBoardCells: 可放置棋子的空格子
	view.SelectableBoardCells = computeSelectableCells(match)

	// timer
	view.Timer = computeTimerInfo(match)

	// robberyInProgress
	view.RobberyInProgress = isRobberyInProgress(match)

	return view
}

// determineMyTeam 通过比较 osuID 与两队 strategistID 确定用户所属队伍。
func determineMyTeam(match *Match, osuID int64) *TeamSide {
	if match == nil || match.Teams == nil {
		return nil
	}
	red := match.Teams.Red
	blue := match.Teams.Blue
	if red != nil && red.StrategistID != nil && *red.StrategistID == int(osuID) {
		t := TeamSideRed
		return &t
	}
	if blue != nil && blue.StrategistID != nil && *blue.StrategistID == int(osuID) {
		t := TeamSideBlue
		return &t
	}
	return nil
}

// computeActions 根据当前阶段计算允许/禁止的操作列表。
func computeActions(match *Match) (allowed, disallowed []string) {
	allActions := []string{"ban", "unban", "pick", "unpick", "rob", "unrob", "win", "unwin", "dead", "undead", "surrender"}

	var allowedSet []string
	phase := getMatchPhase(match)
	action := getTurnAction(match)

	switch phase {
	case MatchPhaseBan:
		allowedSet = []string{"ban", "unban"}
	case MatchPhasePick:
		if action == "rob" {
			allowedSet = []string{"rob", "pick"}
		} else {
			allowedSet = []string{"pick", "unpick"}
		}
	case MatchPhaseWin:
		allowedSet = []string{"win", "unwin"}
	case MatchPhaseTb:
		allowedSet = []string{"pick"}
	case MatchPhaseEnded:
		allowedSet = []string{}
	default: // setup, roll
		allowedSet = []string{}
	}

	allowed = allowedSet
	disallowed = diffStrings(allActions, allowedSet)
	return
}

// computeSelectableSlots 返回可选图池槽位列表，格式 "MOD-INDEX"。
func computeSelectableSlots(match *Match) []string {
	if match == nil || match.Pool == nil {
		return []string{}
	}
	var slots []string
	for _, group := range match.Pool.Slots {
		if group == nil {
			continue
		}
		modStr := group.Mod.String()
		for _, piece := range group.Pieces {
			if piece == nil {
				continue
			}
			// CanBeSelected: state != BANNED && != PICKED && != WON && != DEAD
			if piece.State == PieceStateNormal {
				slots = append(slots, fmt.Sprintf("%s-%d", modStr, piece.Index))
			}
		}
	}
	return slots
}

// computeSelectableCells 返回可放置棋子的空格子列表，格式 "row,col"。
// 逻辑: 对每个空格子，检查是否有任意可选 piece 可以放在此格子。
//   - free mod (NM/FM/Shiro/TB) → 任何空格子
//   - restricted mod (HD/HR/DT) → 格子 zone 必须匹配
func computeSelectableCells(match *Match) []string {
	if match == nil || match.Board == nil {
		return []string{}
	}

	// 收集可选 piece 的 mod 集合
	selectableMods := collectSelectableMods(match)
	if len(selectableMods) == 0 {
		return []string{}
	}

	hasFreeMod := false
	restrictedMods := map[BoardZone]bool{}
	for _, mod := range selectableMods {
		if isFreeMod(mod) {
			hasFreeMod = true
		} else {
			restrictedMods[BoardZone(mod.String())] = true
		}
	}

	var cells []string
	for _, row := range match.Board.Cells {
		for _, cell := range row {
			if cell == nil || cell.State != "empty" {
				continue
			}
			// free mod 可放任何空格子
			if hasFreeMod {
				cells = append(cells, fmt.Sprintf("%d,%d", cell.Position.Row, cell.Position.Col))
				continue
			}
			// restricted mod 需要匹配 zone
			if restrictedMods[cell.Zone] {
				cells = append(cells, fmt.Sprintf("%d,%d", cell.Position.Row, cell.Position.Col))
			}
		}
	}
	return cells
}

// collectSelectableMods 返回所有可选 piece 的 mod 集合（去重）。
func collectSelectableMods(match *Match) []PieceMod {
	if match == nil || match.Pool == nil {
		return nil
	}
	seen := map[PieceMod]bool{}
	var mods []PieceMod
	for _, group := range match.Pool.Slots {
		if group == nil {
			continue
		}
		for _, piece := range group.Pieces {
			if piece != nil && piece.State == PieceStateNormal && !seen[group.Mod] {
				seen[group.Mod] = true
				mods = append(mods, group.Mod)
			}
		}
	}
	return mods
}

// isFreeMod 判断 mod 是否为自由放置类型。
// domain.IsFreeMod 的等价实现: NM/FM/Shiro/TB 是 free, HD/HR/DT 是 restricted。
func isFreeMod(mod PieceMod) bool {
	switch mod {
	case PieceModHd, PieceModHr, PieceModDt:
		return false
	default:
		return true
	}
}

// computeTimerInfo 从 TimerState 计算 TimerInfo。
func computeTimerInfo(match *Match) *TimerInfo {
	if match == nil || match.Timer == nil {
		return &TimerInfo{
			StartedAt:        time.Time{},
			DurationSeconds:  0,
			RemainingSeconds: 0,
			IsPaused:         false,
		}
	}
	t := match.Timer
	startedAt := time.Now()
	if t.StartedAt != nil {
		startedAt = *t.StartedAt
	}

	duration := t.TimeLimit
	remaining := duration
	if !t.IsPaused && t.StartedAt != nil {
		elapsed := int(time.Since(*t.StartedAt).Seconds())
		remaining = duration - elapsed
		if t.BonusUsed {
			remaining += t.BonusTime
		}
	}
	if t.IsPaused && t.RemainingAtPause != nil {
		remaining = *t.RemainingAtPause
	}
	if remaining < 0 {
		remaining = 0
	}

	return &TimerInfo{
		StartedAt:        startedAt,
		DurationSeconds:  duration,
		RemainingSeconds: remaining,
		IsPaused:         t.IsPaused,
	}
}

// isRobberyInProgress 检查当前回合是否正在进行抢劫操作。
func isRobberyInProgress(match *Match) bool {
	return getTurnAction(match) == "rob"
}

// --- SpectatorView ---

// computeSpectatorView 从 Match 计算观众视图。
func computeSpectatorView(match *Match) *SpectatorView {
	view := &SpectatorView{
		Board:        computeBoardSummary(match),
		Scores:       computeTeamScores(match),
		CurrentPhase: getMatchPhase(match),
		ActiveTeam:   match.ActiveTeam,
	}
	if match.TurnState != nil {
		tn := match.TurnState.Counter
		view.TurnNumber = &tn
	}
	// RecentMoves 由 resolver 方法填充（需要 MoveService 调用）
	view.RecentMoves = []*Move{}
	return view
}

// computeBoardSummary 从 Match.Board 生成简化的棋盘摘要。
func computeBoardSummary(match *Match) *BoardSummary {
	if match == nil || match.Board == nil {
		return &BoardSummary{Cells: [][]*CellSummary{}}
	}
	cells := make([][]*CellSummary, len(match.Board.Cells))
	for y := range match.Board.Cells {
		cells[y] = make([]*CellSummary, len(match.Board.Cells[y]))
		for x := range match.Board.Cells[y] {
			cell := match.Board.Cells[y][x]
			if cell == nil {
				continue
			}
			summary := &CellSummary{
				Position: cell.Position,
			}
			if cell.State == "occupied" {
				summary.Piece = cellToPieceSummary(cell)
			}
			cells[y][x] = summary
		}
	}
	return &BoardSummary{Cells: cells}
}

// cellToPieceSummary 从 BoardCell 提取棋子摘要信息。
// 需要从 pieceID (如 "NM-1") 解析 mod，从 teamID 解析 owner。
func cellToPieceSummary(cell *BoardCell) *PieceSummary {
	if cell == nil || cell.PieceID == nil {
		return nil
	}
	mod := parseModFromPieceID(*cell.PieceID)
	var owner *TeamSide
	if cell.TeamID != nil {
		t := TeamSide(*cell.TeamID)
		owner = &t
	}
	return &PieceSummary{
		Mod:   mod,
		State: PieceStatePicked, // 棋盘上的格子状态为 occupied → piece state = picked
		Owner: owner,
	}
}

// parseModFromPieceID 从 "NM-1" 格式的 pieceID 提取 PieceMod。
func parseModFromPieceID(pieceID string) PieceMod {
	parts := strings.SplitN(pieceID, "-", 2)
	if len(parts) == 0 {
		return PieceModNm
	}
	return PieceMod(strings.ToUpper(parts[0]))
}

// computeTeamScores 统计每队已获胜的棋子数。
func computeTeamScores(match *Match) *TeamScores {
	scores := &TeamScores{Red: 0, Blue: 0}
	if match == nil || match.Board == nil {
		return scores
	}
	for _, row := range match.Board.Cells {
		for _, cell := range row {
			if cell == nil || cell.State != "occupied" || cell.TeamID == nil {
				continue
			}
			switch TeamSide(*cell.TeamID) {
			case TeamSideRed:
				scores.Red++
			case TeamSideBlue:
				scores.Blue++
			}
		}
	}
	return scores
}

// --- OverlayView ---

// computeOverlayView 从 Match 计算 OBS Overlay 视图。
func computeOverlayView(match *Match) *OverlayView {
	return &OverlayView{
		Board:     computeBoardRenderData(match),
		Scores:    computeTeamScores(match),
		Timer:     computeTimerDisplay(match),
		LastEvent: nil, // WebSocket 事件系统尚未实现
	}
}

// computeBoardRenderData 从 Match.Board 生成渲染数据。
func computeBoardRenderData(match *Match) *BoardRenderData {
	if match == nil || match.Board == nil {
		return &BoardRenderData{Cells: [][]*CellRender{}, LastChangedCells: []*Position{}}
	}
	cells := make([][]*CellRender, len(match.Board.Cells))
	for y := range match.Board.Cells {
		cells[y] = make([]*CellRender, len(match.Board.Cells[y]))
		for x := range match.Board.Cells[y] {
			cell := match.Board.Cells[y][x]
			if cell == nil {
				continue
			}
			render := &CellRender{
				Position: cell.Position,
				Zone:     cell.Zone,
			}
			if cell.State == "occupied" {
				render.Piece = cellToPieceRender(cell)
			}
			cells[y][x] = render
		}
	}
	return &BoardRenderData{
		Cells:            cells,
		LastChangedCells: []*Position{}, // 变更追踪尚未实现
	}
}

// cellToPieceRender 从 BoardCell 提取渲染用棋子信息。
func cellToPieceRender(cell *BoardCell) *PieceRender {
	if cell == nil || cell.PieceID == nil {
		return nil
	}
	mod := parseModFromPieceID(*cell.PieceID)
	var owner *TeamSide
	if cell.TeamID != nil {
		t := TeamSide(*cell.TeamID)
		owner = &t
	}
	return &PieceRender{
		Mod:          mod,
		State:        PieceStatePicked,
		Owner:        owner,
		BeatmapCover: nil, // 需要 beatmap 查询，Phase 2 暂不填充
	}
}

// computeTimerDisplay 从 TimerState 生成显示用计时器信息。
func computeTimerDisplay(match *Match) *TimerDisplay {
	if match == nil || match.Timer == nil {
		return &TimerDisplay{RemainingSeconds: 0, IsPaused: false, IsWarning: false}
	}
	t := match.Timer

	remaining := t.TimeLimit
	if !t.IsPaused && t.StartedAt != nil {
		elapsed := int(time.Since(*t.StartedAt).Seconds())
		remaining = t.TimeLimit - elapsed
		if t.BonusUsed {
			remaining += t.BonusTime
		}
	}
	if t.IsPaused && t.RemainingAtPause != nil {
		remaining = *t.RemainingAtPause
	}
	if remaining < 0 {
		remaining = 0
	}

	return &TimerDisplay{
		RemainingSeconds: remaining,
		IsPaused:         t.IsPaused,
		IsWarning:        remaining > 0 && remaining <= 10,
	}
}

// --- RefereeView ---

// computeRefereeView 从 Match 构建裁判视图（完整数据引用）。
func computeRefereeView(match *Match) *RefereeView {
	return &RefereeView{
		Board:            match.Board,
		Pool:             match.Pool,
		Teams:            match.Teams,
		TurnState:        match.TurnState,
		Timer:            match.Timer,
		AuditLog:         []*AuditEntry{},       // 审计系统尚未实现
		ConnectionStatus: []*ClientConnection{}, // WebSocket 连接追踪尚未实现
	}
}

// --- 辅助函数 ---

// getMatchPhase 安全获取 MatchPhase。
func getMatchPhase(match *Match) MatchPhase {
	if match == nil || match.Phase == nil {
		return MatchPhaseSetup
	}
	return *match.Phase
}

// getTurnAction 安全获取 TurnState.Action。
func getTurnAction(match *Match) string {
	if match == nil || match.TurnState == nil || match.TurnState.Action == nil {
		return ""
	}
	return strings.ToLower(*match.TurnState.Action)
}

// diffStrings 返回 all 中存在但 exclude 中不存在的元素。
func diffStrings(all, exclude []string) []string {
	excludeSet := map[string]bool{}
	for _, s := range exclude {
		excludeSet[s] = true
	}
	var result []string
	for _, s := range all {
		if !excludeSet[s] {
			result = append(result, s)
		}
	}
	return result
}
