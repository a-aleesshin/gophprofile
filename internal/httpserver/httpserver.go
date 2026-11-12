package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

var ErrInvalidConfig = errors.New("invalid http server config")

type Config struct {
	Addr            string        `env:"ADDR,required,notEmpty"`
	ReadTimeout     time.Duration `env:"READ_TIMEOUT" envDefault:"5s"`
	WriteTimeout    time.Duration `env:"WRITE_TIMEOUT" envDefault:"30s"`
	IdleTimeout     time.Duration `env:"IDLE_TIMEOUT" envDefault:"60s"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`
}

type Server struct {
	name     string
	server   *http.Server
	shutdown time.Duration
	logger   *slog.Logger
}

func New(name string, handler http.Handler, cfg Config, logger *slog.Logger) (*Server, error) {
	switch {
	case name == "":
		return nil, fmt.Errorf("%w: name is required", ErrInvalidConfig)
	case handler == nil:
		return nil, fmt.Errorf("%w: handler is required", ErrInvalidConfig)
	case cfg.Addr == "":
		return nil, fmt.Errorf("%w: address is required", ErrInvalidConfig)
	case cfg.ShutdownTimeout <= 0:
		return nil, fmt.Errorf("%w: shutdown timeout must be positive", ErrInvalidConfig)
	case logger == nil:
		return nil, fmt.Errorf("%w: logger is required", ErrInvalidConfig)
	}

	return &Server{
		name: name,
		server: &http.Server{
			Addr:              cfg.Addr,
			Handler:           handler,
			ReadHeaderTimeout: cfg.ReadTimeout,
			ReadTimeout:       cfg.ReadTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
		},
		shutdown: cfg.ShutdownTimeout,
		logger:   logger,
	}, nil
}

func (s *Server) Run(ctx context.Context) error {
	failed := make(chan error, 1)

	go func() {
		s.logger.Info("http server listening", "server", s.name, "addr", s.server.Addr)

		if err := s.server.ListenAndServe(); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			failed <- fmt.Errorf("%s: listen on %s: %w", s.name, s.server.Addr, err)

			return
		}

		failed <- nil
	}()

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}

	stopping, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.shutdown)
	defer cancel()

	if err := s.server.Shutdown(stopping); err != nil {
		return fmt.Errorf("%s: shutdown: %w", s.name, err)
	}

	s.logger.Info("http server stopped", "server", s.name)

	return ctx.Err()
}
