package graphql

import (
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/gin-gonic/gin"
	"github.com/vektah/gqlparser/v2/ast"

	"rctHubBackend/internal/authsession"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/jwtutil"
)

// NewHandler 创建生产级 GraphQL HTTP handler。
//
// 显式配置 transports / caches / extensions，不使用已废弃的 NewDefaultServer。
// 注意：不注册 WebSocket transport —— 实时推送走独立 WebSocket Gateway（见 ADR-001 §6）。
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

	return srv
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
