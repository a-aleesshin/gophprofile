package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/a-aleesshin/gophprofile/internal/logging"
	"github.com/a-aleesshin/gophprofile/profile/internal/app"
	"github.com/a-aleesshin/gophprofile/profile/internal/config"
)

func main() {
	ctx, cancel := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	_ = godotenv.Load(".env")

	cfg, err := config.LoadServer()
	if err != nil {
		slog.Error("failed to load the configuration", "error", err)
		os.Exit(1)
	}

	logger, err := logging.New(cfg.Log, os.Stdout)
	if err != nil {
		slog.Error("failed to build the logger", "error", err)
		os.Exit(1)
	}

	logger = logger.With("service", "profile", "component", "server")

	logger.Info("starting",
		"postgres_max_conns", cfg.Postgres.MaxConns,
		"http_addr", cfg.HTTP.Addr,
		"file_addr", cfg.File.Addr,
		"public_base", cfg.URLs.PublicBase,
		"max_size_bytes", cfg.Policy.MaxSizeBytes,
		"max_avatars_per_user", cfg.Policy.MaxAvatarsPerUser,
		"rest_timeout", cfg.REST.Timeout)

	runner, shutdown, err := app.BuildServer(ctx, cfg, logger)
	if err != nil {
		logger.Error("failed to build the server", "error", err)
		os.Exit(1)
	}

	defer shutdown()

	logger.Info("started")

	if err := runner.Run(ctx); err != nil && !errorIsShutdown(err) {
		logger.Error("stopped with an error", "error", err)
		shutdown()
		os.Exit(1)
	}

	logger.Info("stopped")
}

func errorIsShutdown(err error) bool {
	return errors.Is(err, context.Canceled)
}
