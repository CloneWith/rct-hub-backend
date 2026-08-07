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

- **User management** with osu! OAuth 2.0 login and JWT sessions
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

> See `docs/adr-001-graphql-introduction.md` for the full architecture decision record.

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
| `LOG_LEVEL` | `info` | Zap log level (`debug`, `info`, `warn`, `error`) |
| `LOG_DIR` | `./logs` | Directory for timestamped log files (empty = stdout only) |
| `LOG_SUPPRESS` | *(empty)* | Comma-separated blacklist of log categories to silence (see [Log Categories](#log-categories)) |
| `MONGODB_URI` | `mongodb://localhost:27017/?replicaSet=rs0&directConnection=true` | MongoDB connection string |
| `MONGODB_NAME` | `rcthub` | MongoDB database name |
| `REDIS_ADDR` | `localhost:6379` | Redis address |
| `REDIS_PASSWORD` | *(empty)* | Redis password |
| `REDIS_DB` | `0` | Redis database number |
| `JWT_SECRET` | *(required)* | JWT signing secret — must be ≥ 32 bytes |
| `JWT_EXPIRY_HOURS` | `168` (7 days) | JWT token lifetime |
| `OSU_CLIENT_ID` | *(empty)* | osu! OAuth client ID |
| `OSU_CLIENT_SECRET` | *(empty)* | osu! OAuth client secret |
| `OSU_REDIRECT_URI` | `http://localhost:8080/auth/osu/callback` | OAuth callback URL |
| `OSU_API_BASE` | `https://osu.ppy.sh` | osu! API base URL |
| `ALLOWED_ORIGINS` | `*` | Comma-separated CORS allowed origins |

## Authentication

The backend uses osu! OAuth 2.0 for login and issues JWT tokens for session management.

### Setup osu! OAuth

1. Register an application at [osu! Account Settings → OAuth](https://osu.ppy.sh/home/account/edit#oauth).
2. Set the callback URL to `http://localhost:8080/auth/osu/callback` (matching `OSU_REDIRECT_URI`).
3. Enter the Client ID and Client Secret into `.env`.

### Auth Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /auth/osu` | Redirects to osu! authorization page |
| `GET /auth/osu/callback` | OAuth callback — on success, redirects to `/auth/callback?token=<jwt>` |
| `GET /api/v1/auth/me` | Returns current user info (requires `Authorization: Bearer <jwt>`) |

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
- Authentication is optional: provide `Authorization: Bearer <jwt>` for authenticated queries; public queries work without it.

### Queries (15)

```
ping · me
match(id) · matchByCode(code) · matches(status, page, perPage)
room(id) · roomByCode(code) · rooms(type, page, perPage)
beatmap(id) · beatmapByOsuId(osuId) · beatmaps(page, perPage)
user(id) · users(page, perPage)
announcements(page, perPage) · announcement(id)
```

Nested resolvers: `Match.moves`, `Match.recentMove`, `Match.room`, `Room.match`, `PoolSlot.beatmap` — with DataLoader batch loading to prevent N+1 queries.

### Client Views (Read Model)

Each match exposes four tailored views to prevent over-fetching and under-fetching:

| View | Auth | Purpose |
|------|------|---------|
| `strategistView` | `@requireRole(role: STRATEGIST)` | Allowed/disallowed actions, selectable slots & cells, timer, robbery state |
| `spectatorView` | Public | Board summary, team scores, current phase, recent moves |
| `overlayView` | Public | Minimal render data for OBS overlays |
| `refereeView` | `@requireRole(role: REFEREE, admin: true)` | Full match data, audit log placeholder, connection status placeholder |

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

Successful results include the new match version and committed events with
stable event IDs and per-match sequence numbers. The legacy direct-win,
advance-turn, robbery-stage, unban, and undo mutations are not public because
they do not represent valid MatchEngine commands.

### Example Query

```graphql
query MatchDashboard($id: ID!) {
  match(id: $id) {
    phase
    activeTeam
    board { cells { position zone piece { state owner } } }
    pool { slots { mod index beatmap { title artist coverUrl } state } }
    teams { red { name strategistID } blue { name strategistID } }
    timer { remainingSeconds isPaused }
    spectatorView {
      scores { red blue }
      currentPhase
      recentMoves { type teamSide createdAt }
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

The 4×4 board is divided into four mod zones:

```
     Col 0   Col 1   Col 2   Col 3
Row 0  HD      HD      DT      DT
Row 1  HD      HD      DT      DT
Row 2  HR      HR      NM      NM
Row 3  HR      HR      NM      NM
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

docs/
  adr-001-graphql-introduction.md   # GraphQL architecture decision record
  schema-full.graphql               # Full GraphQL schema reference

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
make docker-up     # Start MongoDB + Redis containers
make docker-down   # Stop containers
make initdb        # Initialize database (collections + indexes + validators)
make initdb-seed   # Initialize with sample data
make initdb-drop   # Drop, rebuild, and seed
make dev           # Start dependencies, initialize schema, and run server
make verify        # Run verification tool
```

### Testing

The project maintains comprehensive tests across all layers:

| Package | Tests | Coverage |
|---------|-------|----------|
| `domain` | 7 tests | Board logic, piece rules, win detection |
| `graphql` | 39 tests | Phase 1 (queries) + Phase 2 (views) + Phase 3 (mutations) |
| `matchengine` | 10 suites | Engine, robbery, scenarios, timers, terminal states |
| `service` | Multiple | Auth, room, match service logic |
| `config` | 1 test | Configuration validation |

```bash
make test
```

## Log Categories

The logger package (`internal/logger`) defines a set of **typed category constants** that map to per-module log files. By default, every category gets its own `<name>-<timestamp>.log` file. Categories listed in `LOG_SUPPRESS` are silenced entirely.

| Constant | String | Covers |
|----------|--------|--------|
| `CatRuntime` | `runtime` | Server start/stop, config load, graceful shutdown, background workers. This IS the main logger (`Provider.Main()`); always active, cannot be suppressed. |
| `CatStorage` | `storage` | MongoDB & Redis: connection management, query execution, cache hits/misses, index checks. |
| `CatNetwork` | `network` | Inbound HTTP tracing (Gin access logs) and outbound calls: osu! API fetcher, webhooks, upstream proxies. |
| `CatAuth` | `auth` | osu! OAuth handshake, JWT issuance/validation/refresh, session expiry, token revocation. |
| `CatAudit` | `audit` | Security-sensitive mutations: role grants/revocations, ban/unban, verification status, match result finalisation. |
| `CatMatchEngine` | `matchengine` | Board transitions, command execution, event emission, timer state, win detection, robbery logic. |
| `CatFetcher` | `fetcher` | osu! API proxy: three-tier cache lookups (Redis → MongoDB → osu! API v2), token refresh, cache invalidation. |

### Usage

```go
// In a service that needs a category logger:
log := provider.Get(logger.CatStorage)
log.Info("connected", zap.String("db", "rcthub"))
```

### Suppressing Categories

Set `LOG_SUPPRESS` in `.env` to a comma-separated list of category names to silence:

```env
# Silence verbose categories in production
LOG_SUPPRESS=network,fetcher
```

Suppressed categories return a no-op logger — their logs are silently dropped. The `runtime` category (main logger) is always active and cannot be suppressed.

### Adding a New Category

1. Add a `const` entry in `internal/logger/categories.go`.
2. Add it to the `AllCategories()` return slice.
3. Document it in the table above.

## Roadmap

- [ ] **WebSocket Gateway** — Real-time board synchronization, timer push, reconnection & version recovery
- [ ] **Fetcher Module** — Beatmap metadata and avatar proxy service
- [ ] **Audit System** — Full action audit log with sequence tracking
- [ ] **Connection Tracking** — WebSocket client connection status per match
- [ ] **Remaining Mutations** — Implement `unbanPoolSlot`, `grantWinPermission`, `beginRobbery`, `cancelRobbery`, `undoAction`
- [ ] **Dockerfile & CI** — Production container image and CI/CD pipeline

## License

See [LICENSE](LICENSE) for details.
