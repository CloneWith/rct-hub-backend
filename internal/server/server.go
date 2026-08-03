package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"rctHubBackend/internal/config"
	"rctHubBackend/internal/database"
	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/fetcher"
	"rctHubBackend/internal/graphql"
	"rctHubBackend/internal/handler"
	"rctHubBackend/internal/middleware"
	"rctHubBackend/internal/oauth"
	"rctHubBackend/internal/repository"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/jwtutil"
)

// Server wraps the HTTP server and its dependencies.
type Server struct {
	httpServer *http.Server
	router     *gin.Engine
	deps       *Deps
	logger     *zap.Logger
}

// Deps aggregates repositories, services, and utilities used by handlers.
type Deps struct {
	Cfg         *config.Config
	DB          *database.DB
	Repos       *repository.Repositories
	Services    *service.Services
	AuthSvc     service.AuthService
	UserSvc     *service.UserService
	BeatmapSvc  *service.BeatmapService
	AnnounceSvc *service.AnnouncementService
	Fetcher     fetcher.Fetcher
	JWTSigner   *jwtutil.Signer
}

// New creates a new Server with routes configured.
func New(cfg *config.Config, db *database.DB, logger *zap.Logger) *Server {
	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(gin.Recovery())

	router.Use(ginzap.Ginzap(logger, time.RFC3339, true))
	router.Use(ginzap.RecoveryWithZap(logger, true))

	router.Use(cors.New(cors.Config{
		AllowOrigins:     cfg.CORS.AllowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	router.Use(middleware.ErrorHandler())

	signer := jwtutil.NewSigner(cfg.JWT.Secret, "rcthub-backend")
	oauthClient := oauth.NewClient(oauth.Config{
		ClientID:     cfg.Osu.ClientID,
		ClientSecret: cfg.Osu.ClientSecret,
		RedirectURI:  cfg.Osu.RedirectURI,
		APIBase:      cfg.Osu.APIBase,
	}, db.Redis)

	repos := repository.NewRepositories(db.MongoDB)
	services := service.NewServices(repos)

	// osu! API fetcher — three-tier lookup (Redis → MongoDB → osu! API v2).
	apiClient := fetcher.NewAPIClient(fetcher.APIClientConfig{
		ClientID:     cfg.Osu.ClientID,
		ClientSecret: cfg.Osu.ClientSecret,
		APIBase:      cfg.Osu.APIBase,
	}, db.Redis, logger)
	osuFetcher := fetcher.New(apiClient, repos.Users, repos.Beatmaps, db.Redis, logger, fetcher.Config{
		UserCacheTTL:    cfg.Osu.FetcherUserCacheTTL,
		BeatmapCacheTTL: cfg.Osu.FetcherBeatmapCacheTTL,
	})

	deps := &Deps{
		Cfg:         cfg,
		DB:          db,
		Repos:       repos,
		Services:    services,
		AuthSvc:     service.NewAuthService(oauthClient, repos.Users, signer, cfg.JWT.Expiry),
		UserSvc:     service.NewUserService(repos.Users),
		BeatmapSvc:  service.NewBeatmapService(repos.Beatmaps),
		AnnounceSvc: service.NewAnnouncementService(repos.Announcements),
		Fetcher:     osuFetcher,
		JWTSigner:   signer,
	}

	s := &Server{
		router: router,
		deps:   deps,
		logger: logger,
		httpServer: &http.Server{
			Addr:    fmt.Sprintf(":%s", cfg.Port),
			Handler: router,
		},
	}

	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	health := handler.NewHealthHandler(s.deps.DB)
	s.router.GET("/health", health.Check)

	auth := handler.NewAuthHandler(s.deps.AuthSvc)
	s.router.GET("/auth/osu", auth.OsuLogin)
	s.router.GET("/auth/osu/callback", auth.OsuCallback)

	// GraphQL endpoint — all reads, client views, and in-game commands
	// GET  /graphql → GraphiQL Playground
	// POST /graphql → GraphQL Query/Mutation
	gqlResolver := graphql.NewResolver(s.deps.Services)
	gqlHandler := graphql.NewHandler(gqlResolver)
	s.router.GET("/graphql", graphql.GinPlayground("/graphql"))
	s.router.POST("/graphql", graphql.GinGraphQL(gqlHandler, s.deps.JWTSigner, s.deps.Services.Beatmaps))

	users := handler.NewUserHandler(s.deps.UserSvc)
	beatmaps := handler.NewBeatmapHandler(s.deps.BeatmapSvc)
	rooms := handler.NewRoomHandler(s.deps.Services.Rooms)
	announcements := handler.NewAnnouncementHandler(s.deps.AnnounceSvc)

	api := s.router.Group("/api/v1")
	{
		api.GET("/health", health.Check)

		// Authenticated endpoints — room configuration (pre-game setup)
		// All read operations and in-game commands are served via GraphQL (/graphql).
		authorized := api.Group("")
		authorized.Use(middleware.Auth(s.deps.JWTSigner))
		{
			authorized.POST("/rooms", rooms.Create)
			authorized.PATCH("/rooms/:id/strategists", rooms.SetStrategists)
			authorized.PATCH("/rooms/:id/streamer", rooms.SetStreamer)
			authorized.PATCH("/rooms/:id/bp-order", rooms.SetBPOrder)
			authorized.PATCH("/rooms/:id/players", rooms.SetPlayers)
			authorized.PATCH("/rooms/:id/mp-link", rooms.SetMPLink)
			authorized.PATCH("/rooms/:id/stream-link", rooms.SetStreamLink)
			authorized.POST("/rooms/:id/start-match", rooms.StartMatch)
		}

		// Admin-only endpoints — CRUD operations (curl/script friendly)
		admin := authorized.Group("")
		admin.Use(middleware.RequireRole(domain.RoleAdmin))
		{
			admin.POST("/beatmaps", beatmaps.Create)
			admin.PUT("/beatmaps/:id", beatmaps.Update)
			admin.DELETE("/beatmaps/:id", beatmaps.Delete)

			admin.PATCH("/users/:id/roles", users.UpdateRoles)
			admin.PATCH("/users/:id/banned", users.SetBanned)
			admin.PATCH("/users/:id/verify-status", users.SetVerifyStatus)

			admin.POST("/announcements", announcements.Create)
			admin.PUT("/announcements/:id", announcements.Update)
			admin.DELETE("/announcements/:id", announcements.Delete)
			admin.POST("/announcements/:id/publish", announcements.Publish)
		}
	}
}

// Start runs the HTTP server.
func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
