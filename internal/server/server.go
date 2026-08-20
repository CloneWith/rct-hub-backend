package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"

	"rctHubBackend/internal/authsession"
	"rctHubBackend/internal/beatmapmetadata"
	"rctHubBackend/internal/config"
	"rctHubBackend/internal/database"
	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/fetcher"
	"rctHubBackend/internal/graphql"
	"rctHubBackend/internal/handler"
	"rctHubBackend/internal/irc"
	"rctHubBackend/internal/ircpublisher"
	"rctHubBackend/internal/ircreview"
	"rctHubBackend/internal/logger"
	"rctHubBackend/internal/matchcommand"
	"rctHubBackend/internal/middleware"
	"rctHubBackend/internal/oauth"
	"rctHubBackend/internal/persistence"
	"rctHubBackend/internal/realtime"
	"rctHubBackend/internal/repository"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/jwtutil"
)

// Server wraps the HTTP server and its dependencies.
type Server struct {
	httpServer       *http.Server
	router           *gin.Engine
	deps             *Deps
	logs             *logger.Provider
	logger           *zap.Logger
	metadata         *beatmapmetadata.Manager
	ircClient        *irc.Client
	ircJobs          *persistence.IRCJobStore
	backgroundCancel context.CancelFunc
	backgroundDone   chan struct{}
	backgroundStart  func()
	backgroundOnce   sync.Once
}

// Deps aggregates repositories, services, and utilities used by handlers.
type Deps struct {
	Cfg          *config.Config
	DB           *database.DB
	Repos        *repository.Repositories
	Services     *service.Services
	AuthSvc      service.AuthService
	UserSvc      *service.UserService
	BeatmapSvc   *service.BeatmapService
	AnnounceSvc  *service.AnnouncementService
	Fetcher      fetcher.Fetcher
	JWTSigner    *jwtutil.Signer
	AuthSessions *authsession.Store
}

// New creates a new Server with routes configured.
func New(cfg *config.Config, db *database.DB, logs *logger.Provider) *Server {
	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	mainLog := logs.Main()
	authLog := logs.Get(string(logger.CatAuth))
	fetcherLog := logs.Get(string(logger.CatFetcher))
	auditLog := logs.Get(string(logger.CatAudit))
	matchEngineLog := logs.Get(string(logger.CatMatchEngine))

	router := gin.New()
	router.Use(gin.Recovery())

	router.Use(ginzap.Ginzap(mainLog, time.RFC3339, true))
	router.Use(ginzap.RecoveryWithZap(mainLog, true))

	router.Use(cors.New(cors.Config{
		AllowOrigins:     cfg.CORS.AllowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	router.Use(middleware.ErrorHandler(mainLog))

	signer := jwtutil.NewSigner(cfg.JWT.Secret, "rcthub-backend")
	browserSessions := authsession.NewStore(db.Redis, cfg.AuthSession.IdleExpiry, cfg.AuthSession.AbsoluteExpiry)
	oauthClient := oauth.NewClient(oauth.Config{
		ClientID:     cfg.Osu.ClientID,
		ClientSecret: cfg.Osu.ClientSecret,
		RedirectURI:  cfg.Osu.RedirectURI,
		APIBase:      cfg.Osu.APIBase,
	}, db.Redis, authLog)

	repos := repository.NewRepositories(db.Mongo, db.MongoDB)

	// osu! API fetcher — three-tier lookup (Redis → MongoDB → osu! API v2).
	// Created before services so it can be injected as a CacheInvalidator.
	apiClient := fetcher.NewAPIClient(fetcher.APIClientConfig{
		ClientID:     cfg.Osu.ClientID,
		ClientSecret: cfg.Osu.ClientSecret,
		APIBase:      cfg.Osu.APIBase,
	}, db.Redis, fetcherLog)
	osuFetcher := fetcher.New(apiClient, repos.Users, repos.Beatmaps, db.Redis, fetcherLog, fetcher.Config{
		UserCacheTTL:    cfg.Osu.FetcherUserCacheTTL,
		BeatmapCacheTTL: cfg.Osu.FetcherBeatmapCacheTTL,
	})

	services := service.NewServices(repos, osuFetcher, logs, browserSessions)

	deps := &Deps{
		Cfg:          cfg,
		DB:           db,
		Repos:        repos,
		Services:     services,
		AuthSvc:      service.NewAuthService(oauthClient, repos.Users, osuFetcher, signer, cfg.JWT.Expiry, authLog),
		UserSvc:      services.Users,
		BeatmapSvc:   services.Beatmaps,
		AnnounceSvc:  service.NewAnnouncementService(repos.Announcements),
		Fetcher:      osuFetcher,
		JWTSigner:    signer,
		AuthSessions: browserSessions,
	}

	s := &Server{
		router: router,
		deps:   deps,
		logs:   logs,
		logger: mainLog,
		httpServer: &http.Server{
			Addr:    fmt.Sprintf(":%s", cfg.Port),
			Handler: router,
		},
	}
	// Keep the durable job reader available even when live IRC automation is
	// disabled. Referees must still be able to inspect and retry jobs that were
	// written by an earlier process or by a manually operated deployment.
	jobStore := persistence.NewIRCJobStore(db.MongoDB)
	s.ircJobs = jobStore
	publisher := ircpublisher.New(repos.MatchCommands, jobStore, repos.Matches, repos.Rooms, repos.Users)
	observationStore := persistence.NewIRCObservationStore(db.MongoDB)
	reviewReconciler := ircreview.New(observationStore, repos.MatchCommands, time.Minute)
	metadataManager := beatmapmetadata.New(persistence.NewBeatmapMetadataStore(db.MongoDB), repos.Beatmaps, osuFetcher)
	s.metadata = metadataManager
	var worker *irc.Worker
	if cfg.Bancho.Addr != "" && cfg.Bancho.Username != "" {
		client := irc.NewClient(&net.Dialer{Timeout: 10 * time.Second}, cfg.Bancho.Addr, cfg.Bancho.Username, cfg.Bancho.Password, cfg.Bancho.Channel)
		client.WithDeliveryGate(irc.NewRedisDeliveryGate(db.Redis, "rct:bancho:delivery:", 30*time.Second))
		worker = irc.NewWorker(jobStore, client, time.Second).WithRateLimiter(
			irc.NewRedisRateLimiter(db.Redis, "rct:bancho:rate:"+cfg.Bancho.Username, time.Second),
		).WithValidator(func(ctx context.Context, job irc.Job) error {
			matchID, err := bson.ObjectIDFromHex(job.MatchID)
			if err != nil {
				return fmt.Errorf("IRC job match ID is invalid")
			}
			match, err := repos.Matches.ByID(ctx, matchID)
			if err != nil {
				return fmt.Errorf("reload match before IRC delivery: %w", err)
			}
			room, err := repos.Rooms.ByID(ctx, match.RoomID)
			if err != nil {
				return fmt.Errorf("reload room before IRC delivery: %w", err)
			}
			if room.Settings.MPLink == nil {
				return fmt.Errorf("%w: multiplayer link is no longer configured", irc.ErrJobObsolete)
			}
			channel, err := irc.ChannelFromMPLink(*room.Settings.MPLink)
			if err != nil || channel != job.Channel {
				return fmt.Errorf("%w: job channel %q is no longer current", irc.ErrJobObsolete, job.Channel)
			}
			return nil
		})
		s.ircClient = client
	}
	s.backgroundStart = func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.backgroundCancel = cancel
		s.backgroundDone = make(chan struct{})
		var workers sync.WaitGroup
		if s.ircClient != nil {
			workers.Add(1)
			client := s.ircClient
			client.SetReceiptHandler(func(receipt irc.DeliveryReceipt) {
				var err error
				if receipt.Acknowledged {
					err = jobStore.Ack(ctx, receipt.JobID, receipt.LeaseToken, receipt.ReceivedAt)
				} else {
					err = jobStore.Reject(ctx, receipt.JobID, receipt.LeaseToken, "Bancho rejected the requested operation")
				}
				if err != nil {
					s.logger.Error("IRC delivery receipt could not be persisted", zap.String("job_id", receipt.JobID), zap.Error(err))
					return
				}
				client.ReleaseDelivery(receipt)
			})
			go func() {
				defer workers.Done()
				err := client.Run(ctx, func(observation irc.Observation) {
					if err := observationStore.Save(ctx, observation); err != nil {
						s.logger.Error("IRC observation could not be persisted", zap.String("observation_id", observation.ID), zap.Error(err))
					}
				})
				if err != nil && !errors.Is(err, context.Canceled) {
					s.logger.Error("IRC reader stopped", zap.Error(err))
				}
			}()
		}
		startPeriodicTask(ctx, &workers, time.Second, metadataManager.RunOnce, func(err error) {
			s.logger.Error("beatmap metadata worker failed", zap.Error(err))
		})
		startPeriodicTask(ctx, &workers, time.Second, reviewReconciler.RunOnce, func(err error) {
			s.logger.Error("IRC evidence reconciliation failed", zap.Error(err))
		})
		startPeriodicTask(ctx, &workers, time.Second, publisher.RunOnce, func(err error) {
			s.logger.Error("IRC outbox publisher failed", zap.Error(err))
		})
		if worker != nil {
			startPeriodicTask(ctx, &workers, time.Second, worker.RunOnce, func(err error) {
				s.logger.Error("IRC side-effect worker failed", zap.Error(err))
			})
		}
		go func() {
			workers.Wait()
			close(s.backgroundDone)
		}()
	}

	s.registerRoutes(auditLog, authLog, matchEngineLog)
	return s
}

func (s *Server) registerRoutes(auditLog, authLog, matchEngineLog *zap.Logger) {
	health := handler.NewHealthHandler(s.deps.DB)
	s.router.GET("/health", health.Check)

	auth := handler.NewAuthHandler(s.deps.AuthSvc, s.deps.AuthSessions, s.deps.Cfg.FrontEndURI, handler.AuthCookieConfig{
		Name: s.deps.Cfg.AuthCookie.Name, Domain: s.deps.Cfg.AuthCookie.Domain,
		Secure: s.deps.Cfg.AuthCookie.Secure, SameSite: authSameSite(s.deps.Cfg.AuthCookie.SameSite),
		TTL: s.deps.Cfg.AuthSession.AbsoluteExpiry,
	})
	// Sliding cookie refresh: whenever the server-side session is renewed, the
	// browser cookie Max-Age restarts to the idle window, so active users are
	// never logged out by a stale cookie. The absolute deadline still bounds it.
	sessionCookie := authsession.CookieConfig{
		Name: s.deps.Cfg.AuthCookie.Name, Domain: s.deps.Cfg.AuthCookie.Domain,
		Secure: s.deps.Cfg.AuthCookie.Secure, SameSite: authSameSite(s.deps.Cfg.AuthCookie.SameSite),
		TTL: s.deps.Cfg.AuthSession.IdleExpiry,
	}
	s.router.GET("/auth/osu", auth.OsuLogin)
	s.router.GET("/auth/osu/callback", auth.OsuCallback)
	s.router.POST("/auth/logout", auth.Logout)

	// GraphQL endpoint — all reads, client views, and in-game commands
	// GET  /graphql → GraphiQL Playground
	// POST /graphql → GraphQL Query/Mutation
	commands := matchcommand.NewOrchestrator(
		s.deps.Repos.MatchCommands,
		s.deps.Repos.Users,
		s.deps.Repos.Matches,
		s.deps.Repos.Rooms,
		nil,
		matchEngineLog,
	)
	gqlResolver := graphql.NewResolver(s.deps.Services, commands).WithAuditReader(s.deps.Repos.MatchCommands).WithAutomationIssues(s.deps.Repos.MatchCommands).WithBeatmapMetadata(s.metadata).WithIRCReader(persistence.NewIRCObservationStore(s.deps.DB.MongoDB)).WithIRCJobs(s.ircJobs).WithIRCStatus(s.ircClient)
	gqlHandler := graphql.NewHandler(gqlResolver)
	s.router.GET("/graphql", graphql.GinPlayground("/graphql"))
	s.router.POST("/graphql", graphql.GinGraphQL(gqlHandler, s.deps.JWTSigner, s.deps.AuthSessions, s.deps.Services, sessionCookie))
	// WebSocket is read-only: commands still enter through GraphQL's
	// authoritative Orchestrator. The gateway rehydrates a snapshot first and
	// then streams only durable outbox events in sequence order.
	realtimeGateway := realtime.NewGateway(
		s.deps.Repos.MatchSnapshots,
		s.deps.Repos.MatchCommands,
		s.deps.JWTSigner,
		s.deps.AuthSessions,
		s.deps.Cfg.AuthCookie.Name,
		s.deps.Cfg.CORS.AllowedOrigins,
		func(ctx context.Context, claims *jwtutil.Claims, _ bson.ObjectID) error {
			if claims == nil || claims.UserID == "" {
				return fmt.Errorf("missing authenticated user")
			}
			user, err := s.deps.UserSvc.GetByOsuID(ctx, claims.OsuID)
			if err != nil || user == nil || user.IsBanned || user.VerifyStatus != domain.Verified {
				return fmt.Errorf("current user is not allowed to subscribe")
			}
			return nil
		},
		matchEngineLog,
	)
	s.router.GET("/ws/match", gin.WrapH(realtimeGateway))

	users := handler.NewUserHandler(s.deps.UserSvc, auditLog)
	beatmaps := handler.NewBeatmapHandler(s.deps.BeatmapSvc, auditLog)
	rooms := handler.NewRoomHandler(s.deps.Services.Rooms, auditLog)
	announcements := handler.NewAnnouncementHandler(s.deps.AnnounceSvc)

	api := s.router.Group("/api/v1")
	{
		api.GET("/health", health.Check)

		// Authenticated endpoints — room configuration (pre-game setup)
		// All read operations and in-game commands are served via GraphQL (/graphql).
		authorized := api.Group("")
		authorized.Use(middleware.Auth(s.deps.JWTSigner, s.deps.AuthSessions, s.deps.Cfg.AuthCookie.Name, sessionCookie))
		{
			authorized.POST("/rooms", rooms.Create)
			authorized.PATCH("/rooms/:id/strategists", rooms.SetStrategists)
			authorized.PATCH("/rooms/:id/streamer", rooms.SetStreamer)
			authorized.PATCH("/rooms/:id/mappool", rooms.SetMappool)
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
			admin.PATCH("/beatmaps/:id", beatmaps.Patch)
			admin.DELETE("/beatmaps/:id", beatmaps.Delete)

			admin.PATCH("/users/:id", users.Patch)
			admin.PATCH("/users/:id/roles", users.UpdateRoles)
			admin.PATCH("/users/:id/banned", users.SetBanned)
			admin.PATCH("/users/:id/verify-status", users.SetVerifyStatus)

			admin.POST("/announcements", announcements.Create)
			admin.PATCH("/announcements/:id", announcements.Patch)
			admin.DELETE("/announcements/:id", announcements.Delete)
			admin.POST("/announcements/:id/publish", announcements.Publish)
		}
	}
}

func authSameSite(value string) http.SameSite {
	if value == "strict" {
		return http.SameSiteStrictMode
	}
	return http.SameSiteLaxMode
}

// Start runs the HTTP server.
func (s *Server) Start() error {
	if s.backgroundStart != nil {
		s.backgroundOnce.Do(s.backgroundStart)
	}
	return s.httpServer.ListenAndServe()
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	var shutdownErrors []error
	httpDone := make(chan error, 1)
	go func() { httpDone <- s.httpServer.Shutdown(ctx) }()
	if s.backgroundCancel != nil {
		s.backgroundCancel()
	}
	if s.ircClient != nil {
		_ = s.ircClient.Close()
	}
	if s.backgroundDone != nil {
		select {
		case <-s.backgroundDone:
		case <-ctx.Done():
			shutdownErrors = append(shutdownErrors, ctx.Err())
		}
	}
	if err := <-httpDone; err != nil {
		shutdownErrors = append(shutdownErrors, err)
	}
	return errors.Join(shutdownErrors...)
}
