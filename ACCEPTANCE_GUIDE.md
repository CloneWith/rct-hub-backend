# RCT Hub 后端 M7 本机人工验收手册

本文交给负责 M7 人工验收的协作者使用。目标不是再次证明纯规则引擎，而是确认真实后端在浏览器、MongoDB、Redis、osu! 和 Bancho 环境中能够完整工作。

请使用非生产数据库和主办方持有的测试凭据。文中的 Client Secret、IRC password、Cookie 和用户资料不得提交到 Git。

## 1. 本次验收的边界

本次必须证明：

- 真实 osu! OAuth 能建立 HttpOnly 浏览器会话。
- 裁判、红方策略师兼队长、蓝方策略师兼队长、观众可以同时使用同一场比赛。
- 正式比赛的 REST 配置、GraphQL 查询和命令使用同一份权威状态。
- 页面刷新、WebSocket 断线、重连和后端重启不会丢失比赛进度。
- 重复命令、旧版本命令和无权限操作会被正确处理。
- 谱面资料在后台刷新，失败可见且可重试，不会改变比赛版本。
- Bancho 离线时人工操作仍可继续；连接真实受控房间后，地图命令、确认和裁判审核可工作。
- 比赛最终状态和审计记录在重启后仍然存在。

当前 `rct-hub` Web 仓库还没有正式比赛页面和 WebSocket 比赛客户端，登录回调也仍按旧式 URL token 编写。因此本轮使用浏览器中的 GraphQL Playground、开发者控制台和真实 Cookie 完成浏览器合同验收。它不能替代未来的页面布局、交互和可用性验收。

## 2. 验收前需要准备什么

### 2.1 本机软件

- Docker Engine 或 Docker Desktop，包含 Compose v2。
- 仓库 `.go-version` 指定的 Go 版本。
- curl。
- Chrome 或其他可以建立多个独立用户配置的浏览器。

先检查：

```bash
docker version
docker compose version
go version
curl --version
```

### 2.2 osu! 账号和赛事素材

至少准备四个不同的真实账号：

| 身份 | 本地权限 | 房间身份 |
| --- | --- | --- |
| 管理员 | `admin`、`verified` | 房间创建者和裁判 |
| 红方 | `player`、`strategist`、`verified` | 红方策略师兼队长 |
| 蓝方 | `player`、`strategist`、`verified` | 蓝方策略师兼队长 |
| 观众 | `player`、`verified` | 只读观众 |

还需要：

- 红蓝双方各 8 个不重复的正 osu! ID，共 16 个。
- 两名队长必须出现在各自的 8 人名单中。
- 一个有效普通谱面 BID。
- 一个有效 TB BID。
- 一个确定不存在的正 BID，用来检查元数据失败。
- 一个由测试人员控制的 osu! 多人房间。
- 连接该房间且有权执行 `!mp map` 的 Bancho IRC 账号。

只有红方、蓝方、裁判和观众需要实际登录。若还要验收开赛时自动邀请全部 16 人，则这 16 人都必须已经存在于本地 `users` 集合，并带有真实用户名；否则邀请规划会进入 `automationIssues`，但后续地图命令仍可单独验收。

## 3. 建立独立的本地配置

不要覆盖协作者日常使用的 `.env`。复制示例配置：

```bash
cp .env.example .env.m7
export ENV_FILE=.env.m7
```

PowerShell 使用：

```powershell
Copy-Item .env.example .env.m7
$env:ENV_FILE = '.env.m7'
```

编辑 `.env.m7`：

```env
APP_ENV=development
PORT=8080
FRONTEND_URI=http://localhost:3000
LOG_LEVEL=info

MONGODB_URI=mongodb://localhost:27017/?replicaSet=rs0&directConnection=true
MONGODB_NAME=rcthub_m7_acceptance

REDIS_ADDR=localhost:6379
REDIS_PASSWORD=
REDIS_DB=0

JWT_SECRET=<至少32字节的随机值>
AUTH_COOKIE_NAME=rcthub_session
AUTH_COOKIE_DOMAIN=
AUTH_COOKIE_SECURE=false
AUTH_COOKIE_SAME_SITE=lax
AUTH_SESSION_IDLE_HOURS=24
AUTH_SESSION_MAX_HOURS=168

OSU_CLIENT_ID=<真实测试应用Client ID>
OSU_CLIENT_SECRET=<真实测试应用Client Secret>
OSU_REDIRECT_URI=http://localhost:8080/auth/osu/callback
OSU_API_BASE=https://osu.ppy.sh

ALLOWED_ORIGINS=http://localhost:3000

BANCHO_IRC_ADDR=
BANCHO_IRC_USERNAME=
BANCHO_IRC_PASSWORD=
BANCHO_IRC_CHANNEL=
```

在 [osu! 账号设置的 OAuth 页面](https://osu.ppy.sh/home/account/edit#oauth)创建测试应用。Callback URL 必须与 `OSU_REDIRECT_URI` 完全一致。

此时故意让 Bancho 配置为空。第 11 节先验收离线行为，再启用真实 IRC。

## 4. 启动依赖并完成自动检查

从仓库根目录执行：

```bash
docker compose up -d --wait
go run ./tools/verify
go test ./...
MONGODB_TEST_URI='mongodb://localhost:27017/?replicaSet=rs0&directConnection=true' \
  go test -count=1 -run '^TestMongoIntegration' -v ./internal/persistence
REDIS_TEST_ADDR='localhost:6379' \
  go test -count=1 -run '^TestRedisIntegration' -v ./internal/irc
```

PowerShell 的两个集成测试变量写法：

```powershell
$env:MONGODB_TEST_URI = 'mongodb://localhost:27017/?replicaSet=rs0&directConnection=true'
go test -count=1 -run '^TestMongoIntegration' -v ./internal/persistence
$env:REDIS_TEST_ADDR = 'localhost:6379'
go test -count=1 -run '^TestRedisIntegration' -v ./internal/irc
```

所有命令必须通过。不要用 `initdb-seed`，其中是假的 osu! ID 和休闲房间。

在管理员第一次 OAuth 登录前初始化数据库并创建管理员：

```bash
go run ./cmd/initdb -admin-id=<管理员真实osuID> -admin-name='<管理员用户名>'
```

如果这个 osu! ID 已经登录并写入数据库，`initdb` 会发现重复 ID 后跳过，不会自动把他升级为管理员。遇到这种情况，最干净的做法是换一个新的非生产数据库名重新初始化。

在单独终端启动后端，并让它继续运行：

```bash
export ENV_FILE=.env.m7
go run ./cmd/server
```

PowerShell：

```powershell
$env:ENV_FILE = '.env.m7'
go run ./cmd/server
```

访问 `http://localhost:8080/health`。MongoDB、Redis 和后端均健康后再继续。

## 5. 建立四个浏览器身份

分别建立管理员、红方、蓝方和观众四个 Chrome Profile。不要使用多个普通标签页，因为它们会共享 Cookie。

每个 Profile 访问：

```text
http://localhost:8080/auth/osu
```

OAuth 回调最后会跳转到 `http://localhost:3000/auth/callback`。当前 Web 回调页面可能提示缺少 token；这不是本轮的判定依据。后端已在跳转前设置 HttpOnly Cookie。

每个 Profile 随后打开 `http://localhost:8080/graphql`，执行：

```graphql
query CurrentUser {
  me {
    id
    onlineID
    username
    roles
    verifyStatus
    isBanned
  }
}
```

管理员应直接是 `admin + VERIFIED`。新登录的其他账号默认是 `player + PENDING`。

在管理员 Profile 查询用户：

```graphql
query AcceptanceUsers {
  users(page: 1, perPage: 50) {
    items { id onlineID username roles verifyStatus }
  }
}
```

记录红方、蓝方和观众各自的 Mongo `id`。在管理员 Profile 的开发者控制台执行，替换三个 ID：

```js
const patchUser = (id, roles) => fetch("http://localhost:8080/api/v1/users/" + id, {
  method: "PATCH",
  credentials: "include",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ roles, verify_status: "verified" })
}).then(async response => {
  const body = await response.json();
  if (!response.ok) throw new Error(body.error || `HTTP ${response.status}`);
  return body;
});

await patchUser("<RED_MONGO_ID>", ["player", "strategist"]);
await patchUser("<BLUE_MONGO_ID>", ["player", "strategist"]);
await patchUser("<SPECTATOR_MONGO_ID>", ["player"]);
```

权限或审核状态变化会撤销该用户已有的浏览器会话。红方、蓝方和观众都要重新访问 `/auth/osu` 登录，然后再次执行 `CurrentUser` 查询。

通过标准：四个 Profile 均显示正确账号、权限和 `VERIFIED`，没有串号。

## 6. 创建受控房间和正式比赛

先在 osu! 客户端中创建一个多人房间，保留房主权限，并复制官方链接：

```text
https://osu.ppy.sh/community/matches/<正整数房间ID>
```

准备下面这些值：

- `RED_OSU_ID`：红方策略师兼队长。
- `BLUE_OSU_ID`：蓝方策略师兼队长。
- `RED_PLAYERS`、`BLUE_PLAYERS`：各 8 个不重复的数字 ID。
- `VALID_BID`：有效普通 BID。
- `TB_BID`：有效 TB BID。
- `MISSING_BID`：确定不存在的正 BID。
- `MP_LINK`：刚创建的官方房间链接。

在管理员 Profile 的开发者控制台粘贴下面代码。先替换顶部所有值：

```js
const RED_OSU_ID = 111;
const BLUE_OSU_ID = 222;
const RED_PLAYERS = [111, 112, 113, 114, 115, 116, 117, 118];
const BLUE_PLAYERS = [222, 223, 224, 225, 226, 227, 228, 229];
const VALID_BID = 123456;
const TB_BID = 234567;
const MISSING_BID = 999999999;
const MP_LINK = "https://osu.ppy.sh/community/matches/12345678";

const api = async (method, path, data) => {
  const response = await fetch("http://localhost:8080/api/v1" + path, {
    method,
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: data === undefined ? undefined : JSON.stringify(data)
  });
  const body = await response.json();
  if (!response.ok || !body.success) {
    throw new Error(body.error || `HTTP ${response.status}`);
  }
  return body.data;
};

const piece = beatmap_id => ({ beatmap_id, state: "normal" });
const room = await api("POST", "/rooms", {
  name: "Local M7 Acceptance",
  type: "match"
});
const base = "/rooms/" + room.id;

await api("PATCH", base + "/strategists", {
  red_strategist_user_id: RED_OSU_ID,
  blue_strategist_user_id: BLUE_OSU_ID
});
await api("PATCH", base + "/bp-order", {
  first_pick: "red",
  first_ban: "blue"
});
await api("PATCH", base + "/players", {
  red_leader: RED_OSU_ID,
  blue_leader: BLUE_OSU_ID,
  red_players: RED_PLAYERS,
  blue_players: BLUE_PLAYERS
});
await api("PATCH", base + "/mappool", { mappool: { slots: {
  NM: [piece(VALID_BID), piece(VALID_BID), piece(VALID_BID), piece(VALID_BID)],
  HD: [piece(VALID_BID), piece(VALID_BID), piece(VALID_BID), piece(VALID_BID)],
  HR: [piece(VALID_BID), piece(VALID_BID), piece(VALID_BID), piece(VALID_BID)],
  DT: [piece(VALID_BID), piece(VALID_BID), piece(VALID_BID), piece(VALID_BID)],
  FM: [piece(VALID_BID), piece(VALID_BID), piece(VALID_BID), piece(MISSING_BID)],
  Shiro: [{ state: "normal" }],
  TB: [piece(TB_BID)]
}}});
await api("PATCH", base + "/mp-link", { mp_link: MP_LINK });

const match = await api("POST", base + "/start-match");
console.log({ roomId: room.id, matchId: match.id });
```

保存控制台输出的 `roomId` 和 `matchId`。REST 的 `start-match` 只创建 `READY` 状态，真正开赛仍由裁判执行 GraphQL `startMatch`。

正式房间必须满足：每队恰好 8 人、16 个 ID 全部不同、队长在名单中、恰好一个 Shiro、恰好一个 TB、谱池初始状态正常、官方 MP 链接有效。计时器不需要配置，正式比赛固定使用 RCTS1 标准计时。

## 7. 查询比赛状态和当前合法操作

任何角色都可以用下面查询读取公开权威状态：

```graphql
query MatchState($id: ID!) {
  match(id: $id) {
    id
    pool {
      poolSlotID
      beatmapID
      metadataStatus
      metadataAttempts
      metadataNextRetryAt
      metadataLastError
      beatmap { title artist difficultyName }
    }
    snapshot {
      version
      lifecycle
      phase
      turn
      activeTeam
      pendingPieceID
      wonCounts { red blue }
      robberyUsed { red blue }
      teamPauseUsed { red blue }
      timer {
        startedAt
        durationMilliseconds
        paused
        remainingAtPauseMilliseconds
      }
      board {
        cells {
          cell row col zone
          piece {
            id sourcePoolSlotID mod forceMod selectedBy owner outcome
          }
        }
      }
      winner
      result {
        winner reason surrenderingTeam confirmingPlayerIDs
        wonCounts { red blue }
      }
    }
  }
}
```

变量：

```json
{ "id": "<MATCH_ID>" }
```

红蓝策略师分别查询自己的合法操作：

```graphql
query StrategistState($id: ID!) {
  match(id: $id) {
    snapshot { version lifecycle phase turn activeTeam }
    strategistView {
      isMyTurn
      myTeam
      analysis {
        allowedActions
        banPoolSlotIDs
        legalPlacements { poolSlotID cell forceMod }
        shiroCells
        robberyPlans { targetPieceID sacrificeSets }
        pendingTBRequestID
        canAcceptTBRequest
        canRejectTBRequest
      }
    }
    captainView {
      myTeam
      analysis {
        allowedActions
        pendingTBRequestID
        canAcceptTBRequest
        canRejectTBRequest
      }
    }
  }
}
```

裁判查询：

```graphql
query RefereeState($id: ID!) {
  match(id: $id) {
    refereeView {
      snapshot { version lifecycle phase turn activeTeam }
      analysis { allowedActions }
      automationIssues(limit: 50) {
        eventID sequence eventType attempts lastError occurredAt
      }
      auditLog(limit: 100) {
        actionId sequence commandType previousVersion resultingVersion timestamp reason
        actor { osuID capability team adminOverride refereeOverride }
      }
    }
  }
}
```

观众只查询公开视图：

```graphql
query SpectatorState($id: ID!) {
  match(id: $id) {
    spectatorView {
      board { cells { cell piece { id owner outcome } } }
      wonCounts { red blue }
      currentPhase
      activeTeam
      turnNumber
      lifecycle
    }
  }
}
```

不要人工猜 Shiro、抢夺或 Mod 落点。始终使用 `analysis` 返回的合法选项。

## 8. GraphQL 比赛命令的统一规则

每条比赛命令都包含：

- 当前 `matchId`。
- 查询所得的最新 `expectedVersion`。
- 一个从未使用过的 UUID `commandId`。浏览器控制台可运行 `crypto.randomUUID()` 生成。

建议每次 mutation 都选择这些返回字段：

```graphql
success
commandId
disposition
previousVersion
resultingVersion
currentVersion
error { code message currentVersion }
snapshot { version lifecycle phase turn activeTeam pendingPieceID }
events { eventId sequence resultingVersion type }
```

常用命令及 `input` 形状：

| 操作 | Mutation | 输入结构 |
| --- | --- | --- |
| 裁判开赛 | `startMatch` | `{matchId, expectedVersion, commandId}` |
| 策略师 Ban | `banPoolSlot` | `{meta:{...}, poolSlotId}` |
| 普通落子 | `placePiece` | `{meta:{...}, poolSlotId, position:{row,col}}` |
| 放置 Shiro | `placeShiro` | `{meta:{...}, position:{row,col}}` |
| 抢夺 | `robPiece` | `{meta:{...}, targetPieceId, sacrificeSets}` |
| 裁判确认谱面结果 | `confirmBeatmapResult` | `{meta:{...}, boardPieceId, winningTeam}` |
| 校准计时 | `calibrateTimer` | `{meta:{...}, remainingMilliseconds, reason}` |
| 授予追加时间 | `grantAdditionalTime` | `{meta:{...}, reason}` |
| 暂停/恢复计时 | `pauseTimer` / `resumeTimer` | `{meta:{...}, reason}` |
| 记录投降 | `recordSurrender` | `{meta:{...}, surrenderingTeam, confirmingPlayerIds, reason}` |

`TeamSide` 在 GraphQL 中使用 `RED`、`BLUE`。`PositionInput` 的 `row`、`col` 从 0 开始。普通落子生成的棋子 ID 是 `piece-<commandId>`。

开赛示例：

```graphql
mutation Start($input: CommandMeta!) {
  startMatch(input: $input) {
    success commandId disposition previousVersion resultingVersion currentVersion
    error { code message currentVersion }
    snapshot { version lifecycle phase turn activeTeam }
    events { eventId sequence resultingVersion type }
  }
}
```

```json
{
  "input": {
    "matchId": "<MATCH_ID>",
    "expectedVersion": 0,
    "commandId": "<NEW_UUID>"
  }
}
```

普通落子示例：

```graphql
mutation Place($input: PlacePieceInput!) {
  placePiece(input: $input) {
    success commandId disposition previousVersion resultingVersion currentVersion
    error { code message currentVersion }
    snapshot { version lifecycle phase turn activeTeam pendingPieceID }
    events { eventId sequence resultingVersion type }
  }
}
```

```json
{
  "input": {
    "meta": {
      "matchId": "<MATCH_ID>",
      "expectedVersion": 5,
      "commandId": "<NEW_UUID>"
    },
    "poolSlotId": "<analysis返回的poolSlotID>",
    "position": { "row": 0, "col": 0 }
  }
}
```

裁判确认结果示例：

```graphql
mutation Confirm($input: ConfirmBeatmapResultInput!) {
  confirmBeatmapResult(input: $input) {
    success commandId disposition previousVersion resultingVersion currentVersion
    error { code message currentVersion }
    snapshot { version lifecycle phase turn activeTeam pendingPieceID }
    events { eventId sequence resultingVersion type }
  }
}
```

```json
{
  "input": {
    "meta": {
      "matchId": "<MATCH_ID>",
      "expectedVersion": 6,
      "commandId": "<NEW_UUID>"
    },
    "boardPieceId": "piece-<落子命令UUID>",
    "winningTeam": "RED"
  }
}
```

其余 mutation 的准确类型以仓库根目录的 `schema.graphql` 为准。

## 9. 完整比赛流程

按以下顺序执行，并在每次成功命令后重新查询 `MatchState`：

1. 裁判以版本 0 执行 `startMatch`。
2. 红蓝双方根据 `activeTeam` 和 `banPoolSlotIDs` 完成四次 ABBA Ban。
3. 把包含 `MISSING_BID` 的槽位 Ban 掉，防止后面的真实房间切到不存在的谱面。
4. 当前策略师从 `legalPlacements` 选择一个普通落子。
5. 裁判用 `pendingPieceID` 确认获胜方。
6. 继续制造一组合法的两连。`shiroCells` 出现后执行 `placeShiro`。
7. 继续选择胜负，让某一方形成合法牺牲连线。`robberyPlans` 出现后，原样使用其中一项的 `targetPieceID` 和 `sacrificeSets` 执行抢夺。
8. 不要提前制造四连，否则比赛会在后续覆盖完成前结束。
9. 完成第 10 节的刷新、重放、冲突和 WebSocket 测试。
10. 完成第 11 节元数据测试。
11. 完成第 12、13 节的 Bancho 离线和真实房间测试。
12. 裁判把当前计时校准为 1000 ms，等待耗尽。普通操作应返回 `TIMER_EXPIRED`。
13. 裁判执行 `grantAdditionalTime`。对应队伍的 `teamPauseUsed` 应变成 `true`，操作重新可用。
14. 同一队后续再次耗尽时间并请求追加，应返回 `TEAM_PAUSE_ALREADY_USED`。
15. 最后由裁判执行 `recordSurrender`：提供投降方名单中四个不同的 ID，并且必须包含队长。

通过标准：生命周期最终为 `FINISHED`，胜方与投降方相反，终局后普通比赛命令不再改变状态。

如果还要人工验收 TB，请新建第二场比赛。第 11 至 14 回合由一名队长请求 TB、对方队长接受，裁判启动并确认 TB 结果；不要在已经结束的主验收比赛上继续操作。

## 10. 刷新、命令重放和 WebSocket 重连

### 10.1 页面刷新

每个成功命令后，四个 Profile 都硬刷新 GraphQL 页面并重新执行各自查询。

通过标准：四方看到相同的 `snapshot.version`、棋盘、阶段、回合和比分。私有视图可以不同，但不能出现两个权威比赛状态。

### 10.2 命令重放和冲突

- 使用完全相同的 `commandId + payload` 再提交一次：应返回 `REPLAYED`，版本不增加。
- 使用相同 `commandId` 但修改 payload：应返回 `DUPLICATE_COMMAND_MISMATCH`。
- 记住一个旧版本，其他角色先成功执行命令，再用旧 `expectedVersion` 提交新命令：应返回 `MATCH_VERSION_CONFLICT` 和当前版本。
- 观众尝试执行比赛 mutation：必须被拒绝，状态不变。

### 10.3 WebSocket

在四个 Profile 的开发者控制台分别执行：

```js
window.m7Events = [];
window.m7Socket = new WebSocket("ws://localhost:8080/ws/match");
window.m7Socket.onmessage = event => {
  const message = JSON.parse(event.data);
  window.m7Events.push(message);
  console.log(message);
};
window.m7Socket.onopen = () => window.m7Socket.send(JSON.stringify({
  type: "subscribe",
  schemaVersion: 1,
  matchId: "<MATCH_ID>"
}));
```

测试步骤：

1. 四方首次订阅应收到当前 snapshot。
2. 执行一条命令，所有连接应按顺序收到更新，并最终到达同一版本。
3. 在观众 Profile 执行 `window.m7Socket.close()`。
4. 其他角色连续执行两条合法命令。
5. 观众重新运行订阅代码。

通过标准：重连后的第一份 snapshot 是最新状态，不需要浏览器自己推算漏掉的回合；四方最终版本一致。

## 11. 真实 osu! 谱面元数据

执行第 7 节 `MatchState` 查询会为尚未缓存的 BID 建立后台任务。后端 worker 每秒检查任务，但失败后的自动重试从约 1 分钟开始指数退避，最高约 1 小时。

验证：

1. 记录当前 `snapshot.version`。
2. 有效普通 BID 和 TB BID 最终应为 `READY`，并出现 `beatmap` 内容。
3. `MISSING_BID` 应变为 `FAILED`，显示 `metadataAttempts`、`metadataNextRetryAt` 和 `metadataLastError`。
4. 由当前房间裁判执行：

```graphql
mutation RetryMetadata($input: RetryBeatmapMetadataInput!) {
  retryBeatmapMetadata(input: $input)
}
```

```json
{
  "input": {
    "matchID": "<MATCH_ID>",
    "beatmapID": "<MISSING_BID>"
  }
}
```

5. 等待 worker 再次处理，attempts 应增加。
6. 重新查询比赛，`snapshot.version` 必须仍与第 1 步相同。
7. 非裁判或不属于该比赛的 BID 不得被手动重试。

`FAILED` 不是永久停止状态；到达 `metadataNextRetryAt` 后仍会自动重试。

## 12. Bancho 关闭时的行为

确认 `.env.m7` 中四个 `BANCHO_IRC_*` 仍为空，并且后端是在该配置下启动的。

裁判查询：

```graphql
query IRCStatus($matchId: ID!) {
  ircConnectionStatus(matchId: $matchId) {
    configured connected degraded lastError
  }
  ircJobs(matchId: $matchId) {
    id kind payload status attempts automaticRetry nextTryAt lastError
  }
}
```

通过标准：

- `configured=false`。
- Ban、落子和人工确认结果仍能成功。
- 比赛状态和版本正常持久化。
- 无法完成的自动化不会伪装成成功；应能从 `ircJobs` 或 `refereeView.automationIssues` 看见。

## 13. 连接真实受控 Bancho 房间

停止后端，但不要停止 MongoDB 和 Redis。在 `.env.m7` 写入：

```env
BANCHO_IRC_ADDR=irc.ppy.sh:6667
BANCHO_IRC_USERNAME=<osu用户名，空格通常写成下划线>
BANCHO_IRC_PASSWORD=<IRC password，不是osu登录密码>
BANCHO_IRC_CHANNEL=
```

`BANCHO_IRC_CHANNEL` 保持为空。后端会从 `MP_LINK` 自动加入 `#mp_<房间ID>`。当前实现使用明文 IRC TCP 6667，不支持 TLS。

重新启动后端，重复 `IRCStatus` 查询。必须看到 `configured=true`、`connected=true`。若离线阶段留下了待发送任务，先让它们恢复并处理完，再执行新的落子。

测试地图命令：

1. 当前策略师放置一个使用有效 BID 的普通棋子。
2. 多人房间应出现 `!mp map <BID>`。
3. 查询 `ircJobs`，对应任务应经历 `PENDING/SENDING/SENT`，最终到达 `ACKNOWLEDGED`。
4. 在结果尚未由裁判确认前，在多人房间发送：

```text
!result RED piece-<落子commandId>
```

5. 查询证据：

```graphql
query IRCEvidence($matchId: ID!, $channel: String!) {
  ircObservations(matchId: $matchId, channel: $channel) {
    id channel sender command raw observedAt reviewStatus reviewReason
    suggestedResult { winningTeam boardPieceID }
  }
}
```

变量中的 channel 是 `#mp_<房间ID>`。

6. 应出现 `PENDING` observation。裁判审核后执行：

```graphql
mutation ConfirmIRCEvidence($input: ConfirmIRCResultInput!) {
  confirmIRCResult(input: $input) {
    success commandId resultingVersion currentVersion
    error { code message currentVersion }
    snapshot { version lifecycle phase turn activeTeam pendingPieceID }
  }
}
```

```json
{
  "input": {
    "matchId": "<MATCH_ID>",
    "expectedVersion": 20,
    "commandId": "<NEW_UUID>",
    "observationId": "<OBSERVATION_ID>",
    "boardPieceId": "piece-<落子commandId>",
    "winningTeam": "RED"
  }
}
```

IRC 消息只是证据，不会自行改变比赛结果。只有裁判确认后，权威状态才可以前进。

真实 Bancho 可能不向 IRC 客户端广播未知的 `!result` 文本。如果游戏聊天中能发送，但 `ircObservations` 始终收不到，应记录为真实平台兼容性失败，不能通过直接调用普通确认 mutation 来冒充这项验收已经成功。

失败的发送任务可以由裁判调用 `retryIRCJob`。规划阶段失败、尚未形成 job 的事件由 `retryMatchAutomation` 重试。

## 14. 后端重启恢复和最终审计

在比赛仍进行时：

1. 记录最新 `snapshot.version`、棋盘、pending piece 和 IRC job 状态。
2. 正常停止后端。
3. 保持 MongoDB 和 Redis 运行，用同一 `.env.m7` 重新启动后端。
4. 四个浏览器重新查询比赛并重新订阅 WebSocket。
5. 检查未完成的元数据和 IRC 任务是否继续处理。

通过标准：比赛恢复到同一版本，没有退回旧模型；WebSocket 首次 snapshot 与 GraphQL 一致；已经成功执行的命令不会因重启再次改变棋盘。

比赛结束后查询 `refereeView.auditLog`，确认至少包含：

- 开赛和四次 Ban。
- 普通落子及结果确认。
- Shiro 和抢夺。
- 计时校准、追加时间及原因。
- IRC 证据确认或明确失败记录。
- 最终投降或其他终局命令。

审计中的 `previousVersion`、`resultingVersion` 应连续且与命令顺序一致。

## 15. 最终通过清单

每一项只能填写 `PASS`、`FAIL` 或 `BLOCKED`：

| 项目 | 通过条件 | 结果 |
| --- | --- | --- |
| 自动检查 | verify、单元测试、MongoDB/Redis 集成测试全绿 | |
| OAuth | 四个真实账号都能建立独立 Cookie 会话 | |
| 权限 | 红蓝私有视图正确，观众不能写，裁判能审核 | |
| 正式配置 | REST 创建的房间能生成 READY 权威比赛 | |
| 规则主流程 | ABBA、普通落子、确认、Shiro、抢夺、追加时间成功 | |
| 命令可靠性 | REPLAYED、重复冲突、旧版本冲突均符合预期 | |
| 刷新 | 四方刷新后权威状态一致 | |
| WebSocket | 断线期间漏过命令后，重连立即取得最新 snapshot | |
| 元数据 | READY、FAILED、人工重试可见且不改变比赛版本 | |
| Bancho 离线 | 自动化不可用时人工比赛仍可继续 | |
| Bancho 在线 | `!mp map` 得到确认，证据必须经裁判审核 | |
| 重启恢复 | 比赛、任务和审计在后端重启后继续存在 | |
| 终局 | FINISHED、胜方、原因和审计一致 | |

只有全部必测项目为 `PASS`，才能说 M7 后端真实环境验收完成。Web 页面体验仍是单独的前端验收，不能由这份后端手册代替。

## 16. 失败回报模板

协作者完成后，把下面内容发回开发组。不要附带任何 Secret、Cookie 或 IRC password。

```text
Tester:
Commit SHA:
OS / Docker / Go:
osu! lobby ID:

Automated checks: PASS / FAIL / BLOCKED
OAuth and roles: PASS / FAIL / BLOCKED
Formal match flow: PASS / FAIL / BLOCKED
Refresh and WebSocket: PASS / FAIL / BLOCKED
Metadata: PASS / FAIL / BLOCKED
Bancho offline: PASS / FAIL / BLOCKED
Bancho live: PASS / FAIL / BLOCKED
Restart and audit: PASS / FAIL / BLOCKED

Failure stage:
Actor/profile:
Command or action:
Expected result:
Actual result:
Version before/after:
Relevant error code:
Relevant backend log lines:
Can reproduce after restart: yes / no
```

## 17. 常见阻塞

- MongoDB 一直不健康：运行 `docker compose ps` 和 `docker compose logs mongodb`，确认单节点 replica set 已成为可写 primary。
- 初始化提示管理员重复：该 ID 已经写入当前数据库；换一个新的验收数据库名重新初始化。
- OAuth 回调页面报错：先到 `/graphql` 查询 `me`，以 Cookie 是否生效为准。
- 修改权限后突然 401：这是会话撤销，重新 OAuth 登录。
- 正式比赛无法创建：检查每队是否恰好 8 人、队长是否在名单内、是否恰好一个 Shiro 和 TB、MP 链接是否为官方 HTTPS 地址。
- 元数据一直是 PENDING：确认至少查询过一次该 pool slot，并检查后台 worker 日志和 osu! Client 凭据。
- IRC 显示未配置：确认启动后端的终端确实设置了 `ENV_FILE=.env.m7`，修改配置后必须重启。
- IRC 已连接但没有 ACK：检查 IRC 用户名的空格是否换成下划线、账号是否在房间内、是否拥有执行 `!mp map` 的权限。
- `!result` 没有 observation：保留聊天和日志证据，按 Bancho 兼容性失败报告。
- 端口被占用：确认 8080、27017、6379 没有被其他服务占用，或先停止冲突服务。
