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

// Deps aggregates repositories and utilities used by handlers.
type Deps struct {
	Cfg              *config.Config
	DB               *database.DB
	UserRepo         repository.UserRepository
	BeatmapRepo      repository.BeatmapRepository
	MatchRepo        repository.MatchRepository
	MoveRepo         repository.MoveRepository
	ResultRepo       repository.ResultRepository
	AnnouncementRepo repository.AnnouncementRepository
	AuthService      service.AuthService
	JWTSigner        *jwtutil.Signer
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

	userRepo := repository.NewUserRepository(db.MongoDB)

	deps := &Deps{
		Cfg:              cfg,
		DB:               db,
		UserRepo:         userRepo,
		BeatmapRepo:      repository.NewBeatmapRepository(db.MongoDB),
		MatchRepo:        repository.NewMatchRepository(db.MongoDB),
		MoveRepo:         repository.NewMoveRepository(db.MongoDB),
		ResultRepo:       repository.NewResultRepository(db.MongoDB),
		AnnouncementRepo: repository.NewAnnouncementRepository(db.MongoDB),
		AuthService:      service.NewAuthService(oauthClient, userRepo, signer, cfg.JWT.Expiry),
		JWTSigner:        signer,
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

	auth := handler.NewAuthHandler(s.deps.AuthService)
	s.router.GET("/auth/osu", auth.OsuLogin)
	s.router.GET("/auth/osu/callback", auth.OsuCallback)

	api := s.router.Group("/api/v1")
	{
		api.GET("/health", health.Check)

		authorized := api.Group("")
		authorized.Use(middleware.Auth(s.deps.JWTSigner))
		{
			authorized.GET("/auth/me", auth.Me)
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
