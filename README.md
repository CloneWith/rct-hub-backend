# RCT Hub Backend

Backend service for **RCT (Ranka's Chess Tournament)** — a community osu! tournament platform that blends board-game strategy with rhythm-game competition. Built with Go, Gin, MongoDB, Redis, and GraphQL.

## Table of Contents

- [Overview](#overview)
- [Tech Stack](#tech-stack)
- [Architecture](#architecture)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Authentication](#authentication)
- [REST API](#rest-api)
- [GraphQL API](#graphql-api)
- [Match Engine](#match-engine)
- [Project Structure](#project-structure)
- [Development](#development)
- [Roadmap](#roadmap)

---

## Overview

RCT Hub orchestrates osu! tournament matches structured around a 4×4 board. Two teams (Red vs. Blue) take turns banning and placing beatmap pieces on the board, aiming to align four won pieces in a row — much like Connect Four, but each piece is resolved through an actual osu! multiplayer match.

The backend provides:

- **User management** with osu! OAuth 2.0, HttpOnly browser sessions, and Bearer JWT support for tools
- **Room & match lifecycle** — from pre-match setup through live play to post-match results
- **Board operations** — ban, pick, rob, and win pieces on a zone-based 4×4 grid
- **Client-specific views** — tailored data shapes for strategists, spectators, OBS overlays, and referees
- **Admin CMS** — beatmap management, user roles, announcements

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Language | Go 1.26 |
| HTTP Framework | Gin |
| GraphQL | gqlgen v0.17.94 |
| Database | MongoDB 7.0 (v2 driver, replica set) |
| Cache | Redis 7.4 (go-redis v9) |
| Auth | osu! OAuth 2.0 + JWT (golang-jwt/v5) |
| Logging | Zap + gin-zap |
| Config | godotenv + environment variables |

## Architecture

The system follows a layered design with a three-channel API surface:

```
                     ┌──────────┐
                     │  Client  │
                     └────┬─────┘
              ┌───────────┼───────────┐
              │           │           │
        REST (Gin)   GraphQL     WebSocket
        /api/v1/*    /graphql    (planned)
              │           │
    ┌─────────┴───────────┴─────────┐
    │          Middleware            │
    │  Auth · RBAC · CORS · Zap     │
    └──────────────┬────────────────┘
                   │
    ┌──────────────┴────────────────┐
    │           Service Layer        │
    │  Room · Match · User · Beatmap │
    │  Announcement · Auth · Move    │
    └──────────────┬────────────────┘
                   │
    ┌──────────────┴────────────────┐
    │         Repository Layer       │
    │  MongoDB data access (v2)     │
    └──────────────┬────────────────┘
                   │
    ┌──────────────┴────────────────┐
    │          Domain Layer          │
    │  User · Room · Match · Board   │
    │  Piece · Move · Turn · Timer   │
    └───────────────────────────────┘

    ┌───────────────────────────────┐
    │     Match Engine (isolated)    │
    │  Deterministic rules engine    │
    │  Execute(state, actor, cmd)    │
    │  → Transition{State, Events}   │
    └───────────────────────────────┘
```

### Three-Channel API Design

| Channel | Responsibility |
|---------|---------------|
| **REST** | OAuth callbacks, health checks, pre-match room configuration, admin CRUD |
| **GraphQL** | All read operations, client-tailored views (Read Model), in-match board command mutations |
| **WebSocket** *(planned)* | Real-time board sync, timer ticks, reconnection & version recovery |

## Quick Start

### 1. Start Dependencies

```bash
make docker-up
```

Launches MongoDB (port `27017`, replica set `rs0`) and Redis (port `6379`) via Docker Compose.

### 2. Configure Environment

```bash
cp .env.example .env
# Edit .env — at minimum set JWT_SECRET (must be ≥ 32 bytes)
# For osu! OAuth testing, fill in OSU_CLIENT_ID and OSU_CLIENT_SECRET
```

### 3. Initialize Database

```bash
make initdb        # Create collections, indexes, and validators
make initdb-seed   # Collections + indexes + validators + sample data
make initdb-drop   # Drop and rebuild with sample data
```

Run `make initdb` after upgrading an existing environment. The server now
checks the snapshot, command receipt, action log, and outbox validators at
startup and will fail closed if they have not been installed. Match commands
use MongoDB transactions, so MongoDB must run as a replica set; the bundled
single-node Docker setup already does this.

The `cmd/initdb` tool accepts flags:
- `-drop` — drop existing collections before rebuilding
- `-seed` — insert sample admin user, beatmaps, rooms, and announcements

### 4. Run the Server

```bash
make run
```

The server listens on `:8080` by default (configurable via `PORT` in `.env`).

Verify:
- Health check: `GET http://localhost:8080/health`
- API health: `GET http://localhost:8080/api/v1/health`
- GraphQL Playground: `GET http://localhost:8080/graphql`

## Configuration

All configuration is loaded from environment variables (with `.env` file support via godotenv).

| Variable | Default | Description |
|----------|---------|-------------|
| `APP_ENV` | `development` | Runtime environment (`production` enables Gin release mode) |
| `PORT` | `8080` | HTTP listen port |
| `LOG_LEVEL` | `info` | Zap log level |
| `MONGODB_URI` | `mongodb://localhost:27017/?replicaSet=rs0&directConnection=true` | MongoDB connection string |
| `MONGODB_NAME` | `rcthub` | MongoDB database name |
| `REDIS_ADDR` | `localhost:6379` | Redis address |
| `REDIS_PASSWORD` | *(empty)* | Redis password |
| `REDIS_DB` | `0` | Redis database number |
| `JWT_SECRET` | *(required)* | JWT signing secret — must be ≥ 32 bytes |
| `JWT_EXPIRY_HOURS` | `168` (7 days) | JWT token lifetime |
| `AUTH_COOKIE_NAME` | `rcthub_session` | Browser session cookie name |
| `AUTH_COOKIE_DOMAIN` | *(empty)* | Optional shared parent domain for Web/API subdomains |
| `AUTH_COOKIE_SECURE` | `false` in development, required in production | Send the session cookie only over HTTPS |
| `AUTH_COOKIE_SAME_SITE` | `lax` | Browser SameSite policy (`lax` or `strict`) |
| `OSU_CLIENT_ID` | *(empty)* | osu! OAuth client ID |
| `OSU_CLIENT_SECRET` | *(empty)* | osu! OAuth client secret |
| `OSU_REDIRECT_URI` | `http://localhost:8080/auth/osu/callback` | OAuth callback URL |
| `OSU_API_BASE` | `https://osu.ppy.sh` | osu! API base URL |
| `ALLOWED_ORIGINS` | value of `FRONTEND_URI` | Comma-separated exact browser origins; wildcard is rejected |

## Authentication

The backend uses osu! OAuth 2.0. Browser login stores the signed session in an
HttpOnly cookie; the OAuth redirect contains no token. Bearer JWT remains
available for scripts and non-browser clients.

### Setup osu! OAuth

1. Register an application at [osu! Account Settings → OAuth](https://osu.ppy.sh/home/account/edit#oauth).
2. Set the callback URL to `http://localhost:8080/auth/osu/callback` (matching `OSU_REDIRECT_URI`).
3. Enter the Client ID and Client Secret into `.env`.

### Auth Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /auth/osu` | Redirects to osu! authorization page |
| `GET /auth/osu/callback` | OAuth callback; sets the session cookie and redirects to `/auth/callback` |
| `POST /auth/logout` | Clears the browser session cookie |

### RBAC Middleware

Use `middleware.RequireRole(domain.RoleAdmin, domain.RoleReferee, ...)` to protect role-specific routes.

## REST API

After the GraphQL migration, REST is focused on OAuth, health, room setup, and admin CRUD. All `/api/v1` endpoints return a unified format: `{"success": true, "data": ...}` or `{"success": false, "error": "..."}`.

### Infrastructure & Auth

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/health` | Infrastructure health check |
| GET | `/api/v1/health` | API health check (authenticated) |
| GET | `/auth/osu` | Start osu! OAuth flow |
| GET | `/auth/osu/callback` | OAuth callback |

### Room Configuration (Authenticated)

Pre-match setup endpoints — all require a valid JWT.

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/v1/rooms` | Create a room |
| PATCH | `/api/v1/rooms/:id/strategists` | Set team strategists |
| PATCH | `/api/v1/rooms/:id/streamer` | Set streamer |
| PATCH | `/api/v1/rooms/:id/mappool` | Set the pre-match mappool |
| PATCH | `/api/v1/rooms/:id/bp-order` | Set first pick / first ban order |
| PATCH | `/api/v1/rooms/:id/players` | Set team rosters |
| PATCH | `/api/v1/rooms/:id/mp-link` | Set multiplayer match link |
| PATCH | `/api/v1/rooms/:id/stream-link` | Set stream link |
| POST | `/api/v1/rooms/:id/start-match` | Create the formal READY match; play starts through GraphQL `startMatch` |

### Admin CRUD (Admin Role Required)

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/v1/beatmaps` | Create beatmap |
| PUT | `/api/v1/beatmaps/:id` | Update beatmap |
| DELETE | `/api/v1/beatmaps/:id` | Delete beatmap |
| PATCH | `/api/v1/users/:id/roles` | Update user roles |
| PATCH | `/api/v1/users/:id/banned` | Ban / unban user |
| PATCH | `/api/v1/users/:id/verify-status` | Update verification status |
| POST | `/api/v1/announcements` | Create announcement |
| PUT | `/api/v1/announcements/:id` | Update announcement |
| DELETE | `/api/v1/announcements/:id` | Delete announcement |
| POST | `/api/v1/announcements/:id/publish` | Publish announcement |

## GraphQL API

The GraphQL endpoint at `/graphql` handles all read operations, client-specific views, and in-match board commands.

- **GET `/graphql`** — GraphiQL Playground (development)
- **POST `/graphql`** — Query / Mutation endpoint
- Authentication is optional for public queries. Browsers use the session cookie; tools may send `Authorization: Bearer <jwt>`.

### Queries (15)

```
ping · me
match(id) · matchByCode(code) · matches(page, perPage)
room(id) · roomByCode(code) · rooms(type, page, perPage)
beatmap(id) · beatmapByOsuId(osuId) · beatmaps(page, perPage)
user(id) · users(page, perPage)
announcements(page, perPage) · announcement(id)
```

Formal match lists batch-load authoritative snapshots. `Match.room`,
`Room.match`, and metadata relations remain regular nested resolvers.

### Client Views (Read Model)

Each match exposes five tailored views to prevent over-fetching and under-fetching:

| View | Auth | Purpose |
|------|------|---------|
| `strategistView` | Current verified assigned strategist | Engine-derived actions, legal placements, ban targets, and complete robbery plans |
| `captainView` | Current verified team leader | TB request/response availability for the captain's team |
| `spectatorView` | Public | Authoritative board, won counts, lifecycle, phase, and turn |
| `overlayView` | Public | Minimal render data for OBS overlays |
| `refereeView` | Current assigned referee or administrator | Full snapshot, Engine-derived referee actions, and durable audit log |

### Authoritative Match Mutations

Every formal match mutation requires `expectedVersion` and a client-generated
UUID `commandId`. A retry with the same request returns the original committed
result; a stale page receives `MATCH_VERSION_CONFLICT` and `currentVersion`.

| Area | Mutations |
|------|-----------|
| Lifecycle | `startMatch`, `suspendMatch`, `resumeMatch`, `abortMatch` |
| Ban and placement | `banPoolSlot`, `placePiece`, `placeShiro` and their explicit `referee*` proxy variants |
| Results and robbery | `confirmBeatmapResult`, `robPiece`, `refereeRobPiece` |
| Timer | `grantAdditionalTime`, `calibrateTimer`, `pauseTimer`, `resumeTimer`, `skipCurrentAction` |
| Tie-break and surrender | `requestTb`, `respondTbRequest`, referee proxy variants, `startTb`, `confirmTbResult`, `recordSurrender` |

Successful results include the same typed snapshot returned by match queries,
plus typed event facts, actor information, stable event IDs, and per-match
sequence numbers. Versions are decimal strings so GraphQL's 32-bit `Int` limit
cannot truncate them. The legacy direct-win,
advance-turn, robbery-stage, unban, and undo mutations are not public because
they do not represent valid MatchEngine commands.

### Example Query

```graphql
query MatchDashboard($id: ID!) {
  match(id: $id) {
    id
    pool { poolSlotID beatmapID beatmap { title artist } }
    snapshot {
      version lifecycle phase activeTeam turn
      board { cells { cell row col zone piece { id mod outcome owner } } }
      poolSlots { id mod state }
      timer { startedAt durationMilliseconds paused remainingAtPauseMilliseconds }
    }
    spectatorView {
      wonCounts { red blue }
      currentPhase
    }
  }
}
```

### Regenerating GraphQL Code

After modifying `schema.graphql`:

```bash
make generate
```

`resolver.go` holds manually maintained dependencies; gqlgen writes field
implementations to `schema.resolvers.go`. Keep shared helpers out of the
generated file.

### Web Contract Fixtures and Mock

`contracts/fixtures/` contains deterministic GraphQL responses generated by
real MatchEngine command sequences for READY, BAN, PICK, result confirmation,
suspension, TB, finished, aborted, and adjudication states.

```bash
make fixtures   # regenerate and verify fixture responses
make matchmock  # GraphQL Playground at http://127.0.0.1:8091
```

The mock keeps state in memory and supports the formal mutations, optimistic
versions, and idempotent command replay. Restart it to reset all scenarios.
Fixture codes use the `FIXTURE_<SCENARIO>` form, for example `FIXTURE_PICK`.
It authenticates fixture user `1001`, who can open the RED strategist and
captain views as well as the referee view.

## Match Engine

`internal/matchengine/` is a **fully deterministic rules engine** — the core domain logic for board operations. It has zero external dependencies: no HTTP, no database, no Redis, no osu! API, no random numbers, no system clock.

### Design Principles

- **Pure functions** — `Execute(state, actor, command, now)` never mutates input state; returns `Transition{State, Events}` with version incremented
- **Caller responsibilities** — authentication, ID generation, optimistic locking, persistence, and DTO conversion are handled by the service layer
- **Full auditability** — every state transition produces domain events

### Supported Commands (~28)

`StartMatch`, `BanPoolSlot`, `PlacePiece`, `PlaceShiro`, `RobPiece`, `ConfirmBeatmapResult`, `GrantAdditionalTime`, `CalibrateTimer`, `PauseTimer`/`ResumeTimer`, `SuspendMatch`/`ResumeMatch`, `SkipCurrentAction`, `AbortMatch`, `RequestTB`/`RespondTBRequest`, `StartTB`/`ConfirmTBResult`, `RecordSurrender`, plus referee proxy variants.

### State Lifecycle

```
READY → RUNNING → SUSPENDED / ADJUDICATION_REQUIRED / FINISHED / ABORTED

Phases: NONE → BAN → PICK → WAITING_FOR_RESULT → TB_PREPARATION → TB_PLAYING
```

### Board Layout

The 4×4 board is divided into four mod quadrants:

```
     Col 0   Col 1   Col 2   Col 3
Row 0  DT      DT      HD      HD
Row 1  DT      DT      HD      HD
Row 2  HR      HR      DT      DT
Row 3  HR      HR      DT      DT
```

Win condition: align four won pieces in a row (horizontal, vertical, or diagonal).

## Project Structure

```
cmd/
  server/                  # Application entry point
  initdb/                  # Database initialization tool

internal/
  config/                  # Environment-based configuration
  database/                # MongoDB & Redis connection management
  domain/                  # Core domain models & business rules
  repository/              # MongoDB data access layer
  service/                 # Business logic layer
  handler/                 # REST API handlers
  graphql/                 # GraphQL resolvers, schema, directives, DataLoader
  matchengine/             # Deterministic match rules engine
  middleware/              # Auth, RBAC, error handling
  oauth/                   # osu! OAuth 2.0 client
  server/                  # HTTP server setup & route registration
  logger/                  # Zap logger initialization

pkg/
  errs/                    # Unified error definitions
  jwtutil/                 # JWT signing & parsing utilities
  paginate/                # Pagination helpers
  response/                # Unified HTTP response envelope

contracts/
  graphql-v1.graphql        # Frozen compatibility baseline
  fixtures/                 # Engine-generated Web responses

deploy/
  mongodb/                 # MongoDB health check script

schema.graphql             # Active GraphQL schema (source of truth)
gqlgen.yml                 # gqlgen code generation config
graphql.config.yml         # IDE GraphQL plugin config
```

## Development

### Common Commands

```bash
make run           # Run the server
make build         # Compile to ./bin/server
make test          # Run all tests
make lint          # go vet + staticcheck
make generate      # Regenerate GraphQL code (gqlgen)
make fixtures      # Regenerate deterministic Web fixtures
make matchmock     # Start the fixture-backed GraphQL mock on :8091
make graphql-compat # Reject breaking GraphQL v1 changes
make docker-up     # Start MongoDB + Redis containers
make docker-down   # Stop containers
make initdb        # Initialize database (collections + indexes + validators)
make initdb-seed   # Initialize with sample data
make initdb-drop   # Drop, rebuild, and seed
make dev           # Start dependencies, initialize schema, and run server
make verify        # Run verification tool
```

### Testing

The project tests the rule engine, authoritative command path, snapshot
recovery, GraphQL contract, browser security, and MongoDB transaction path.

```bash
make test
```

## Roadmap

- [ ] **WebSocket Gateway** — Real-time board synchronization, timer push, reconnection & version recovery
- [ ] **Realtime Delivery** — Publish committed outbox events, reconnect by sequence, and resync snapshots
- [ ] **Bancho IRC Adapter** — Multiplayer-room commands, acknowledgements, degraded mode, and referee takeover
- [ ] **osu! Metadata Refresh** — Background refresh policy for beatmaps and users
- [ ] **Dockerfile & CI** — Production container image and CI/CD pipeline

## License

See [LICENSE](LICENSE) for details.
