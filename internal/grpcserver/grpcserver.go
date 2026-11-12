package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"google.golang.org/grpc"
)

var ErrInvalidConfig = errors.New("invalid grpc server config")

const minTokenLength = 32

type Config struct {
	Addr            string        `env:"ADDR,required,notEmpty"`
	Token           string        `env:"TOKEN,required,notEmpty"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`
	MaxRecvMsgSize  int           `env:"MAX_RECV_MSG_SIZE" envDefault:"4194304"`
	MaxSendMsgSize  int           `env:"MAX_SEND_MSG_SIZE" envDefault:"4194304"`
}

type Server struct {
	server   *grpc.Server
	addr     string
	shutdown time.Duration
	logger   *slog.Logger
}

func New(cfg Config, logger *slog.Logger, register func(*grpc.Server)) (*Server, error) {
	switch {
	case cfg.Addr == "":
		return nil, fmt.Errorf("%w: address is required", ErrInvalidConfig)
	case cfg.ShutdownTimeout <= 0:
		return nil, fmt.Errorf("%w: shutdown timeout must be positive", ErrInvalidConfig)
	case cfg.MaxRecvMsgSize <= 0:
		return nil, fmt.Errorf("%w: max receive size must be positive", ErrInvalidConfig)
	case cfg.MaxSendMsgSize <= 0:
		return nil, fmt.Errorf("%w: max send size must be positive", ErrInvalidConfig)
	case len(cfg.Token) < minTokenLength:
		return nil, fmt.Errorf("%w: token must be at least %d bytes",
			ErrInvalidConfig, minTokenLength)
	case logger == nil:
		return nil, fmt.Errorf("%w: logger is required", ErrInvalidConfig)
	case register == nil:
		return nil, fmt.Errorf("%w: nothing to register", ErrInvalidConfig)
	}

	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(cfg.MaxRecvMsgSize),
		grpc.MaxSendMsgSize(cfg.MaxSendMsgSize),
		grpc.ChainUnaryInterceptor(
			recovery(logger),
			logging(logger),
			authorization(cfg.Token),
		),
		grpc.ChainStreamInterceptor(
			recoveryStream(logger),
			loggingStream(logger),
			authorizationStream(cfg.Token),
		),
	)

	register(server)

	return &Server{
		server:   server,
		addr:     cfg.Addr,
		shutdown: cfg.ShutdownTimeout,
		logger:   logger,
	}, nil
}

func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("grpc: listen on %s: %w", s.addr, err)
	}

	failed := make(chan error, 1)

	go func() {
		s.logger.Info("grpc server listening", "addr", listener.Addr().String())

		if serveErr := s.server.Serve(listener); serveErr != nil &&
			!errors.Is(serveErr, grpc.ErrServerStopped) {
			failed <- fmt.Errorf("grpc: serve: %w", serveErr)

			return
		}

		failed <- nil
	}()

	select {
	case serveErr := <-failed:
		return serveErr
	case <-ctx.Done():
	}

	stopped := make(chan struct{})

	go func() {
		s.server.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		s.logger.Info("grpc server stopped")
	case <-time.After(s.shutdown):
		s.logger.Warn("grpc server did not stop in time, forcing it", "after", s.shutdown)
		s.server.Stop()
	}

	return ctx.Err()
}
