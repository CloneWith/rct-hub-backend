package matchfixture

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/paginate"
)

type Scenario struct {
	Name  string
	Match service.FormalMatch
}

var fixtureTime = time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)

// Scenarios generates every Web lifecycle fixture by executing MatchEngine
// commands. No fixture contains hand-maintained state transitions.
func Scenarios() ([]Scenario, error) {
	ready, err := matchengine.NewReadyState(fixtureConfiguration())
	if err != nil {
		return nil, err
	}
	ban, err := execute(ready, matchengine.RefereeActor(), matchengine.StartMatch{}, 1)
	if err != nil {
		return nil, err
	}
	pick := ban
	bans := []struct {
		team matchengine.TeamSide
		slot string
	}{{matchengine.TeamRed, "NM-1"}, {matchengine.TeamBlue, "NM-2"}, {matchengine.TeamBlue, "NM-3"}, {matchengine.TeamRed, "NM-4"}}
	for index, command := range bans {
		pick, err = execute(pick, matchengine.StrategistActor(command.team), matchengine.BanPoolSlot{PoolSlotID: command.slot}, index+2)
		if err != nil {
			return nil, err
		}
	}
	waiting, err := execute(pick, matchengine.StrategistActor(pick.ActiveTeam), matchengine.PlacePiece{PoolSlotID: "FM-1", PieceID: "piece-01", Cell: "A1"}, 7)
	if err != nil {
		return nil, err
	}
	suspended, err := execute(pick, matchengine.RefereeActor(), matchengine.SuspendMatch{Reason: "fixture suspension"}, 7)
	if err != nil {
		return nil, err
	}
	aborted, err := execute(pick, matchengine.RefereeActor(), matchengine.AbortMatch{Reason: "fixture abort"}, 7)
	if err != nil {
		return nil, err
	}

	turnEleven, err := playWonPieces(pick, 10)
	if err != nil {
		return nil, err
	}
	tbPreparation, err := execute(turnEleven, matchengine.CaptainActor(matchengine.TeamRed), matchengine.RequestTB{RequestID: "fixture-tb", Basis: matchengine.TBBasisCaptainAgreement}, 30)
	if err != nil {
		return nil, err
	}
	tbPreparation, err = execute(tbPreparation, matchengine.CaptainActor(matchengine.TeamBlue), matchengine.RespondTBRequest{RequestID: "fixture-tb", Accept: true}, 31)
	if err != nil {
		return nil, err
	}
	tbPlaying, err := execute(tbPreparation, matchengine.RefereeActor(), matchengine.StartTB{}, 32)
	if err != nil {
		return nil, err
	}
	finished, err := execute(tbPlaying, matchengine.RefereeActor(), matchengine.ConfirmTBResult{WinningTeam: matchengine.TeamRed}, 33)
	if err != nil {
		return nil, err
	}
	adjudication, err := playWonPieces(pick, 16)
	if err != nil {
		return nil, err
	}

	states := []struct {
		name  string
		state matchengine.State
	}{
		{"READY", ready}, {"BAN", ban}, {"PICK", pick}, {"WAITING_FOR_RESULT", waiting},
		{"SUSPENDED", suspended}, {"TB_PREPARATION", tbPreparation}, {"TB_PLAYING", tbPlaying},
		{"FINISHED", finished}, {"ABORTED", aborted}, {"ADJUDICATION_REQUIRED", adjudication},
	}
	result := make([]Scenario, len(states))
	for index, item := range states {
		id, _ := bson.ObjectIDFromHex(fmt.Sprintf("507f1f77bcf86cd79943%04x", index+1))
		result[index] = Scenario{Name: item.name, Match: fixtureMatch(id, item.name, item.state)}
	}
	return result, nil
}

func playWonPieces(state matchengine.State, count int) (matchengine.State, error) {
	owners := []matchengine.TeamSide{
		matchengine.TeamRed, matchengine.TeamRed, matchengine.TeamBlue, matchengine.TeamBlue,
		matchengine.TeamBlue, matchengine.TeamBlue, matchengine.TeamRed, matchengine.TeamRed,
		matchengine.TeamRed, matchengine.TeamRed, matchengine.TeamBlue, matchengine.TeamBlue,
		matchengine.TeamBlue, matchengine.TeamBlue, matchengine.TeamRed, matchengine.TeamRed,
	}
	for index := range count {
		cell := matchengine.Cell([]byte{byte('A' + index%4), byte('1' + index/4)})
		pieceID := fmt.Sprintf("piece-%02d", index+1)
		slotID := fmt.Sprintf("NM-%d", index+5)
		var err error
		state, err = execute(state, matchengine.StrategistActor(state.ActiveTeam), matchengine.PlacePiece{PoolSlotID: slotID, PieceID: pieceID, Cell: cell}, 8+index*2)
		if err != nil {
			return matchengine.State{}, err
		}
		state, err = execute(state, matchengine.RefereeActor(), matchengine.ConfirmBeatmapResult{BoardPieceID: pieceID, WinningTeam: owners[index]}, 9+index*2)
		if err != nil {
			return matchengine.State{}, err
		}
	}
	return state, nil
}

func execute(state matchengine.State, actor matchengine.Actor, command matchengine.Command, seconds int) (matchengine.State, error) {
	transition, err := matchengine.Execute(state, actor, command, fixtureTime.Add(time.Duration(seconds)*time.Second))
	if err != nil {
		return matchengine.State{}, fmt.Errorf("fixture %T: %w", command, err)
	}
	return transition.State, nil
}

func fixtureConfiguration() matchengine.Configuration {
	slots := make([]matchengine.PoolSlot, 0, 26)
	for index := 1; index <= 20; index++ {
		slots = append(slots, matchengine.PoolSlot{ID: fmt.Sprintf("NM-%d", index), Mod: matchengine.ModNM})
	}
	slots = append(slots,
		matchengine.PoolSlot{ID: "HD-1", Mod: matchengine.ModHD},
		matchengine.PoolSlot{ID: "HR-1", Mod: matchengine.ModHR},
		matchengine.PoolSlot{ID: "DT-1", Mod: matchengine.ModDT},
		matchengine.PoolSlot{ID: "FM-1", Mod: matchengine.ModFM},
	)
	slots = append(slots, matchengine.PoolSlot{ID: "SHIRO-1", Mod: matchengine.ModShiro}, matchengine.PoolSlot{ID: "TB-1", Mod: matchengine.ModTB})
	return matchengine.Configuration{
		FirstBan: matchengine.TeamRed, FirstPick: matchengine.TeamBlue, PoolSlots: slots,
		Rosters: map[matchengine.TeamSide]matchengine.Roster{
			matchengine.TeamRed:  {LeaderID: 1001, PlayerIDs: []int64{1001, 1002, 1003, 1004, 1005, 1006, 1007, 1008}},
			matchengine.TeamBlue: {LeaderID: 2001, PlayerIDs: []int64{2001, 2002, 2003, 2004, 2005, 2006, 2007, 2008}},
		},
		Timers: matchengine.StandardTimerConfiguration(),
	}
}

func fixtureMatch(id bson.ObjectID, name string, state matchengine.State) service.FormalMatch {
	roomID, _ := bson.ObjectIDFromHex("6" + id.Hex()[1:])
	pool := make(map[string]*int64, len(state.PoolSlots))
	slotIDs := make([]string, 0, len(state.PoolSlots))
	for slotID := range state.PoolSlots {
		slotIDs = append(slotIDs, slotID)
	}
	sort.Strings(slotIDs)
	for index, slotID := range slotIDs {
		slot := state.PoolSlots[slotID]
		if slot.Mod == matchengine.ModShiro {
			pool[slotID] = nil
			continue
		}
		beatmapID := int64(100000 + index)
		pool[slotID] = &beatmapID
	}
	return service.FormalMatch{
		ID: id, Code: "FIXTURE_" + name, Name: strings.ReplaceAll(name, "_", " "),
		RoomID: roomID, RoomType: domain.RoomTypeMatch, CreatedAt: fixtureTime,
		Pool: pool, State: state,
	}
}

type Reader struct {
	mu       sync.RWMutex
	byID     map[bson.ObjectID]service.FormalMatch
	byCode   map[string]service.FormalMatch
	items    []service.FormalMatch
	beatmaps map[int64]domain.Beatmap
	users    map[int64]domain.User
	rooms    map[bson.ObjectID]domain.Room
}

type UserReader struct{ reader *Reader }
type RoomReader struct{ reader *Reader }

func NewReader() (*Reader, error) {
	scenarios, err := Scenarios()
	if err != nil {
		return nil, err
	}
	reader := &Reader{
		byID: make(map[bson.ObjectID]service.FormalMatch, len(scenarios)), byCode: make(map[string]service.FormalMatch, len(scenarios)),
		items: make([]service.FormalMatch, len(scenarios)), beatmaps: make(map[int64]domain.Beatmap),
		users: make(map[int64]domain.User), rooms: make(map[bson.ObjectID]domain.Room, len(scenarios)),
	}
	reader.users[1001] = domain.User{OnlineID: 1001, Username: "fixture-user", VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleStrategist, domain.RoleAdmin}}
	for index, scenario := range scenarios {
		reader.byID[scenario.Match.ID] = scenario.Match
		reader.byCode[scenario.Match.Code] = scenario.Match
		reader.items[index] = scenario.Match
		matchID := scenario.Match.ID
		redID, blueID := int64(1001), int64(2001)
		reader.rooms[scenario.Match.RoomID] = domain.Room{
			ID: scenario.Match.RoomID, Type: domain.RoomTypeMatch, OwnerID: redID, RefereeUserID: &redID, MatchID: &matchID,
			Settings: domain.RoomSettings{RedStrategistUserID: &redID, BlueStrategistUserID: &blueID, RedLeader: &redID, BlueLeader: &blueID},
		}
		for slotID, beatmapID := range scenario.Match.Pool {
			if beatmapID == nil {
				continue
			}
			objectID, _ := bson.ObjectIDFromHex(fmt.Sprintf("%024x", *beatmapID))
			reader.beatmaps[*beatmapID] = domain.Beatmap{
				ID: objectID, OnlineID: *beatmapID, BeatmapsetID: *beatmapID,
				Title: "Fixture " + slotID, Artist: "RCTS1", DifficultyName: slotID,
				Status: "ranked", AuthorID: 1001, RulesetID: 0,
				ModString: string(scenario.Match.State.PoolSlots[slotID].Mod),
				CreatedAt: fixtureTime, UpdatedAt: fixtureTime,
			}
		}
	}
	sort.Slice(reader.items, func(i, j int) bool { return reader.items[i].Code < reader.items[j].Code })
	return reader, nil
}

func (r *Reader) PrivateUsers() *UserReader { return &UserReader{reader: r} }
func (r *Reader) PrivateRooms() *RoomReader { return &RoomReader{reader: r} }

func (r *UserReader) GetByOsuID(_ context.Context, id int64) (*domain.User, error) {
	r.reader.mu.RLock()
	defer r.reader.mu.RUnlock()
	user, exists := r.reader.users[id]
	if !exists {
		return nil, errs.ErrNotFound
	}
	return &user, nil
}

func (r *RoomReader) GetRoom(_ context.Context, id bson.ObjectID) (*domain.Room, error) {
	r.reader.mu.RLock()
	defer r.reader.mu.RUnlock()
	room, exists := r.reader.rooms[id]
	if !exists {
		return nil, errs.ErrNotFound
	}
	return &room, nil
}

func (r *Reader) GetByOsuID(_ context.Context, id int64) (*domain.Beatmap, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	beatmap, exists := r.beatmaps[id]
	if !exists {
		return nil, errs.ErrNotFound
	}
	return &beatmap, nil
}

func (r *Reader) ByID(_ context.Context, id bson.ObjectID) (*service.FormalMatch, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, exists := r.byID[id]
	if !exists {
		return nil, errs.ErrNotFound
	}
	value.State = value.State.Clone()
	return &value, nil
}

func (r *Reader) ByCode(_ context.Context, code string) (*service.FormalMatch, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, exists := r.byCode[code]
	if !exists {
		return nil, errs.ErrNotFound
	}
	value.State = value.State.Clone()
	return &value, nil
}

func (r *Reader) List(_ context.Context, params paginate.Params) (paginate.Result[service.FormalMatch], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	params.Normalize()
	start := min(params.Skip(), int64(len(r.items)))
	end := min(start+params.PerPage, int64(len(r.items)))
	items := make([]service.FormalMatch, end-start)
	copy(items, r.items[start:end])
	for index := range items {
		items[index].State = items[index].State.Clone()
	}
	return paginate.NewResult(items, params, int64(len(r.items))), nil
}
