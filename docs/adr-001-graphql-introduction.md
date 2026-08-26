# ADR: rctHubBackend 是否引入 GraphQL

> **状态**: 提案
> **日期**: 2026-07-24
> **决策者**: 项目技术负责人
> **关联文档**: RCTS1 平台系统开发文档

---

## 1. 背景与问题

rctHubBackend 当前基于 Go + Gin + MongoDB + Redis 构建，已实现 38 个 REST 端点，覆盖用户管理、房间管理、比赛配置、落子记录等核心功能。项目 README 已将"接入 gqlgen 提供 GraphQL 接口"列为下一步计划，`internal/graphql/` 目录已创建但为空，`graphql.config.yml` 已配置但 `schema.graphql` 尚不存在。

RCTS1 开发文档描述了一个复杂的实时赛事系统，具有以下特征：

- **四类客户端**（裁判、策略师、观众、OBS Overlay），各自需要不同的数据视图
- **深度嵌套的领域模型**（Match → Board → Pieces → PoolSlots → Beatmaps → Teams）
- **严格的 Command/Event 模式**，所有状态变更需经过验证、版本控制、审计
- **强实时性要求**，棋盘状态需通过 WebSocket 同步
- **文档 §9.10 明确要求 Read Model 模块**，将领域状态转换为不同客户端需要的数据

核心问题：**是否需要引入 GraphQL，以及如何与现有 REST 和计划中的 WebSocket 共存？**

---

## 2. 现状评估

### 2.1 已有 REST 端点的局限

| 问题 | 具体表现 |
|------|----------|
| **过度获取 (Over-fetching)** | `GET /matches/:id` 返回完整 Match 对象，但观众只需要棋盘和比分，策略师还需要可执行操作列表 |
| **欠获取 (Under-fetching)** | 获取比赛完整状态需要 `GET /matches/:id` + `GET /matches/:id/moves` + `GET /rooms/:id` 多次往返 |
| **视图耦合** | 每个 handler 返回固定的 JSON 结构，无法按客户端角色裁剪字段 |
| **前端类型安全** | REST 响应类型需手动维护，与后端 domain 模型容易脱节 |
| **文档 §9.10 难以实现** | Read Model 要求"将领域状态转换为不同客户端需要的数据"，REST 做法是为每类客户端建独立端点，端点数量膨胀 |

### 2.2 现有架构优势（不应破坏）

- 分层清晰：handler → service → repository → domain，每层接口解耦
- Repository 接口模式，便于测试和替换
- 统一响应格式 `{"success": bool, "data"|"error": ...}`
- RBAC 三级控制（公开 / 认证 / 管理员）
- Service 层已封装全部业务逻辑，GraphQL resolver 可直接复用

---

## 3. GraphQL 适用性分析

### 3.1 支持引入的理由

#### 3.1.1 多客户端异构数据需求（核心驱动力）

文档 §15 定义了四类前端视图，数据需求差异显著：

| 客户端 | 需要的数据 | REST 方案 | GraphQL 方案 |
|--------|-----------|-----------|-------------|
| 策略师 | 棋盘 + 可选格子 + 可选棋子 + Timer + 当前行动方 + 允许操作 | 需要专门端点或返回全量后前端过滤 | 一个查询，按需取字段 |
| 观众 | 棋盘 + 比分 + 当前阶段 + 最近操作 | 需要专门端点 | 一个查询，只取公开字段 |
| OBS Overlay | 棋盘 + 比分 + 动画事件 | 需要专门端点 | 一个查询，只取渲染所需字段 |
| 裁判 | 全部比赛数据 + 审计日志 + 成员列表 + 配置 | 需要多个端点组合 | 一个查询，嵌套获取 |

GraphQL 让每个客户端**用一条查询表达自己的数据需求**，无需后端为每类客户端维护独立端点。

#### 3.1.2 嵌套查询消除往返

当前获取"一场比赛的完整可渲染状态"需要：

```
GET /api/v1/matches/:id           → 比赛基本信息 + 棋盘 + 图池
GET /api/v1/matches/:id/moves     → 落子历史
GET /api/v1/rooms/:id             → 房间配置 + 队伍信息
GET /api/v1/beatmaps/:id (×N)     → 每个图池槽位的谱面详情
```

GraphQL 一条查询即可：

```graphql
query MatchDashboard($id: ID!) {
  match(id: $id) {
    phase
    activeTeam
    version
    board { cells { position zone piece { state owner } } }
    pool { slots { mod index beatmap { title artist coverUrl } state } }
    teams { red { name strategist { username avatarUrl } } blue { ... } }
    timer { startedAt durationSeconds }
    recentMoves(limit: 5) { type teamSide createdAt }
  }
}
```

#### 3.1.3 Read Model 模块的自然映射

文档 §9.10 的 Read Model 要求将领域状态转换为客户端视图。GraphQL 的 Schema 本身就是 Read Model 的声明：

- `Match.strategistView` → 返回 `allowedActions`, `selectablePoolSlots`, `selectableBoardCells`
- `Match.spectatorView` → 返回 `board`, `scores`, `currentPhase`
- `Match.overlayView` → 返回渲染所需的精简字段

字段级 resolver 可按角色裁剪，实现文档要求的"策略师端不需要收到所有内部字段"。

#### 3.1.4 前端类型安全与开发效率

gqlgen 自动生成 Go 类型，配合前端 codegen（如 `graphql-codegen`）可生成 TypeScript 类型。前后端共享同一份 `schema.graphql` 作为契约，减少接口对齐成本。

#### 3.1.5 项目已有铺垫

`internal/graphql/` 目录、`graphql.config.yml`、README 均表明团队已计划使用 gqlgen。引入是顺应既有规划，而非临时起意。

### 3.2 需要警惕的风险

| 风险 | 严重度 | 缓解措施 |
|------|--------|----------|
| **N+1 查询** | 高 | 使用 DataLoader 批量加载关联实体（如 beatmap by ID） |
| **学习曲线** | 中 | gqlgen 在 Go 生态成熟，resolver 模式与现有 handler 类似 |
| **权限复杂度** | 中 | 字段级鉴权需仔细设计，建议用 directive 统一处理 |
| **与 WebSocket 职责重叠** | 中 | 明确边界：GraphQL 只做查询和变更，实时推送走 WebSocket |
| **查询深度/复杂度攻击** | 中 | 配置 `max_depth` 和 `complexity_limit` |
| **团队规模有限** | 中 | 渐进式引入，Phase 0-1 可在 1 周内完成，不阻塞现有开发 |

### 3.3 不适合用 GraphQL 的场景

以下功能应保留在 REST，不应迁入 GraphQL：

- **osu! OAuth 回调** — 依赖 HTTP 302 重定向，GraphQL 无法表达
- **文件上传** — GraphQL multipart 扩展复杂度高于 REST multipart
- **健康检查** — 运维探针需要简单的 HTTP 200
- **OBS Overlay 初始状态** — 可用 GraphQL，但实时更新必须走 WebSocket

---

## 4. 决策：引入 GraphQL，与 REST 和 WebSocket 三通道共存

### 4.1 核心结论

**有必要引入 GraphQL，但定位为 REST 的补充而非替代。**

GraphQL 承担"复杂读取 + 客户端视图 + 类型化变更"，REST 保留"OAuth / 上传 / 运维 / 简单 CRUD"，WebSocket 负责"实时状态推送"。三者共享现有 Service 层。

### 4.2 职责边界

```
REST (Gin)                    GraphQL (gqlgen)              WebSocket Gateway
─────────────────             ──────────────────             ─────────────────
osu! OAuth 回调                Query: 复杂嵌套读取            棋盘状态广播
健康检查                       Mutation: Command 变更          Timer Tick 推送
文件上传                       字段级裁剪 (Read Model)         Domain Event 推送
管理后台 CRUD                  前端类型安全契约               断线重连 + 版本恢复
简单 PATCH (设置策略师等)                                     在线状态
```

### 4.3 关键设计约束

1. **GraphQL resolver 只调用 Service 层**，不直接访问 Repository 或数据库，与现有 handler 保持一致
2. **Mutation 复用现有 Service 方法**，不引入新的业务逻辑分支
3. **expectedVersion 乐观锁在 Mutation input 中传递**，与 REST 行为一致
4. **错误码统一映射**，文档 §19 定义的错误码作为 GraphQL errors 的 extensions.code
5. **认证复用现有 JWT**，通过 HTTP header `Authorization: Bearer <jwt>` 传入

---

## 5. 实现路线规划

### Phase 0: 脚手架搭建（1-2 天）

**目标**: gqlgen 可运行，`/graphql` 端点可访问 GraphiQL playground

**步骤**:

1. 安装 gqlgen 依赖
   ```bash
   go get github.com/99designs/gqlgen
   go run github.com/99designs/gqlgen init
   ```

2. 编写初始 `schema.graphql`（仅核心类型 + hello world 查询）

3. 配置 `gqlgen.yml`:
   ```yaml
   schema: schema.graphql
   exec:
     filename: internal/graphql/generated.go
   model:
     filename: internal/graphql/models_gen.go
   resolver:
     filename: internal/graphql/resolver.go
     type: Resolver
   ```

4. 在 `internal/server/server.go` 注册 GraphQL handler:
   ```go
   // /graphql — GraphQL endpoint (with playground)
   gqlHandler := handler.NewDefaultServer(graphql.NewExecutableSchema(...))
   s.router.POST("/graphql", gqlHandler)
   s.router.GET("/graphql", gqlHandler) // playground
   ```

5. 验证：访问 `http://localhost:8090/graphql` 看到 GraphiQL 界面

**验收标准**: `query { ping }` 返回 `"pong"`

---

### Phase 1: 只读查询（3-5 天）

**目标**: 将现有 GET 端点的查询能力迁移到 GraphQL，支持嵌套查询

**Schema 覆盖范围**:

```graphql
type Query {
  match(id: ID!): Match
  matchByCode(code: String!): Match
  matches(status: MatchStatus, page: Int, perPage: Int): MatchPage!
  room(id: ID!): Room
  roomByCode(code: String!): Room
  rooms(type: RoomType, page: Int, perPage: Int): RoomPage!
  beatmaps(page: Int, perPage: Int): BeatmapPage!
  beatmap(id: ID!): Beatmap
  beatmapByOsuId(osuId: Int!): Beatmap
  announcements: [Announcement!]!
  announcement(id: ID!): Announcement
  me: User
  user(id: ID!): User
  users(page: Int, perPage: Int): UserPage!
}
```

**嵌套关系**:

```graphql
type Match {
  id: ID!
  code: String!
  status: MatchStatus!
  phase: MatchPhase
  activeTeam: TeamSide
  version: Int!
  board: Board
  pool: Mappool
  teams: MatchTeams
  timer: TimerState
  turnState: TurnState
  moves(limit: Int): [Move!]!        # 嵌套查询落子历史
  recentMove: Move                     # 便捷字段
  result: MatchResult
  room: Room                           # 嵌套查询房间
  createdAt: Time!
  startedAt: Time
  finishedAt: Time
}
```

**Resolver 实现**:

```go
func (r *matchResolver) Board(ctx context.Context, obj *domain.Match) (*domain.Board, error) {
    return &obj.Board, nil  // 内嵌字段，直接返回
}

func (r *matchResolver) Moves(ctx context.Context, obj *domain.Match, limit *int) ([]*domain.Move, error) {
    lim := 50
    if limit != nil { lim = *limit }
    return r.services.Matchs.ListMoves(ctx, obj.ID, lim)
}

func (r *matchResolver) Room(ctx context.Context, obj *domain.Match) (*domain.Room, error) {
    // DataLoader 批量加载，避免 N+1
    return r.roomLoader.Load(ctx, obj.RoomID)
}
```

**DataLoader 引入**:

```go
// internal/graphql/dataloader.go
func (r *Resolver) beatmapLoader() *dataloader.Loader {
    return dataloader.NewBatchLoader(func(ctx context.Context, keys []dataloader.Key) []*dataloader.Result {
        ids := /* extract IDs */
        beatmaps, _ := r.services.Beatmaps.ListByIDs(ctx, ids)
        return /* map to results */
    })
}
```

**验收标准**: 一条 GraphQL 查询可取回比赛 + 棋盘 + 图池 + 落子历史 + 房间信息

---

### Phase 2: 客户端视图 + Read Model（5-7 天）

**目标**: 实现文档 §9.10 Read Model，为不同客户端提供裁剪后的视图

**新增 Schema**:

```graphql
type Match {
  # ... 基础字段 ...

  # 客户端专属视图 (字段级鉴权)
  strategistView: StrategistView     @requireRole(role: STRATEGIST)
  spectatorView: SpectatorView
  overlayView: OverlayView
  refereeView: RefereeView           @requireRole(role: REFEREE, admin: true)
}

type StrategistView {
  isMyTurn: Boolean!
  allowedActions: [String!]!
  selectablePoolSlots: [String!]!
  selectableBoardCells: [String!]!
  timer: TimerInfo!
}

type SpectatorView {
  board: BoardSummary!
  scores: TeamScores!
  currentPhase: String!
  activeTeam: TeamSide
}

type OverlayView {
  board: BoardRenderData!
  scores: TeamScores!
  timer: TimerDisplay!
  lastEvent: DomainEvent
}
```

**Directive 鉴权**:

```graphql
directive @requireRole(role: UserRole!, admin: Boolean = false) on FIELD_DEFINITION
```

```go
// internal/graphql/directive_require_role.go
func (d *directiveRoot) RequireRole(ctx context.Context, obj interface{}, next graphql.Resolver, role string, admin bool) (interface{}, error) {
    user := middleware.GetUserFromContext(ctx)
    if user == nil {
        return nil, gqlerror.New("AUTH_REQUIRED")
    }
    if admin && !user.HasRole(domain.RoleAdmin) {
        return nil, gqlerror.New("AUTH_REQUIRED")
    }
    if !user.HasRole(domain.UserRole(role)) {
        return nil, gqlerror.New("ACTION_NOT_ALLOWED")
    }
    return next(ctx)
}
```

**Read Model 转换器**:

```go
// internal/graphql/readmodel.go
type ReadModelService struct {
    matchService *service.MatchService
}

func (s *ReadModelService) StrategistView(ctx context.Context, match *domain.Match, userID int64) (*StrategistView, error) {
    view := &StrategistView{}
    view.IsMyTurn = s.matchService.IsUserTurn(ctx, match, userID)
    view.AllowedActions = s.matchService.AllowedActions(ctx, match, userID)
    view.SelectablePoolSlots = s.matchService.SelectableSlots(ctx, match)
    view.SelectableBoardCells = s.matchService.SelectableCells(ctx, match)
    view.Timer = s.toTimerInfo(match.Timer)
    return view, nil
}
```

**验收标准**: 策略师查询返回 `allowedActions`，观众查询不返回该字段

---

### Phase 3: Command Mutations（5-8 天）

**目标**: 将文档 §12.1 定义的 Command 映射为 GraphQL Mutation

**Schema**:

```graphql
input CommandInput {
  matchId: ID!
  expectedVersion: Int!
  commandId: String!
}

type Mutation {
  banPoolSlot(input: BanPoolSlotInput!): BanPoolSlotResult!
  unbanPoolSlot(input: UnbanPoolSlotInput!): UnbanPoolSlotResult!
  placePiece(input: PlacePieceInput!): PlacePieceResult!
  grantWinPermission(input: GrantWinInput!): GrantWinResult!
  confirmPieceWinner(input: ConfirmWinnerInput!): ConfirmWinnerResult!
  beginRobbery(input: BeginRobberyInput!): BeginRobberyResult!
  completeRobbery(input: CompleteRobberyInput!): CompleteRobberyResult!
  cancelRobbery(input: CancelRobberyInput!): CancelRobberyResult!
  declareTbWinner(input: DeclareTbInput!): DeclareTbResult!
  declareSurrender(input: SurrenderInput!): SurrenderResult!
  undoAction(input: UndoInput!): UndoResult!
  advanceTurn(input: CommandInput!): AdvanceTurnResult!
  pauseMatch(input: CommandInput!): PauseResult!
  resumeMatch(input: CommandInput!): ResumeResult!
}

type PlacePieceResult {
  success: Boolean!
  match: Match
  events: [DomainEvent!]!
  error: MatchError
}
```

**Mutation 实现（复用现有 Service）**:

```go
func (r *mutationResolver) PlacePiece(ctx context.Context, input PlacePieceInput) (*PlacePieceResult, error) {
    user := middleware.GetUserFromContext(ctx)
    if user == nil {
        return nil, gqlerror.New("AUTH_REQUIRED")
    }

    // 复用现有 MatchService.Ban / Pick / Rob 逻辑
    cmd := service.PlacePieceCommand{
        MatchID:        input.MatchID,
        ExpectedVersion: input.ExpectedVersion,
        CommandID:      input.CommandID,
        Slot:           input.Slot,
        Position:       input.Position,
        ActorID:        user.OnlineID,
    }

    result, err := r.services.Matchs.PlacePiece(ctx, cmd)
    if err != nil {
        return errorToResult(err), nil  // 统一错误码映射
    }

    return &PlacePieceResult{
        Success: true,
        Match:   result.Match,
        Events:  result.Events,
    }, nil
}
```

**错误码映射**（对应文档 §19）:

```go
func errorToResult(err error) *PlacePieceResult {
    code := mapErrorToCode(err)
    return &PlacePieceResult{
        Success: false,
        Error: &MatchError{Code: code, Message: errorMessage(code)},
    }
}

func mapErrorToCode(err error) string {
    switch {
    case errors.Is(err, pkgerrs.ErrUnauthorized):
        return "AUTH_REQUIRED"
    case errors.Is(err, pkgerrs.ErrForbidden):
        return "ACTION_NOT_ALLOWED"
    // ... 文档 §19 全部错误码
    default:
        return "INTERNAL_ERROR"
    }
}
```

**验收标准**: Mutation 调用经过完整的验证→计算→持久化→广播流程

---

## 6. GraphQL Subscriptions 评估（暂不引入）

### 6.1 为什么暂不引入

文档对实时性的核心要求（棋盘同步、Timer 校准、断线重连）应通过独立的 WebSocket Gateway 实现，原因：

1. **WebSocket Gateway 需要管理连接池、心跳、重连策略**，这些是传输层职责，与 GraphQL 查询语言正交
2. **gqlgen Subscriptions over WebSocket** 增加了一层协议复杂度，且与 Gin 的集成不如裸 WebSocket 直接
3. **OBS Overlay 场景**需要极低延迟和极简协议，裸 WebSocket JSON 帧更合适
4. **断线重连 + 版本恢复**逻辑（文档 §14.9）更适合自定义协议

### 6.2 未来可选方案

如果后续发现"WebSocket 推送的数据格式与 GraphQL Schema 不一致导致前端维护两套类型"，可考虑：

- GraphQL Subscriptions over WebSocket（使用 `github.com/99designs/gqlgen/graphql/handler/transport/websocket`）
- 或在 WebSocket 帧中直接嵌入 GraphQL 查询结果片段

但这属于优化，不阻塞首发版本。

---

## 7. Schema 设计要点

### 7.1 类型映射

| Go domain 类型 | GraphQL 类型 | 说明 |
|---------------|-------------|------|
| `bson.ObjectID` | `ID!` | 统一用 ObjectID hex |
| `time.Time` | `Time` (自定义标量) | ISO 8601 字符串 |
| `*int64` | `Int` (nullable) | 指针 → nullable |
| `[]int64` | `[Int!]!` | 切片 → non-null list |
| `TeamSide` (string enum) | `enum TeamSide` | GraphQL enum |
| `MatchStatus` (string enum) | `enum MatchStatus` | GraphQL enum |
| `Board` (struct) | `type Board` | 含 `cells: [[Cell!]!]!` |
| `map[PieceMod][]Piece` | `type Mappool { slots: [PoolSlotGroup!]! }` | map → list (GraphQL 无 map) |

### 7.2 分页

统一使用 Relay-style 连接或简化的 `Page<T>` 类型：

```graphql
type MatchPage {
  items: [Match!]!
  page: Int!
  perPage: Int!
  total: Int!
  totalPages: Int!
}
```

与现有 `pkg/paginate.Result` 保持一致。

### 7.3 枚举同步

Go domain 中的枚举（`MatchStatus`, `TeamSide`, `PieceMod`, `MoveType` 等）需在 `schema.graphql` 中定义为 GraphQL enum。建议用 `gqlgen.yml` 的 `models` 配置将 GraphQL enum 映射到 Go 类型，避免手动转换。

### 7.4 自定义标量

```graphql
scalar Time     # time.Time
scalar JSON     # map[string]interface{} (用于 commandPayload 等动态字段)
scalar ObjectID # bson.ObjectID
```

---

## 8. gqlgen 集成步骤详解

### 8.1 目录结构（引入后）

```
internal/graphql/
├── generated.go          # gqlgen 自动生成（不手动编辑）
├── models_gen.go         # 自动生成的 Go 类型
├── resolver.go           # 根 Resolver（依赖注入入口）
├── query.resolvers.go    # Query resolver 实现
├── mutation.resolvers.go # Mutation resolver 实现
├── match.resolvers.go    # Match 类型字段 resolver
├── room.resolvers.go     # Room 类型字段 resolver
├── dataloader.go         # DataLoader 批量加载器
├── directives.go         # 自定义 directive (@requireRole)
├── readmodel.go          # Read Model 转换逻辑
└── errors.go             # 错误码映射

schema.graphql            # GraphQL Schema 定义（项目根目录）
gqlgen.yml                # gqlgen 配置（项目根目录）
```

### 8.2 依赖注入

GraphQL Resolver 通过 `server.Deps` 注入，与现有 handler 一致：

```go
// internal/graphql/resolver.go
type Resolver struct {
    services *service.Services
    repos    *repository.Repositories
    jwtSigner *jwtutil.Signer
    loaders   *LoaderRegistry
}

func NewResolver(deps *server.Deps) *Resolver {
    return &Resolver{
        services:  deps.Services,
        repos:     deps.Repos,
        jwtSigner: deps.JWTSigner,
        loaders:   NewLoaderRegistry(deps.Services),
    }
}
```

### 8.3 认证中间件

GraphQL endpoint 复用现有 JWT 认证：

```go
// internal/server/server.go
gqlHandler := handler.NewDefaultServer(executableSchema)
gqlHandler = handlerMiddleware(gqlHandler, middleware.Auth(deps.JWTSigner))
s.router.POST("/graphql", gqlHandler)
```

### 8.4 开发工具链

| 工具 | 用途 |
|------|------|
| `gqlgen` | Go 后端代码生成 |
| `graphql-codegen` (TS) | 前端 TypeScript 类型生成 |
| GraphiQL / Apollo Sandbox | 开发期调试 |
| `graphql.config.yml` | IDE 集成（VSCode GraphQL 插件） |

---

## 9. 风险与缓解

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|----------|
| N+1 查询导致性能退化 | 高 | 中 | Phase 1 即引入 DataLoader |
| Schema 膨胀难以维护 | 中 | 中 | 按领域拆分 `.graphql` 文件，gqlgen 支持 `# import` |
| REST 与 GraphQL 行为不一致 | 中 | 高 | Mutation 强制走 Service 层，禁止 resolver 直接操作数据库 |
| 查询复杂度攻击 | 低 | 高 | 配置 `max_depth: 10`, `complexity_limit: 1000` |
| 团队学习成本 | 中 | 中 | Phase 0 最小化，先跑通再迭代 |

---

## 10. 总结

| 维度 | 结论 |
|------|------|
| **是否引入** | 是，作为 REST 的补充 |
| **引入时机** | WebSocket Gateway 之前 — GraphQL 先解决"读"的问题 |
| **引入方式** | 渐进式，Phase 0 → 3，每阶段可独立交付 |
| **与 REST 关系** | 共存，REST 保留 OAuth/上传/运维，GraphQL 承担复杂查询和变更 |
| **与 WebSocket 关系** | 互补，GraphQL 做查询/变更，WebSocket 做实时推送 |
| **技术选型** | gqlgen（Go 生态最成熟的 GraphQL 实现） |
| **预估总工期** | 14-22 天（四阶段合计） |
| **首阶段可交付** | Phase 0 + 1 约需 4-7 天，即可替代大部分 GET 端点的查询场景 |
