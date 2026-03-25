package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	appinternal "multi-tenant-bot/internal"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

const defaultMetaVerifyToken = "marketmix-dev-verify-token-2026"

type Config struct {
	Port     string
	LogLevel string

	DatabaseURL string

	RedisURL          string
	SessionTTLMinutes int

	GroqAPIKey string

	MetaVerifyToken string
	MetaAppSecret   string
}

func loadConfig() (*Config, error) {
	cfg := &Config{
		Port:              getEnv("PORT", "8080"),
		LogLevel:          getEnv("LOG_LEVEL", "info"),
		DatabaseURL:       mustEnv("DATABASE_URL"),
		RedisURL:          mustEnv("REDIS_URL"),
		GroqAPIKey:        mustEnv("GROQ_API_KEY"),
		MetaVerifyToken:   getEnv("META_VERIFY_TOKEN", defaultMetaVerifyToken),
		MetaAppSecret:     getEnv("META_APP_SECRET", ""),
		SessionTTLMinutes: getEnvInt("SESSION_TTL_MINUTES", 30),
	}
	return cfg, nil
}

type App struct {
	cfg    *Config
	db     *pgxpool.Pool
	rdb    *redis.Client
	server *http.Server
	log    *slog.Logger
}

func NewApp(cfg *Config) (*App, error) {
	log := newLogger(cfg.LogLevel)

	log.Info("connecting to PostgreSQL", "url", maskURL(cfg.DatabaseURL))
	db, err := connectPostgres(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	log.Info("PostgreSQL connected")

	log.Info("connecting to Redis", "url", maskURL(cfg.RedisURL))
	rdb, err := connectRedis(cfg.RedisURL)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("redis: %w", err)
	}
	log.Info("Redis connected")

	mux := http.NewServeMux()
	registerRoutes(mux, cfg, db, rdb, log)

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return &App{
		cfg:    cfg,
		db:     db,
		rdb:    rdb,
		server: server,
		log:    log,
	}, nil
}

func (a *App) Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		a.log.Info("server listening", "port", a.cfg.Port)
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		a.log.Info("shutdown signal received")
	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	}

	return a.Shutdown()
}

func (a *App) Shutdown() error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	a.log.Info("shutting down HTTP server")
	if err := a.server.Shutdown(shutdownCtx); err != nil {
		a.log.Error("HTTP shutdown error", "err", err)
	}

	a.log.Info("closing PostgreSQL pool")
	a.db.Close()

	a.log.Info("closing Redis connection")
	if err := a.rdb.Close(); err != nil {
		a.log.Error("Redis close error", "err", err)
	}

	a.log.Info("shutdown complete")
	return nil
}

func registerRoutes(
	mux *http.ServeMux,
	cfg *Config,
	db *pgxpool.Pool,
	rdb *redis.Client,
	log *slog.Logger,
) {
	repo := appinternal.NewRepository(db)
	sessions := appinternal.NewSessionStore(rdb, time.Duration(cfg.SessionTTLMinutes)*time.Minute)
	aiClient := appinternal.NewGroqClient(cfg.GroqAPIKey)
	webhookHandler := appinternal.NewWebhookHandler(
		appinternal.WebhookConfig{
			VerifyToken: cfg.MetaVerifyToken,
			AppSecret:   cfg.MetaAppSecret,
		},
		repo,
		sessions,
		aiClient,
		log,
	)

	mux.HandleFunc("/health", handleHealth(db, rdb))
	mux.Handle("/webhook", webhookHandler)
}

func handleHealth(db *pgxpool.Pool, rdb *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
			http.Error(w, "postgres unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := rdb.Ping(ctx).Err(); err != nil {
			http.Error(w, "redis unavailable", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	}
}

func connectPostgres(url string) (*pgxpool.Pool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func connectRedis(rawURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid REDIS_URL: %w", err)
	}

	rdb := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return rdb, nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "FATAL: environment variable %q is required\n", key)
		os.Exit(1)
	}
	return v
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func maskURL(raw string) string {
	if len(raw) > 20 {
		return raw[:10] + "***"
	}
	return "***"
}

func main() {
	_ = godotenv.Load()

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	app, err := NewApp(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "startup error: %v\n", err)
		os.Exit(1)
	}

	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "runtime error: %v\n", err)
		os.Exit(1)
	}
}
