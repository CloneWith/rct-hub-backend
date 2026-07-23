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
	"rctHubBackend/internal/repository"
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

	deps := &Deps{
		Cfg:              cfg,
		DB:               db,
		UserRepo:         repository.NewUserRepository(db.MongoDB),
		BeatmapRepo:      repository.NewBeatmapRepository(db.MongoDB),
		MatchRepo:        repository.NewMatchRepository(db.MongoDB),
		MoveRepo:         repository.NewMoveRepository(db.MongoDB),
		ResultRepo:       repository.NewResultRepository(db.MongoDB),
		AnnouncementRepo: repository.NewAnnouncementRepository(db.MongoDB),
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

	api := s.router.Group("/api/v1")
	{
		api.GET("/health", health.Check)
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
