package graphql

import (
	"context"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/gin-gonic/gin"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"go.uber.org/zap"

	"rctHubBackend/internal/authsession"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/jwtutil"
)

// NewHandler 创建生产级 GraphQL HTTP handler。
//
// 显式配置 transports / caches / extensions，不使用已废弃的 NewDefaultServer。
// 注意：不注册 WebSocket transport —— 实时推送走独立 WebSocket Gateway（见 ADR-001 §6）。
//
// 所有 resolver / validation 抛出的 error 都会经过 loggingErrorPresenter：
// 既走 gqlgen 默认的包装（保留 path / extensions code 等），也会同步写入
// resolver 上的 zap logger（含 category logger）。无 logger 时退化为 no-op。
func NewHandler(resolver *Resolver) *handler.Server {
	config := Config{
		Resolvers: resolver,
	}
	execSchema := NewExecutableSchema(config)

	srv := handler.New(execSchema)

	// --- Transports ---
	// 顺序重要：Server 选取第一个支持的 transport。
	srv.AddTransport(transport.Options{})       // OPTIONS 预检
	srv.AddTransport(transport.POST{})          // 标准 POST application/json
	srv.AddTransport(transport.GET{})           // GET ?query=... (Playground 使用)
	srv.AddTransport(transport.MultipartForm{}) // 文件上传 (Phase 3+ 备用)
	// 不注册 transport.Websocket{} —— 实时推送由独立 WebSocket Gateway 承担

	// --- Caches ---
	srv.SetQueryCache(lru.New[*ast.QueryDocument](1000))

	// --- Extensions ---
	srv.Use(extension.Introspection{}) // Playground / 开发工具依赖
	srv.Use(extension.AutomaticPersistedQuery{
		Cache: lru.New[string](100),
	})

	// --- Error presenter ---
	// 把 resolver error 镜像到日志（保留默认的 path / extensions 包装）。
	// 必须在 AddTransport / Use 之后注册；SetErrorPresenter 直接覆盖前值，
	// 与其它 extensions 的注册顺序互不干扰。
	srv.SetErrorPresenter(loggingErrorPresenter(resolver))

	return srv
}

// loggingErrorPresenter 包装 graphql.DefaultErrorPresenter，在返回给客户端
// 的 *gqlerror.Error 上同步写入 resolver 的 zap logger。日志字段：
//   - path       resolver path（数组元素名 / 索引，与 GraphQL 响应一致）
//   - message    err.Error()
//   - code       Extensions["code"]（若有，多见于 command 路径上的 MatchError）
//   - err        完整 error 链（zap.Error 字段，stacktrace 由 caller skip 控制）
//
// 设计要点：
//   - resolver == nil 或 logger == nil 时退化为 DefaultErrorPresenter（无日志）。
//   - 永远不改写返回的 *gqlerror.Error：默认 presenter 已经把 path / locations
//     / extensions 写齐，我们只多一份日志。
//   - 错误级别统一为 Error：GraphQL error 通常代表客户端触发的失败或上游
//     不可用，需要排障；resolver 想发 Warn 应直接 s.Logger().Warn(...)。
func loggingErrorPresenter(resolver *Resolver) graphql.ErrorPresenterFunc {
	var log *zap.Logger
	if resolver != nil {
		log = resolver.Logger()
	}
	return func(ctx context.Context, err error) *gqlerror.Error {
		gqlErr := graphql.DefaultErrorPresenter(ctx, err)
		if log != nil && gqlErr != nil {
			path := graphql.GetPath(ctx)
			fields := []zap.Field{
				zap.String("message", gqlErr.Message),
				zap.Any("path", pathString(path)),
			}
			if gqlErr.Extensions != nil {
				if code, ok := gqlErr.Extensions["code"]; ok {
					fields = append(fields, zap.String("code", toString(code)))
				}
			}
			if gqlErr.Locations != nil {
				fields = append(fields, zap.Any("locations", gqlErr.Locations))
			}
			if err != nil {
				fields = append(fields, zap.Error(err))
			}
			log.Error("graphql resolver returned error", fields...)
		}
		return gqlErr
	}
}

// pathString 把 gqlgen 的 ast.Path 转换为字符串切片，方便序列化进 zap 字段。
// ast.Path 元素可能是 ast.PathName (string) 或 ast.PathIndex (int)。
func pathString(path ast.Path) []string {
	if len(path) == 0 {
		return nil
	}
	out := make([]string, 0, len(path))
	for _, segment := range path {
		switch v := segment.(type) {
		case ast.PathName:
			out = append(out, string(v))
		case ast.PathIndex:
			out = append(out, intToString(int(v)))
		default:
			out = append(out, "%!unknown")
		}
	}
	return out
}

func intToString(i int) string {
	// 避免引入 strconv 抖动；path 索引通常 < 1e9，足够装进 int64
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func toString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return ""
	}
}

// GinGraphQL 将 gqlgen handler 包装为 Gin handler，并注入可选 JWT 认证 + 请求级 DataLoader。
//
// 认证策略（可选认证）：
//   - 如果 Authorization header 存在且 JWT 有效 → claims 注入 context
//   - 如果 header 不存在或 JWT 无效 → 请求继续但不带 claims
//   - ping 等公开查询不需要 token；me 等查询在 resolver 中检查 claims 是否存在
//
// 可选 cookie 配置：传入 authsession.CookieConfig 时，浏览器会话被滑动续期
// 的响应会附带刷新后的 Set-Cookie（与 middleware.Auth 行为一致）。
//
// DataLoader 策略：
//   - 每个请求创建独立的 BeatmapLoader 与 UserLoader，防止 N+1 重复查询
func GinGraphQL(gqlHandler *handler.Server, signer *jwtutil.Signer, sessions authsession.Resolver, services *service.Services, cookieConfigs ...authsession.CookieConfig) gin.HandlerFunc {
	cookieName := "rcthub_session"
	var refresh *authsession.CookieConfig
	if len(cookieConfigs) > 0 && cookieConfigs[0].Name != "" {
		cookieName = cookieConfigs[0].Name
		cfg := cookieConfigs[0]
		refresh = &cfg
	}
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		ctx = withMetadataRequestCache(ctx)

		// Public GraphQL queries remain available without credentials. Valid
		// Bearer tokens or browser sessions add the authenticated viewer.
		if claims, renewed, err := authsession.ClaimsFromRequest(c.Request, signer, sessions, cookieName); err == nil {
			if renewed && refresh != nil {
				if secret, cookieErr := c.Cookie(cookieName); cookieErr == nil {
					authsession.RefreshCookie(c.Writer, *refresh, secret)
				}
			}
			ctx = WithClaims(ctx, claims)
		}

		// 请求级 DataLoader
		if services != nil {
			ctx = WithBeatmapLoader(ctx, NewBeatmapLoader(services.Beatmaps))
			ctx = WithUserLoader(ctx, NewUserLoader(services.Users))
		}

		c.Request = c.Request.WithContext(ctx)
		gqlHandler.ServeHTTP(c.Writer, c.Request)
	}
}

// GinPlayground 返回 GraphiQL playground 的 Gin handler。
func GinPlayground(endpoint string) gin.HandlerFunc {
	h := playground.Handler("rctHub GraphQL Playground", endpoint)
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}
