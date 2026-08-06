# RCTS1 MatchEngine API

`internal/matchengine` is the deterministic domain engine for formal RCTS1
matches. It owns match-rule decisions and emits state transitions plus domain
events. It has no dependency on HTTP, WebSocket, MongoDB, Redis, osu! APIs,
random identifiers, or the system clock.

This document describes the Go integration contract. It is not a production
REST or realtime API specification.

## Boundary

The engine provides:

- configuration validation and creation of a `READY` aggregate;
- deterministic command evaluation;
- immutable transition results;
- stable rule-error codes;
- serializable match state and domain events;
- read-only legal-action analysis.

The caller remains responsible for:

- authenticating the user and deriving the room role;
- mapping authenticated roles to `Actor`;
- generating command, piece, and TB request identifiers;
- expected-version checks and command idempotency;
- atomic persistence of the new state, version, and events;
- public HTTP/realtime DTOs and schema versioning;
- delivering snapshots and ordered events to clients.

`Actor` is trusted input from the outer application layer. Never construct an
actor directly from an unverified client field.

## Core API

### Create a match

```go
state, err := matchengine.NewReadyState(matchengine.Configuration{
    FirstBan:  matchengine.TeamRed,
    FirstPick: matchengine.TeamBlue,
    PoolSlots: poolSlots,
    Rosters:   rosters,
    Timers:    matchengine.StandardTimerConfiguration(),
})
```

`NewReadyState` validates:

- `FirstBan` and `FirstPick` are `RED` or `BLUE`;
- PoolSlot IDs are non-empty and unique;
- every Mod is supported;
- exactly one Shiro slot and one TB slot exist;
- both teams have exactly eight unique players;
- player IDs are unique across both teams;
- each team leader belongs to that team's roster;
- the timer configuration is complete and positive.

### Execute a command

```go
transition, err := matchengine.Execute(
    state,
    matchengine.StrategistActor(matchengine.TeamBlue),
    matchengine.PlacePiece{
        PoolSlotID: "NM5",
        PieceID:    "piece-1",
        Cell:       "A1",
    },
    serverNow,
)
if err != nil {
    code := matchengine.CodeOf(err)
    // Map the stable code to the public transport error contract.
}

state = transition.State
events := transition.Events
```

`Execute` has these invariants:

1. The input `State` is never mutated.
2. An accepted command increments `State.Version` exactly once.
3. A rejected command returns no transition and changes no state.
4. Decisions depend only on `State`, `Actor`, `Command`, and injected `now`.
5. Events are ordered domain facts for the accepted transition.

The application layer should load the current aggregate, verify the expected
version and idempotency key, call `Execute`, then persist `Transition.State`
and `Transition.Events` in one transaction.

### Analyze legal actions

```go
analysis := matchengine.Analyze(state)
```

`Analysis` contains:

| Field | Meaning |
| --- | --- |
| `selectablePoolSlotIds` | Available non-TB PoolSlots. |
| `emptyCells` | Empty board cells in deterministic board order. |
| `legalCellsByPoolSlot` | Legal cells for every selectable PoolSlot. |
| `legalPlacements` | Flattened legal PoolSlot/cell pairs and derived FM Force Mod. |
| `wonCounts` | Current WON-piece count for RED and BLUE. |
| `stalemate` | No PoolSlot is selectable or no legal placement exists. |

Collection fields serialize as JSON arrays or objects, never `null`.
`Analysis` is advisory for clients and tooling; `Execute` remains authoritative.

## State Model

### Lifecycle

| Lifecycle | Meaning | Ordinary writes |
| --- | --- | --- |
| `READY` | Configured but not started. | Only `StartMatch`. |
| `RUNNING` | Formal match action is active. | Controlled by `Phase`. |
| `SUSPENDED` | Referee froze the match. | Resume, skip, or abort only. |
| `ADJUDICATION_REQUIRED` | Equal-WON stalemate needs the deferred human rule. | Closed. No winner is fabricated. |
| `FINISHED` | A four, TB, surrender, or unequal stalemate decided a winner. | Closed. |
| `ABORTED` | Referee voided the match without a winner. | Closed. |

### Phase

| Phase | Meaning |
| --- | --- |
| `NONE` | No active formal action. |
| `BAN` | ABBA PoolSlot bans. |
| `PICK` | ABAB placement, Shiro, robbery, or negotiated TB request. |
| `WAITING_FOR_RESULT` | One BoardPiece awaits referee result confirmation. |
| `TB_PREPARATION` | Captains agreed to TB, or turn 15 forced TB after both robberies; referee prepares play. |
| `TB_PLAYING` | TB is in progress and awaits referee result confirmation. |

`Turn` uses `-3` through `0` during Ban, then starts at `1` for Pick.
`ActiveTeam` is absent when the current phase has no strategist action.

### PoolSlot and BoardPiece

A `PoolSlot` is configured match input. A `BoardPiece` is created when a slot
is placed. They must not share identity or lifecycle.

BoardPiece outcomes are:

- `WAITING_RESULT`: placed but not owned;
- `WON`: owned and eligible for alignments;
- `WHITE`: unowned Shiro;
- `DEAD`: sacrificed, still occupies its cell, and cannot form alignments.

For FM, `BoardPiece.ForceMod` is derived from the board zone. Both DT regions
produce NM Force Mod.

## Commands

| Command | Required actor | Main valid context |
| --- | --- | --- |
| `StartMatch` | Referee | `READY`. |
| `BanPoolSlot` | Active strategist | `RUNNING/BAN`, live timer. |
| `PlacePiece` | Active strategist | `RUNNING/PICK`, live timer. |
| `PlaceShiro` | Active strategist | `RUNNING/PICK`, Shiro available. |
| `RobPiece` | Active strategist | `RUNNING/PICK`, robbery unused, non-overlapping sacrifices valid, and the robbed target participates in the resulting required alignment. |
| `ConfirmBeatmapResult` | Referee | `WAITING_FOR_RESULT`, matching pending piece. |
| `GrantAdditionalTime` | Referee | Expired Ban, Pick, or result-confirmation timer; once per active team per match. |
| `CalibrateTimer` | Referee | Active phase with a timer. |
| `PauseTimer` / `ResumeTimer` | Referee | Active operational timer. |
| `SuspendMatch` / `ResumeMatch` | Referee | Running/suspended lifecycle transition. |
| `SkipCurrentAction` | Referee | Expired Ban/Pick action, or suspended action. |
| `AbortMatch` | Referee | Running or suspended match. |
| `RequestTB` | Team captain | Pick turns 11 through 14 with `CAPTAIN_AGREEMENT`. |
| `RespondTBRequest` | Opposing team captain | Matching pending request. |
| `StartTB` | Referee | `TB_PREPARATION`; reason required after expiry. |
| `ConfirmTBResult` | Referee | `TB_PLAYING`. |
| `RecordSurrender` | Referee | Four unique rostered confirmations including the leader. |

Referee proxy variants exist for Ban, placement, Shiro, robbery, and TB
negotiation. They override the acting role and, for strategist actions, timer
expiry only. They do not bypass phase, active-team, board, Mod, sacrifice, or TB
rules. Every proxy command requires a non-blank audit reason and emits
`REFEREE_PROXY_ACTION_RECORDED` after the normal command events.

All operational and administrative audit reasons reject empty or
whitespace-only values.

## Timer Semantics

The engine never reads the system clock. The caller supplies authoritative
server time to `Execute`.

- Expiry occurs at the exact deadline.
- Expiry does not advance a turn automatically.
- Remaining time is clamped to zero and cannot exceed the configured duration.
- Pause freezes the current remainder.
- Resume cannot create time after expiry.
- Standard timers are 60s Ban, 15s Ban additional, 90s Pick, 30s Pick
  additional, 20s result confirmation, 10s result additional, and 90s TB
  preparation. Result confirmation additional time is available after its timer
  expires and consumes the active team's single pause opportunity.

TB negotiation is available on turns 11 through 14 and requires both team
captains to agree. On turn 15, once both teams have used their one robbery, the
engine enters TB preparation immediately before another Pick can occur.

Go `time.Duration` values serialize as integer nanoseconds. Public DTOs should
convert them to an explicitly documented transport unit.

## Results and Stalemate

Terminal `Result.reason` is one of:

- `FOUR_ALIGNMENT`
- `TB`
- `SURRENDER`
- `STALEMATE_WON_COUNT`

When a stalemate has unequal WON counts, the larger count wins and both counts
are stored in `Result`. When both counts are equal, the engine enters
`ADJUDICATION_REQUIRED`, stores only `StalemateEvidence`, exposes no winner or
result, and rejects further writes. The deferred pseudo scoring rule is not
implemented as an implicit tiebreaker.

## Stable Errors

Use `CodeOf(err)` instead of matching error messages.

| Code | Meaning |
| --- | --- |
| `INVALID_REQUEST` | Structurally invalid command or configuration. |
| `ACTION_NOT_ALLOWED` | Actor capability or operation is not permitted. |
| `MATCH_LIFECYCLE_CONFLICT` | Lifecycle does not accept the command. |
| `MATCH_PHASE_CONFLICT` | Phase does not accept the command. |
| `NOT_ACTIVE_TEAM` | Strategist/proxy team is not active. |
| `INVALID_POOL_SLOT` | PoolSlot does not exist or cannot be used by this command. |
| `POOL_SLOT_UNAVAILABLE` | PoolSlot is already banned or selected. |
| `INVALID_BOARD_CELL` | Cell is invalid or occupied. |
| `INVALID_MOD_ZONE` | HD/HR/DT placement does not match the cell zone. |
| `RESULT_NOT_PENDING` | Pending result identity or context is wrong. |
| `TIMER_EXPIRED` | Strategist action reached its deadline. |
| `TIMER_PAUSED` | Strategist action is blocked by operational pause. |
| `TEAM_PAUSE_ALREADY_USED` | Active team already consumed its one opportunity. |
| `ROBBERY_NOT_AVAILABLE` | Team already robbed or robbery is unavailable. |
| `ROBBERY_REQUIREMENTS_NOT_MET` | Target or sacrifice evidence is invalid. |
| `ALIGNMENT_OVERLAP` | A sacrifice piece appears in more than one alignment. |
| `TB_NOT_AVAILABLE` | TB basis/request state is invalid. |
| `SURRENDER_EVIDENCE_INVALID` | Roster confirmation evidence is insufficient. |

## Persistence and Recovery

`State`, `Event`, and `Analysis` are JSON serializable. JSON round trips are
covered by tests, but serialization alone is not a persistence transaction.

The M3 orchestrator must atomically persist:

1. the accepted command identity and idempotency result;
2. the previous and new aggregate versions;
3. the complete new `State`;
4. the ordered `Events` emitted by the transition.

Historical rollback is also an orchestration/persistence concern. It is not an
engine command.

## Match Lab

Run the local manual test tool:

```text
go run ./tools/matchlab
```

Then open `http://127.0.0.1:8091`.

Built-in scenarios:

| Scenario | Purpose |
| --- | --- |
| `ready` | Match before start. |
| `first-pick` | Completed ABBA Ban at Pick turn 1. |
| `robbery-ready` | Valid BLUE three-alignment and RED target. |
| `turn-13` | Pick turn 13 for negotiated TB tests. |
| `stalemate-final` | Full board with the final piece waiting for confirmation. RED produces 8:8 adjudication; BLUE produces a 9:7 win. |

Match Lab exposes `GET /api/state`, `POST /api/reset`, `POST /api/time`, and
`POST /api/command`. These endpoints are test-only adapters. They intentionally
do not implement authentication, expected-version checks, idempotency,
persistence, or public DTO stability and must not be used by Web clients.

## Verification

```text
go test ./...
go test -race ./...
go run ./tools/verify
```
