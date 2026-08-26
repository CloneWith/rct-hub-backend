# RCT Hub Backend — Frontend / AI Agent Reference

This document describes the backend contract for frontend developers and AI agents working on the RCT Hub project. It covers authentication, RESTful endpoints, response formats, domain models, and planned GraphQL/WebSocket interfaces.

## Project Overview

RCT Hub is a web platform for the osu! "RCT" tournament format. The backend (`rctHubBackend`) is implemented in Go with Gin and exposes both RESTful and GraphQL APIs backed by MongoDB and Redis.

- **Base URL (local)**: `http://localhost:8090`
- **API prefix**: `/api/v1`
- **Health**: `GET /health` and `GET /api/v1/health`
- **Authentication**: osu! OAuth 2.0 + JWT
- **CORS**: configured from `ALLOWED_ORIGINS` in `.env`

## Authentication

### Login Flow

1. Redirect the user to `GET /auth/osu`.
2. After authorization, osu! redirects to `GET /auth/osu/callback?code=...&state=...`.
3. The backend creates or updates the local user, issues a JWT, and redirects to:
   ```
   /auth/callback?token=<jwt>
   ```
4. The frontend stores the JWT and sends it on every protected request:
   ```
   Authorization: Bearer <jwt>
   ```

### Token Contents

The JWT contains:

- `user_id`: MongoDB ObjectID hex string
- `osu_id`: osu! user id (int64)
- `username`: osu! username
- `roles`: array of user roles (`player`, `strategist`, `referee`, `streamer`, `admin`)
- standard `exp`, `iat`, `iss` claims

### Current User

- `GET /api/v1/auth/me` — returns the full `User` object.
- `GET /api/v1/users/me` — alias returning the current user profile.

## Response Format

All `/api/v1` endpoints return a unified envelope:

```json
{
  "success": true,
  "data": { ... }
}
```

On error:

```json
{
  "success": false,
  "error": "human readable message"
}
```

### Field-Level Validation Details

When a request fails input validation (`400 Bad Request`), the response carries a
`details` array describing each offending field. `field` is the wire-format
(JSON) field path, `rule` is the failed validation rule, and `message` is an
English explanation:

```json
{
  "success": false,
  "error": "invalid input",
  "details": [
    { "field": "name", "rule": "required", "message": "name is required" },
    { "field": "first_pick", "rule": "oneof", "message": "first_pick must be one of: red, blue" },
    { "field": "body", "rule": "json", "message": "request body ended unexpectedly" }
  ]
}
```

- `rule: "required"` — the field is missing or empty; `field` may be a dotted
  path for nested settings (e.g. `settings.mp_link`, returned when starting a
  match before room setup is complete).
- `rule: "oneof"` — `message` lists the allowed values.
- `rule: "min"` / `"max"` — length bounds (characters for strings, items for
  slices).
- `rule: "type"` — the field has the wrong JSON type; `field: "body"` with
  `rule: "json"` marks a malformed or empty request body.

Frontends can use `field` to highlight the corresponding input, or render
`message` directly when no form control matches.

HTTP status codes follow REST conventions:

- `200 OK` — success
- `201 Created` — resource created
- `204 No Content` — deletion success
- `400 Bad Request` — invalid input
- `401 Unauthorized` — missing or invalid JWT
- `403 Forbidden` — insufficient role permissions
- `404 Not Found` — resource does not exist
- `409 Conflict` — duplicate or conflict
- `500 Internal Server Error` — unexpected server error

## Pagination

List endpoints accept:

- `?page=1` — page number, defaults to 1
- `?per_page=20` — page size, defaults to 20, capped at 100

Paginated response:

```json
{
  "success": true,
  "data": {
    "data": [ ... ],
    "page": 1,
    "per_page": 20,
    "total": 100,
    "total_pages": 5
  }
}
```

## Domain Models

### User

```json
{
  "id": "...",
  "osuId": 123456,
  "username": "player_name",
  "avatarUrl": "https://a.ppy.sh/123456",
  "countryCode": "CN",
  "roles": ["player"],
  "verifyStatus": "verified",
  "isBanned": false,
  "globalRank": 1024,
  "pp": 114.51,
  "createdAt": "2026-07-23T04:09:19Z",
  "updatedAt": "2026-07-23T04:09:19Z"
}
```

Notes:

- `roles` can contain: `player`, `strategist`, `referee`, `streamer`, `admin`.
- `verify_status` can be: `verified`, `pending`, `unverified`.
- On first osu! login, users are created with `player` role and `pending` status.

### Beatmap

```json
{
  "_id": "...",
  "id": 1000000,
  "beatmapset_id": 500000,
  "title": "Seed Beatmap",
  "artist": "Seed Artist",
  "version": "Normal",
  "user_id": 1000,
  "mode_int": 0,
  "status": "ranked",
  "difficulty_rating": 4.5,
  "bpm": 180,
  "total_length": 120,
  "drain": 5,
  "cs": 4,
  "ar": 9,
  "accuracy": 8,
  "cover_url": "https://assets.ppy.sh/beatmaps/500000/covers/cover.jpg",
  "mod_string": "NM",
  "mod_index": 0,
  "selector_id": 0,
  "credit_user_ids": [],
  "skill": "",
  "comment": "",
  "is_original": false,
  "created_at": "...",
  "updated_at": "..."
}
```

### Room

```json
{
  "id": "...",
  "code": "ABCDEF",
  "name": "Friendly Match",
  "type": "casual",
  "owner_id": 123456,
  "settings": {
    "red_strategist_user_id": 111,
    "blue_strategist_user_id": 222,
    "streamer_user_id": 333,
    "mappool": { ... },
    "first_pick": "red",
    "first_ban": "blue",
    "red_players": [111, 112],
    "blue_players": [222, 223],
    "red_leader": 111,
    "blue_leader": 222,
    "mp_link": "https://osu.ppy.sh/mp/...",
    "stream_link": "https://twitch.tv/..."
  },
  "match_id": "...",
  "created_at": "...",
  "updated_at": "..."
}
```

Room types: `private`, `casual`, `match`.

### Match

```json
{
  "id": "...",
  "room_id": "...",
  "code": "MATCH-001",
  "name": "Friendly Match",
  "room_type": "casual",
  "team_red": { ... },
  "team_blue": { ... },
  "mappool": { ... },
  "board": { ... },
  "bp_order": { "first_pick": "red", "first_ban": "blue" },
  "turn_state": { ... },
  "timer": { ... },
  "status": "active",
  "started_at": "...",
  "finished_at": null,
  "created_at": "...",
  "updated_at": "..."
}
```

Match statuses: `pending`, `active`, `finished`, `canceled`.

### Move

```json
{
  "id": "...",
  "match_id": "...",
  "room_id": "...",
  "type": "pick",
  "team_side": "red",
  "operator_id": 123456,
  "slot": { "mod": "NM", "index": 1 },
  "from": null,
  "to": { "x": 0, "y": 0 },
  "force_mod": null,
  "created_at": "..."
}
```

Move types: `ban`, `pick`, `rob`, `win`, `surrender`.

### Announcement

```json
{
  "id": "...",
  "pinned": true,
  "visible": true,
  "title": "Welcome",
  "content": "...",
  "author_id": 1,
  "published_at": "...",
  "created_at": "...",
  "updated_at": "..."
}
```

## REST API Reference

### Authentication

| Method | Endpoint | Auth | Description |
|--------|----------|------|-------------|
| GET | `/auth/osu` | No | Start osu! OAuth login |
| GET | `/auth/osu/callback` | No | OAuth callback (redirects with token) |
| GET | `/api/v1/auth/me` | Yes | Current user |

### Users

| Method | Endpoint | Auth | Description |
|--------|----------|------|-------------|
| GET | `/api/v1/users/me` | Yes | Current user profile |
| GET | `/api/v1/users` | Yes | List non-banned users |
| GET | `/api/v1/users/:id` | Yes | User details |
| PATCH | `/api/v1/users/:id/roles` | Admin | Update user roles |
| PATCH | `/api/v1/users/:id/banned` | Admin | Ban/unban user |
| PATCH | `/api/v1/users/:id/verify-status` | Admin | Update verify status |

### Beatmaps

| Method | Endpoint | Auth | Description |
|--------|----------|------|-------------|
| GET | `/api/v1/beatmaps` | No | List beatmaps |
| GET | `/api/v1/beatmaps/:id` | No | Beatmap details |
| GET | `/api/v1/beatmaps/osu/:osu_id` | No | Beatmap by osu! id |
| POST | `/api/v1/beatmaps` | Admin | Create beatmap |
| PUT | `/api/v1/beatmaps/:id` | Admin | Update beatmap |
| DELETE | `/api/v1/beatmaps/:id` | Admin | Delete beatmap |

### Rooms

| Method | Endpoint | Auth | Description |
|--------|----------|------|-------------|
| GET | `/api/v1/rooms` | Yes | List rooms (optional `?type=`) |
| GET | `/api/v1/rooms/:id` | Yes | Room details |
| GET | `/api/v1/rooms/:code` | No | Room by invite code |
| POST | `/api/v1/rooms` | Yes | Create room |
| PATCH | `/api/v1/rooms/:id/metadata` | Admin | Replace room metadata (full state; omitted optionals are cleared) |
| PUT | `/api/v1/rooms/:id/metadata` | Admin | Incremental metadata update (only provided fields change) |
| PATCH | `/api/v1/rooms/:id/strategists` | Yes | Set strategists |
| PATCH | `/api/v1/rooms/:id/streamer` | Yes | Set streamer |
| PATCH | `/api/v1/rooms/:id/referee` | Admin | Assign formal room referee |
| PATCH | `/api/v1/rooms/:id/mappool` | Yes | Set room mappool |
| PATCH | `/api/v1/rooms/:id/bp-order` | Yes | Set first pick/ban |
| PATCH | `/api/v1/rooms/:id/players` | Yes | Set team rosters |
| PATCH | `/api/v1/rooms/:id/mp-link` | Yes | Set multiplayer link |
| PATCH | `/api/v1/rooms/:id/stream-link` | Yes | Set stream link |
| POST | `/api/v1/rooms/:id/start-match` | Yes | Start match from room |

### Matches

| Method | Endpoint | Auth | Description |
|--------|----------|------|-------------|
| GET | `/api/v1/matches` | Yes | List matches (optional `?status=`) |
| GET | `/api/v1/matches/:id` | Yes | Match details |
| GET | `/api/v1/matches/:code` | No | Match by code |
| GET | `/api/v1/matches/:id/moves` | Yes | Paginated moves |
| GET | `/api/v1/matches/:id/moves/latest` | Yes | Latest moves (`?limit=50`) |
| POST | `/api/v1/matches/:id/end` | Yes | End match |
| POST | `/api/v1/matches/:id/advance-turn` | Yes | Advance turn |
| GET | `/api/v1/matches/:id/win-condition` | Yes | Check winner |

### Announcements

| Method | Endpoint | Auth | Description |
|--------|----------|------|-------------|
| GET | `/api/v1/announcements` | No | List visible announcements |
| GET | `/api/v1/announcements/:id` | No | Announcement details |
| POST | `/api/v1/announcements` | Admin | Create announcement |
| PUT | `/api/v1/announcements/:id` | Admin | Update announcement |
| DELETE | `/api/v1/announcements/:id` | Admin | Delete announcement |
| POST | `/api/v1/announcements/:id/publish` | Admin | Publish announcement |

## GraphQL

The backend exposes a GraphQL endpoint at `/graphql` powered by `github.com/99designs/gqlgen`.

- **Playground**: `GET /graphql`
- **Queries / Mutations**: `POST /graphql` with `Authorization: Bearer <jwt>` for protected fields

The schema is defined in `schema.graphql` at the project root. Key capabilities:

- **Query**: `ping`, `me`, `user`, `users`, `beatmap`, `beatmapByOsuId`, `beatmaps`, `room`, `roomByCode`, `rooms`, `match`, `matchByCode`, `matches`, `announcement`, `announcements`.
- **Match nested fields**: `moves`, `recentMove`, `room`, plus role-gated views `strategistView`, `spectatorView`, `overlayView`, `refereeView`.
- **Mutation** (in-game commands): `banPoolSlot`, `placePiece`, `completeRobbery`, `declareTbWinner`, `declareSurrender`, `advanceTurn`, `pauseMatch`, `resumeMatch`, and others.

Response types use `*Page` (e.g. `UserPage`, `MatchPage`) with `items`, `page`, `perPage`, `total`, `totalPages`.

The REST endpoints listed above remain the canonical path for room setup and admin CRUD operations; GraphQL is used for reads, views, and in-match commands.

## WebSocket / Real-time (Planned)

A WebSocket endpoint will be available at `/ws` for real-time board synchronization.

Connection handshake:

1. Connect to `wss://host/ws` with `Authorization: Bearer <jwt>`.
2. Send a `JOIN` message to subscribe to a room or match channel.
3. The server broadcasts state changes to all subscribers.

Planned message types:

- `JOIN` — subscribe to a room/match
- `LEAVE` — unsubscribe
- `MOVE` — a ban/pick/rob/win action
- `BOARD_STATE` — full board state broadcast
- `TURN_UPDATE` — active team/phase/timer update
- `SYSTEM` — server notifications
- `ANNOUNCEMENT` — new CMS announcement

Redis pub/sub will be used to fan out messages across multiple backend instances.

## Role-Based Access

Global roles (stored in JWT):

- `player` — default logged-in user
- `strategist` — can act on behalf of a team in a match
- `referee` — can control matches
- `streamer` — read-only access to streaming data
- `admin` — full access

Room-scoped roles (stored per room/match):

- `admin` — room owner / referee
- `strategist` — assigned team strategist
- `streamer` — match streamer
- `spectator` — viewer

## Common Patterns for Frontend

1. **Always send JWT on protected routes.** Missing tokens return `401`.
2. **Use `code` for public lookups.** Rooms and matches have short human-readable codes for sharing.
3. **Poll then upgrade.** Until WebSocket is ready, poll `GET /api/v1/matches/:id/moves/latest` for live updates.
4. **Admin-only mutations require the `admin` role.** Attempting admin actions without it returns `403`.
5. **Room setup order.** Create room → set strategists → set BP order → set players → set MP link → start match.

## Local Development

```bash
# Start dependencies
docker-compose up -d

# Initialize database
make initdb-seed

# Run server
make run
```

Default local URLs:

- API: `http://localhost:8090`
- Health: `http://localhost:8090/health`

## Environment Variables

Key variables for frontend behavior:

| Variable | Description |
|----------|-------------|
| `PORT` | Backend port (default `8090`) |
| `OSU_REDIRECT_URI` | Must match osu! OAuth app settings |
| `ALLOWED_ORIGINS` | Frontend origin for CORS |

See `.env.example` for the full list.

## Notes for AI Agents

- When generating frontend code, assume the backend returns the unified `{"success", "data"}` envelope.
- Use `Authorization: Bearer <token>` for protected calls.
- Treat IDs carefully: the MongoDB ObjectID is exposed as `id` on `User`, `Room`, `Match`, `Announcement`, and `Beatmap`. Beatmap also exposes `onlineId` for the osu! beatmap id.
- Prefer REST for room setup and admin CRUD operations; use GraphQL for reads, match views, and in-match commands.
- Do not hardcode admin credentials; rely on osu! OAuth and role assignment.
