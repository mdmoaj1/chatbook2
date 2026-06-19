package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chatbook/backend/internal/auth"
	"github.com/chatbook/backend/internal/config"
	"github.com/chatbook/backend/internal/contact"
	"github.com/chatbook/backend/internal/messaging"
	"github.com/chatbook/backend/internal/middleware"
	"github.com/chatbook/backend/internal/notification"
	"github.com/chatbook/backend/internal/presence"
	"github.com/chatbook/backend/internal/signaling"
	"github.com/chatbook/backend/internal/thread"
	"github.com/chatbook/backend/internal/user"
	"github.com/chatbook/backend/internal/websocket"
	"github.com/chatbook/backend/pkg/postgres"
	"github.com/chatbook/backend/pkg/redis"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	// ── Logger ───────────────────────────────────────────────────────────────
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	// ── Config ───────────────────────────────────────────────────────────────
	cfg := config.Load()

	// ── Database ─────────────────────────────────────────────────────────────
	db, err := postgres.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to PostgreSQL")
	}
	defer db.Close()

	if err := postgres.RunMigrations(db, "./migrations"); err != nil {
		log.Fatal().Err(err).Msg("Failed to run migrations")
	}

	// ── Redis ────────────────────────────────────────────────────────────────
	rdb, err := redis.Connect(cfg.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to Redis")
	}
	defer rdb.Close()

	// ── Services ─────────────────────────────────────────────────────────────
	presenceService     := presence.NewService(rdb)
	notificationService := notification.NewService(cfg.FCMCredentialsPath)
	wsHub               := websocket.NewHub(presenceService)

	// ── Repositories ─────────────────────────────────────────────────────────
	authRepo    := auth.NewRepository(db)
	userRepo    := user.NewRepository(db)
	contactRepo := contact.NewRepository(db)
	msgRepo     := messaging.NewRepository(db)
	threadRepo  := thread.NewRepository(db)

	// ── Service layer ────────────────────────────────────────────────────────
	authService    := auth.NewService(authRepo, cfg.JWTSecret, cfg.GoogleClientID)
	userService    := user.NewService(userRepo)
	contactService := contact.NewService(contactRepo, presenceService)
	msgService     := messaging.NewService(msgRepo, wsHub, notificationService, presenceService)
	sigService     := signaling.NewService(wsHub, notificationService)
	threadService  := thread.NewService(threadRepo)

	// ── Handlers ─────────────────────────────────────────────────────────────
	authHandler    := auth.NewHandler(authService)
	userHandler    := user.NewHandler(userService)
	contactHandler := contact.NewHandler(contactService)
	wsHandler      := websocket.NewHandler(wsHub, msgService, sigService, presenceService)
	threadHandler  := thread.NewHandler(threadService)

	// ── Gin Router ───────────────────────────────────────────────────────────
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.ZerologMiddleware())
	r.Use(cors.New(cors.Config{
		AllowOrigins:     cfg.AllowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Authorization", "Content-Type"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// ── Health & Metrics ─────────────────────────────────────────────────────
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "healthy",
			"version": cfg.Version,
		})
	})
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// ── API Routes ───────────────────────────────────────────────────────────
	v1 := r.Group("/api/v1")

	// Auth (public)
	v1.POST("/auth/google",  authHandler.GoogleSignIn)
	v1.POST("/auth/refresh", authHandler.RefreshToken)
	v1.POST("/auth/logout",  middleware.Auth(cfg.JWTSecret), authHandler.Logout)

	// Authenticated routes
	authed := v1.Group("/")
	authed.Use(middleware.Auth(cfg.JWTSecret))
	authed.Use(middleware.RateLimit(rdb, 200, time.Minute))
	{
		// ── Users ──────────────────────────────────────────────────────────
		authed.GET("/users/me",            userHandler.GetMe)
		authed.PUT("/users/me",            userHandler.UpdateProfile)
		authed.POST("/users/me/fcm-token", userHandler.UpdateFCMToken)
		authed.GET("/users/search",        userHandler.Search)

		// ── Contacts ──────────────────────────────────────────────────────
		authed.GET("/contacts",              contactHandler.List)
		authed.POST("/contacts",             contactHandler.Add)
		authed.DELETE("/contacts/:id",       contactHandler.Remove)
		authed.POST("/contacts/:id/block",   contactHandler.Block)
		authed.POST("/contacts/:id/unblock", contactHandler.Unblock)

		// ── Threads / Feed ─────────────────────────────────────────────────
		authed.POST("/threads",                    threadHandler.CreateThread)
		authed.GET("/threads",                     threadHandler.ListFeed)
		authed.GET("/threads/:id",                 threadHandler.GetThread)
		authed.POST("/threads/:id/react",          threadHandler.React)
		authed.POST("/threads/:id/comments",       threadHandler.AddComment)
		authed.GET("/threads/:id/comments",        threadHandler.GetComments)
	}

	// ── WebSocket ─────────────────────────────────────────────────────────────
	// Single unified WebSocket:
	//   - Text messaging (E2E encrypted — backend stores ciphertext only)
	//   - WebRTC signaling (SDP / ICE)
	//   - Presence updates
	//   - Typing indicators
	//   - File transfer notifications (NEVER file data — always P2P)
	r.GET("/ws", middleware.WSAuth(cfg.JWTSecret), wsHandler.HandleConnection)

	// ── Start Hub ────────────────────────────────────────────────────────────
	go wsHub.Run()

	// ── HTTP Server ──────────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Info().Str("port", cfg.Port).Msg("Chatbook backend starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("Server failed")
		}
	}()

	// ── Graceful Shutdown ────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal().Err(err).Msg("Forced shutdown")
	}
	log.Info().Msg("Server exited")
}
