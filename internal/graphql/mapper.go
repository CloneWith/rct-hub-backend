package graphql

import (
	"strconv"
	"strings"

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

func upperEnum(s string) string {
	return strings.ToUpper(s)
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

func mapPieceMod(m domain.PieceMod) PieceMod {
	return PieceMod(upperEnum(string(m)))
}

func mapPieceState(s domain.PieceState) PieceState {
	return PieceState(upperEnum(string(s)))
}

func mapForceModPtr(fm *domain.ForceMod) *string {
	if fm == nil {
		return nil
	}
	return new(string(*fm))
}

// --- 核心类型映射 ---

func mapUser(u *domain.User) *User {
	if u == nil {
		return nil
	}
	return &User{
		ID:           u.ID.Hex(),
		OnlineID:     strconv.FormatInt(u.OnlineID, 10),
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
		ID:            r.ID.Hex(),
		Code:          r.Code,
		Name:          r.Name,
		Type:          mapRoomType(r.Type),
		OwnerID:       strconv.FormatInt(r.OwnerID, 10),
		RefereeUserID: int64PtrToStringPtr(r.RefereeUserID),
		Round:         r.Round,
		ScheduledAt:   r.ScheduledAt,
		Settings:      mapRoomSettings(&r.Settings),
		MatchID:       matchID,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
		// Match 由 roomResolver.Match 解析
	}
}

func mapRoomSettings(s *domain.RoomSettings) *RoomSettings {
	if s == nil {
		return nil
	}
	return &RoomSettings{
		RedStrategistUserID:  int64PtrToStringPtr(s.RedStrategistUserID),
		BlueStrategistUserID: int64PtrToStringPtr(s.BlueStrategistUserID),
		StreamerUserID:       int64PtrToStringPtr(s.StreamerUserID),
		Mappool:              mapMappool(&s.Mappool),
		FirstPick:            mapTeamSidePtr(s.FirstPick),
		FirstBan:             mapTeamSidePtr(s.FirstBan),
		RedPlayers:           int64SliceToStringSlice(s.RedPlayers),
		BluePlayers:          int64SliceToStringSlice(s.BluePlayers),
		RedLeader:            int64PtrToStringPtr(s.RedLeader),
		BlueLeader:           int64PtrToStringPtr(s.BlueLeader),
		MpLink:               s.MPLink,
		StreamLink:           s.StreamLink,
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
		BeatmapID: int64PtrToStringPtr(p.BeatmapID),
		State:     mapPieceState(p.State),
		TeamID:    p.TeamID,
		ForceMod:  mapForceModPtr(p.ForceMod),
		Position:  mapPositionPtr(p.Position),
		// Beatmap 由 poolSlotResolver.Beatmap 解析
	}
}

func mapBeatmap(b *domain.Beatmap) *Beatmap {
	if b == nil {
		return nil
	}
	return &Beatmap{
		ID:                b.ID.Hex(),
		OnlineID:          strconv.FormatInt(b.OnlineID, 10),
		BeatmapsetID:      strconv.FormatInt(b.BeatmapsetID, 10),
		Title:             b.Title,
		Artist:            b.Artist,
		DifficultyName:    b.DifficultyName,
		Status:            b.Status,
		AuthorID:          strconv.FormatInt(b.AuthorID, 10),
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
		SelectorID:        int64ValToStringPtr(b.SelectorID),
		CreditUserIDs:     int64SliceToStringSlice(b.CreditUserIDs),
		Skill:             nullableStr(b.Skill),
		Comment:           nullableStr(b.Comment),
		IsOriginal:        b.IsOriginal,
		CreatedAt:         b.CreatedAt,
		UpdatedAt:         b.UpdatedAt,
	}
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
		AuthorID:    strconv.FormatInt(a.AuthorID, 10),
		PublishedAt: a.PublishedAt,
		CreatedAt:   a.CreatedAt,
		UpdatedAt:   a.UpdatedAt,
	}
}

// --- 分页映射 ---

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

func int64PtrToStringPtr(p *int64) *string {
	if p == nil {
		return nil
	}
	value := strconv.FormatInt(*p, 10)
	return &value
}

func int64ValToStringPtr(v int64) *string {
	if v == 0 {
		return nil
	}
	value := strconv.FormatInt(v, 10)
	return &value
}

func int64SliceToStringSlice(s []int64) []string {
	result := make([]string, len(s))
	for i, v := range s {
		result[i] = strconv.FormatInt(v, 10)
	}
	return result
}

func nullableStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
