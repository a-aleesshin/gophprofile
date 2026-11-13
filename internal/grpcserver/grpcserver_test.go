package grpcserver

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	grpchealth "google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func freeAddr(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	return addr
}

func testConfig(addr string) Config {
	return Config{
		Addr:            addr,
		Token:           "0123456789abcdef0123456789abcdef",
		ShutdownTimeout: time.Second,
		MaxRecvMsgSize:  1 << 20,
		MaxSendMsgSize:  1 << 20,
	}
}

func TestServerServesAndStopsOnContext(t *testing.T) {
	t.Parallel()

	// Arrange
	addr := freeAddr(t)

	server, err := New(testConfig(addr), discard(), func(s *grpc.Server) {
		healthpb.RegisterHealthServer(s, grpchealth.NewServer())
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- server.Run(ctx) }()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	client := healthpb.NewHealthClient(conn)

	// Act
	require.Eventually(t, func() bool {
		response, checkErr := client.Check(t.Context(), &healthpb.HealthCheckRequest{})

		return checkErr == nil &&
			response.GetStatus() == healthpb.HealthCheckResponse_SERVING
	}, 5*time.Second, 20*time.Millisecond)

	cancel()

	// Assert
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not stop after the context was cancelled")
	}

	listener, err := net.Listen("tcp", addr)
	require.NoError(t, err, "the port must be released after shutdown")
	require.NoError(t, listener.Close())
}

func TestServerReportsAPortItCannotTake(t *testing.T) {
	t.Parallel()

	// Arrange
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = listener.Close() })

	server, err := New(testConfig(listener.Addr().String()), discard(), func(*grpc.Server) {})
	require.NoError(t, err)

	// Act
	runErr := server.Run(t.Context())

	// Assert
	require.Error(t, runErr)
	assert.ErrorContains(t, runErr, "grpc: listen")
}

func TestNewRejectsAnIncompleteServer(t *testing.T) {
	t.Parallel()

	register := func(*grpc.Server) {}

	tests := []struct {
		name     string
		cfg      Config
		logger   *slog.Logger
		register func(*grpc.Server)
	}{
		{
			name: "no address",
			cfg: Config{
				Token: "0123456789abcdef0123456789abcdef", ShutdownTimeout: time.Second,
				MaxRecvMsgSize: 1, MaxSendMsgSize: 1,
			},
			logger: discard(), register: register,
		},
		{
			name: "no shutdown timeout",
			cfg: Config{
				Addr: ":0", Token: "0123456789abcdef0123456789abcdef", MaxRecvMsgSize: 1,
				MaxSendMsgSize: 1,
			},
			logger: discard(), register: register,
		},
		{
			name: "no receive limit",
			cfg: Config{
				Addr: ":0", Token: "0123456789abcdef0123456789abcdef",
				ShutdownTimeout: time.Second,
			},
			logger: discard(), register: register,
		},
		{name: "no logger", cfg: testConfig(":0"), register: register},
		{name: "nothing to register", cfg: testConfig(":0"), logger: discard()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Act
			server, err := New(test.cfg, test.logger, test.register)

			// Assert
			require.ErrorIs(t, err, ErrInvalidConfig)
			assert.Nil(t, server)
		})
	}
}
