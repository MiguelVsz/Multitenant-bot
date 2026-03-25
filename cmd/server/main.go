package main

import (
	"log/slog"
	"net/http"
	"os"

	"multi-tenant-bot/config"
	irouter "multi-tenant-bot/internal/router"
	"multi-tenant-bot/pkg/llm"
)

func main() {
	cfg := config.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	llmClient := llm.NewMockClient()
	routerService := irouter.NewService(llmClient, logger, cfg.BusinessType)
	routerHandler := irouter.NewHandler(routerService)

	mux := http.NewServeMux()
	routerHandler.Register(mux)

	server := &http.Server{
		Addr:    cfg.Address(),
		Handler: mux,
	}

	logger.Info("router agent server starting", "addr", cfg.Address(), "provider", cfg.LLMProvider, "model", cfg.LLMModel)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("router agent server stopped", "error", err)
		os.Exit(1)
	}
}
