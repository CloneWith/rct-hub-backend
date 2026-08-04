package graphql

import (
	"strings"
	"time"

	"rctHubBackend/internal/domain"
	"rctHubBackend/pkg/paginate"
)

// ============================================================================
// 类型映射辅助函数
// domain → GraphQL 生成类型
// 枚举转换: domain 使用小写/混合大小写, GraphQL 使用大写
// ============================================================================

// --- 辅助函数 ---

func ptrInt(v int64) *int {
	i := int(v)
	return &i
}

func ptrFloat64(v float32) *float64 {
	f := float64(v)
	return &f
}

func ptrStr(v string) *string {
	return &v
}

func upperEnum(s string) string {
	return strings.ToUpper(s)
}

func durationSeconds(d time.Duration) *int {
	s := int(d.Seconds())
	return &s
}

// --- 枚举映射 ---

func mapUserRole(r domain.UserRole) UserRole {
	return UserRole(upperEnum(string(r)))
}

func mapRoles(roles []domain.UserRole) []UserRole {
	result := make([]UserRole, len(roles))
	for i, r := range roles {
		result[i] = mapUserRole(r)
	}
	return result
}

func mapVerifyStatus(s domain.VerifyStatus) VerifyStatus {
	return VerifyStatus(upperEnum(string(s)))
}

func mapRoomType(t domain.RoomType) RoomType {
	return RoomType(upperEnum(string(t)))
}

func mapTeamSide(s domain.TeamSide) TeamSide {
	return TeamSide(upperEnum(string(s)))
}

func mapTeamSidePtr(s *domain.TeamSide) *TeamSide {
	if s == nil {
		return nil
	}
	v := mapTeamSide(*s)
	return &v
}

func mapMatchStatus(s domain.MatchStatus) MatchStatus {
	return MatchStatus(upperEnum(string(s)))
}

func mapMatchPhase(p domain.MatchPhase) MatchPhase {
	return MatchPhase(upperEnum(string(p)))
}

func mapPieceMod(m domain.PieceMod) PieceMod {
	return PieceMod(upperEnum(string(m)))
}

func mapPieceState(s domain.PieceState) PieceState {
	return PieceState(upperEnum(string(s)))
}

func mapMoveType(t domain.MoveType) MoveType {
	return MoveType(upperEnum(string(t)))
}

func mapZone(z domain.Zone) BoardZone {
	return BoardZone(upperEnum(string(z)))
}

func mapForceModPtr(fm *domain.ForceMod) *string {
	if fm == nil {
		return nil
	}
	return ptrStr(string(*fm))
}

// --- 核心类型映射 ---

func mapUser(u *domain.User) *User {
	if u == nil {
		return nil
	}
	return &User{
		ID:           u.ID.Hex(),
		OnlineID:     int(u.OnlineID),
		Username:     u.Username,
		AvatarURL:    u.AvatarURL,
		CountryCode:  u.CountryCode,
		GlobalRank:   ptrInt(u.GlobalRank),
		Pp:           ptrFloat64(u.PP),
		VerifyStatus: mapVerifyStatus(u.VerifyStatus),
		IsBanned:     u.IsBanned,
		Roles:        mapRoles(u.Roles),
		CreatedAt:    u.CreatedAt,
		UpdatedAt:    u.UpdatedAt,
	}
}

func mapRoom(r *domain.Room) *Room {
	if r == nil {
		return nil
	}
	var matchID *string
	if r.MatchID != nil {
		id := r.MatchID.Hex()
		matchID = &id
	}
	return &Room{
		ID:        r.ID.Hex(),
		Code:      r.Code,
		Name:      r.Name,
		Type:      mapRoomType(r.Type),
		OwnerID:   int(r.OwnerID),
		Settings:  mapRoomSettings(&r.Settings),
		MatchID:   matchID,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
		// Match 由 roomResolver.Match 解析
	}
}

func mapRoomSettings(s *domain.RoomSettings) *RoomSettings {
	if s == nil {
		return nil
	}
	return &RoomSettings{
		RedStrategistUserID:  int64PtrToIntPtr(s.RedStrategistUserID),
		BlueStrategistUserID: int64PtrToIntPtr(s.BlueStrategistUserID),
		StreamerUserID:       int64PtrToIntPtr(s.StreamerUserID),
		Mappool:              mapMappool(&s.Mappool),
		FirstPick:            mapTeamSidePtr(s.FirstPick),
		FirstBan:             mapTeamSidePtr(s.FirstBan),
		RedPlayers:           int64SliceToIntSlice(s.RedPlayers),
		BluePlayers:          int64SliceToIntSlice(s.BluePlayers),
		RedLeader:            int64PtrToIntPtr(s.RedLeader),
		BlueLeader:           int64PtrToIntPtr(s.BlueLeader),
		MpLink:               s.MPLink,
		StreamLink:           s.StreamLink,
	}
}

func mapMatch(m *domain.Match) *Match {
	if m == nil {
		return nil
	}
	board := mapBoard(&m.Board)
	pool := mapMappool(&m.Mappool)
	teams := &MatchTeams{
		Red:  mapTeam(&m.TeamRed),
		Blue: mapTeam(&m.TeamBlue),
	}
	bpOrder := mapBPOrder(&m.BPOrder)
	turnState := mapTurnState(&m.TurnState)
	timer := mapTimerState(&m.Timer)
	phase := mapMatchPhase(m.TurnState.Phase)

	result := &Match{
		ID:         m.ID.Hex(),
		Code:       m.Code,
		Name:       m.Name,
		RoomType:   mapRoomType(m.RoomType),
		RoomID:     m.RoomID.Hex(),
		Status:     mapMatchStatus(m.Status),
		Phase:      &phase,
		ActiveTeam: mapTeamSidePtr(m.TurnState.ActiveTeam),
		Board:      board,
		Pool:       pool,
		Teams:      teams,
		BpOrder:    bpOrder,
		TurnState:  turnState,
		Timer:      timer,
		CreatedAt:  m.CreatedAt,
		StartedAt:  m.StartedAt,
		FinishedAt: m.FinishedAt,
		UpdatedAt:  m.UpdatedAt,
		// Moves, RecentMove, Room, Result 由 resolver 方法解析
	}
	return result
}

func mapTeam(t *domain.Team) *Team {
	if t == nil {
		return nil
	}
	return &Team{
		ID:           t.ID.Hex(),
		Side:         mapTeamSide(t.Side),
		Name:         t.Name,
		Description:  t.Description,
		Seed:         t.Seed,
		Color:        t.Color,
		LeaderID:     int64ValToIntPtr(t.LeaderID),
		StrategistID: int64ValToIntPtr(t.StrategistID),
		Players:      int64SliceToIntSlice(t.Players),
	}
}

func mapBoard(b *domain.Board) *Board {
	if b == nil {
		return nil
	}
	cells := make([][]*BoardCell, len(b.Cells))
	for y := range b.Cells {
		cells[y] = make([]*BoardCell, len(b.Cells[y]))
		for x := range b.Cells[y] {
			cells[y][x] = mapBoardCell(&b.Cells[y][x])
		}
	}
	return &Board{
		Rows:  b.Rows,
		Cols:  b.Cols,
		Cells: cells,
	}
}

func mapBoardCell(c *domain.Cell) *BoardCell {
	if c == nil {
		return nil
	}
	return &BoardCell{
		Position: mapPosition(c.Position),
		Zone:     mapZone(c.Zone),
		State:    string(c.State),
		PieceID:  c.PieceID,
		TeamID:   c.TeamID,
	}
}

func mapPosition(p domain.Position) *Position {
	return &Position{
		Row: p.Y, // domain Y = GraphQL row
		Col: p.X, // domain X = GraphQL col
	}
}

func mapPositionPtr(p *domain.Position) *Position {
	if p == nil {
		return nil
	}
	return mapPosition(*p)
}

func mapMappool(m *domain.Mappool) *Mappool {
	if m == nil {
		return nil
	}
	// 确定遍历顺序: NM, HD, HR, DT, FM, Shiro, TB
	order := []domain.PieceMod{
		domain.PieceModNM, domain.PieceModHD, domain.PieceModHR, domain.PieceModDT,
		domain.PieceModFM, domain.PieceModShiro, domain.PieceModTB,
	}

	var groups []*PoolSlotGroup
	for _, mod := range order {
		pieces, ok := m.Slots[mod]
		if !ok || len(pieces) == 0 {
			continue
		}
		slots := make([]*PoolSlot, len(pieces))
		for i, piece := range pieces {
			slots[i] = mapPieceToPoolSlot(mod, i+1, &piece)
		}
		groups = append(groups, &PoolSlotGroup{
			Mod:    mapPieceMod(mod),
			Pieces: slots,
		})
	}
	return &Mappool{Slots: groups}
}

func mapPieceToPoolSlot(mod domain.PieceMod, index int, p *domain.Piece) *PoolSlot {
	if p == nil {
		return nil
	}
	return &PoolSlot{
		Mod:       mapPieceMod(mod),
		Index:     index,
		BeatmapID: int64PtrToIntPtr(p.BeatmapID),
		State:     mapPieceState(p.State),
		TeamID:    p.TeamID,
		ForceMod:  mapForceModPtr(p.ForceMod),
		Position:  mapPositionPtr(p.Position),
		// Beatmap 由 poolSlotResolver.Beatmap 解析
	}
}

func mapPoolSlot(s *domain.PoolSlot) *PoolSlot {
	if s == nil {
		return nil
	}
	return &PoolSlot{
		Mod:   mapPieceMod(s.Mod),
		Index: s.Index,
		// BeatmapID/State/TeamID/ForceMod/Position 不可用 (domain.PoolSlot 只有 Mod+Index)
		// Beatmap resolver 会因 BeatmapID=nil 返回 nil
	}
}

func mapBeatmap(b *domain.Beatmap) *Beatmap {
	if b == nil {
		return nil
	}
	return &Beatmap{
		ID:                b.ID.Hex(),
		OnlineID:          int(b.OnlineID),
		BeatmapsetID:      int(b.BeatmapsetID),
		Title:             b.Title,
		Artist:            b.Artist,
		DifficultyName:    b.DifficultyName,
		Status:            b.Status,
		AuthorID:          int(b.AuthorID),
		RulesetID:         b.RulesetID,
		StarRating:        b.StarRating,
		Bpm:               b.BPM,
		TotalLength:       b.TotalLength,
		DrainRate:         b.DrainRate,
		CircleSize:        b.CircleSize,
		ApproachRate:      b.ApproachRate,
		OverallDifficulty: b.OverallDifficulty,
		CoverURL:          b.CoverURL,
		ModString:         b.ModString,
		ModIndex:          b.ModIndex,
		SelectorID:        ptrInt(b.SelectorID),
		CreditUserIDs:     int64SliceToIntSlice(b.CreditUserIDs),
		Skill:             nullableStr(b.Skill),
		Comment:           nullableStr(b.Comment),
		IsOriginal:        b.IsOriginal,
		CreatedAt:         b.CreatedAt,
		UpdatedAt:         b.UpdatedAt,
	}
}

func mapMove(m *domain.Move) *Move {
	if m == nil {
		return nil
	}
	return &Move{
		ID:         m.ID.Hex(),
		MatchID:    m.MatchID.Hex(),
		RoomID:     m.RoomID.Hex(),
		Type:       mapMoveType(m.Type),
		TeamSide:   mapTeamSidePtr(m.TeamSide),
		OperatorID: int(m.OperatorID),
		Slot:       mapPoolSlot(m.Slot),
		From:       mapPositionPtr(m.From),
		To:         mapPositionPtr(m.To),
		ForceMod:   mapForceModPtr(m.ForceMod),
		RedScore:   mapPlayerScore(m.RedScore),
		BlueScore:  mapPlayerScore(m.BlueScore),
		Comment:    m.Comment,
		CreatedAt:  m.CreatedAt,
	}
}

func mapPlayerScore(s *domain.PlayerScore) *PlayerScore {
	if s == nil {
		return nil
	}
	return &PlayerScore{
		UserID:   int(s.UserID),
		Score:    int(s.Score),
		Accuracy: s.Acc,
		Combo:    s.Combo,
	}
}

func mapBPOrder(o *domain.BPOrder) *BPOrder {
	if o == nil {
		return nil
	}
	return &BPOrder{
		FirstPick: mapTeamSide(o.FirstPick),
		FirstBan:  mapTeamSide(o.FirstBan),
	}
}

func mapTurnState(t *domain.TurnState) *TurnState {
	if t == nil {
		return nil
	}
	action := string(t.Action)
	return &TurnState{
		Phase:      mapMatchPhase(t.Phase),
		Counter:    t.Counter,
		ActiveTeam: mapTeamSidePtr(t.ActiveTeam),
		Action:     &action,
		StartedAt:  &t.StartedAt,
		TimeLimit:  durationSeconds(t.TimeLimit),
		BonusTime:  durationSeconds(t.BonusTime),
		BonusUsed:  t.BonusUsed,
	}
}

func mapTimerState(t *domain.TimerState) *TimerState {
	if t == nil {
		return nil
	}
	return &TimerState{
		StartedAt:        &t.StartedAt,
		TimeLimit:        int(t.TimeLimit.Seconds()),
		BonusTime:        int(t.BonusTime.Seconds()),
		BonusUsed:        t.BonusUsed,
		IsPaused:         t.IsPaused,
		PausedAt:         t.PausedAt,
		RemainingAtPause: durationSeconds(t.RemainingAtPause),
	}
}

func mapMatchResult(r *domain.Result) *MatchResult {
	if r == nil {
		return nil
	}
	return &MatchResult{
		ID:         r.ID.Hex(),
		MatchID:    r.MatchID.Hex(),
		RoomID:     r.RoomID.Hex(),
		Winner:     mapTeamSidePtr(r.WinnerID),
		WinReason:  mapWinReason(r.WinReason),
		Scores:     mapTeamScores(r.Scores),
		WonPieces:  mapWonPieces(r.WonPieces),
		Alignments: mapWinningAlignments(r.Alignments),
		Summary:    nullableStr(r.Summary),
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
	}
}

func mapWinReason(r domain.WinReason) WinReason {
	return WinReason(upperEnum(string(r)))
}

func mapTeamScores(scores []domain.TeamScore) []*TeamScore {
	result := make([]*TeamScore, len(scores))
	for i, s := range scores {
		result[i] = &TeamScore{
			TeamSide: mapTeamSide(s.TeamID),
			Score:    s.Score,
		}
	}
	return result
}

func mapWonPieces(won map[domain.TeamSide]int) map[string]any {
	if won == nil {
		return nil
	}
	// map[TeamSide]int → JSON object {"red":N,"blue":N}
	m := map[string]any{}
	for side, count := range won {
		m[string(side)] = count
	}
	return m
}

func mapWinningAlignments(aligned []domain.WinningAlignment) []*WinningAlignment {
	result := make([]*WinningAlignment, len(aligned))
	for i, a := range aligned {
		positions := make([]*Position, len(a.Positions))
		for j, p := range a.Positions {
			positions[j] = mapPosition(p)
		}
		result[i] = &WinningAlignment{
			Length:    a.Length,
			Positions: positions,
			TeamID:    a.TeamID,
		}
	}
	return result
}

func mapAnnouncement(a *domain.Announcement) *Announcement {
	if a == nil {
		return nil
	}
	return &Announcement{
		ID:          a.ID.Hex(),
		Pinned:      a.Pinned,
		Visible:     a.Visible,
		Title:       a.Title,
		Content:     a.Content,
		AuthorID:    int(a.AuthorID),
		PublishedAt: a.PublishedAt,
		CreatedAt:   a.CreatedAt,
		UpdatedAt:   a.UpdatedAt,
	}
}

// --- 分页映射 ---

func mapMatchPage(result paginate.Result[domain.Match]) *MatchPage {
	items := make([]*Match, len(result.Data))
	for i := range result.Data {
		items[i] = mapMatch(&result.Data[i])
	}
	return &MatchPage{
		Items:      items,
		Page:       int(result.Page),
		PerPage:    int(result.PerPage),
		Total:      int(result.Total),
		TotalPages: int(result.TotalPages),
	}
}

func mapRoomPage(result paginate.Result[domain.Room]) *RoomPage {
	items := make([]*Room, len(result.Data))
	for i := range result.Data {
		items[i] = mapRoom(&result.Data[i])
	}
	return &RoomPage{
		Items:      items,
		Page:       int(result.Page),
		PerPage:    int(result.PerPage),
		Total:      int(result.Total),
		TotalPages: int(result.TotalPages),
	}
}

func mapBeatmapPage(result paginate.Result[domain.Beatmap]) *BeatmapPage {
	items := make([]*Beatmap, len(result.Data))
	for i := range result.Data {
		items[i] = mapBeatmap(&result.Data[i])
	}
	return &BeatmapPage{
		Items:      items,
		Page:       int(result.Page),
		PerPage:    int(result.PerPage),
		Total:      int(result.Total),
		TotalPages: int(result.TotalPages),
	}
}

func mapUserPage(result paginate.Result[domain.User]) *UserPage {
	items := make([]*User, len(result.Data))
	for i := range result.Data {
		items[i] = mapUser(&result.Data[i])
	}
	return &UserPage{
		Items:      items,
		Page:       int(result.Page),
		PerPage:    int(result.PerPage),
		Total:      int(result.Total),
		TotalPages: int(result.TotalPages),
	}
}

func mapAnnouncementPage(result paginate.Result[domain.Announcement]) *AnnouncementPage {
	items := make([]*Announcement, len(result.Data))
	for i := range result.Data {
		items[i] = mapAnnouncement(&result.Data[i])
	}
	return &AnnouncementPage{
		Items:      items,
		Page:       int(result.Page),
		PerPage:    int(result.PerPage),
		Total:      int(result.Total),
		TotalPages: int(result.TotalPages),
	}
}

// --- 低级辅助 ---

func int64PtrToIntPtr(p *int64) *int {
	if p == nil {
		return nil
	}
	return ptrInt(*p)
}

// int64ValToIntPtr 将 int64 值转换为 *int，零值返回 nil。
func int64ValToIntPtr(v int64) *int {
	if v == 0 {
		return nil
	}
	return ptrInt(v)
}

func int64SliceToIntSlice(s []int64) []int {
	result := make([]int, len(s))
	for i, v := range s {
		result[i] = int(v)
	}
	return result
}

func nullableStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
