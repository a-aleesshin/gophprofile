package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/a-aleesshin/gophprofile/file/internal/config"
	"github.com/a-aleesshin/gophprofile/file/migrations"
	"github.com/a-aleesshin/gophprofile/internal/logging"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

func main() {
	ctx, cancel := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	_ = godotenv.Load(".env")

	cfg, err := config.LoadMigrate()
	if err != nil {
		slog.Error("failed to load the configuration", "error", err)
		os.Exit(1)
	}

	logger, err := logging.New(cfg.Log, os.Stdout)
	if err != nil {
		slog.Error("failed to build the logger", "error", err)
		os.Exit(1)
	}

	logger = logger.With("service", "file", "component", "migrate")

	pool, err := pgtx.Connect(ctx, cfg.Postgres)
	if err != nil {
		logger.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}

	defer pool.Close()

	logger.Info("applying migrations")

	if err := migrations.Up(ctx, pool); err != nil {
		logger.Error("failed to apply migrations", "error", err)
		os.Exit(1)
	}

	logger.Info("migrations applied")
}
