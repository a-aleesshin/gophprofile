package grpcserver

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpchealth "google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	testToken  = "0123456789abcdef0123456789abcdef"
	echoMethod = "/gophprofile.test.v1.Echo/Ping"
)

type echo struct {
	panics bool
	err    error
	calls  int
}

func (e *echo) ping(context.Context) (*healthpb.HealthCheckResponse, error) {
	e.calls++

	if e.panics {
		panic("the decoder gave up")
	}

	if e.err != nil {
		return nil, e.err
	}

	return &healthpb.HealthCheckResponse{
		Status: healthpb.HealthCheckResponse_SERVING,
	}, nil
}

var echoDesc = grpc.ServiceDesc{
	ServiceName: "gophprofile.test.v1.Echo",
	HandlerType: (*any)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "Ping",
		Handler: func(
			srv any,
			ctx context.Context,
			decode func(any) error,
			interceptor grpc.UnaryServerInterceptor,
		) (any, error) {
			request := new(healthpb.HealthCheckRequest)
			if err := decode(request); err != nil {
				return nil, err
			}

			service, ok := srv.(*echo)
			if !ok {
				return nil, errors.New("unexpected service")
			}

			if interceptor == nil {
				return service.ping(ctx)
			}

			return interceptor(ctx, request,
				&grpc.UnaryServerInfo{Server: srv, FullMethod: echoMethod},
				func(ctx context.Context, _ any) (any, error) { return service.ping(ctx) })
		},
	}},
}

func serve(t *testing.T, logs *bytes.Buffer, service *echo) *grpc.ClientConn {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	addr := freeAddr(t)

	server, err := New(testConfig(addr), logger, func(s *grpc.Server) {
		s.RegisterService(&echoDesc, service)
		healthpb.RegisterHealthServer(s, grpchealth.NewServer())
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- server.Run(ctx) }()

	t.Cleanup(func() {
		cancel()
		<-done
	})

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	require.Eventually(t, func() bool {
		connection, dialErr := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if dialErr != nil {
			return false
		}

		_ = connection.Close()

		return true
	}, 5*time.Second, 20*time.Millisecond)

	return conn
}

func bearer(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, authorizationKey, bearerPrefix+token)
}

func callEcho(ctx context.Context, conn *grpc.ClientConn) error {
	return conn.Invoke(ctx, echoMethod,
		&healthpb.HealthCheckRequest{}, &healthpb.HealthCheckResponse{})
}

func TestRecoveryKeepsTheProcessAlive(t *testing.T) {
	t.Parallel()

	// Arrange
	var logs bytes.Buffer

	service := &echo{panics: true}
	conn := serve(t, &logs, service)

	// Act
	err := callEcho(bearer(t.Context(), testToken), conn)

	// Assert
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.Equal(t, "internal error", status.Convert(err).Message(),
		"the panic text is for the log, not for the caller")

	assert.Contains(t, logs.String(), "a grpc handler panicked")
	assert.Contains(t, logs.String(), "the decoder gave up")
	assert.Contains(t, logs.String(), "stack=")

	service.panics = false

	require.NoError(t, callEcho(bearer(t.Context(), testToken), conn),
		"grpc-go does not recover panics, so without this the process would be gone")
}

func TestLoggingKeepsTheErrorTheCallerDoesNotSee(t *testing.T) {
	t.Parallel()

	// Arrange
	var logs bytes.Buffer

	conn := serve(t, &logs, &echo{
		err: status.Error(codes.Internal, "pq: relation \"files\" does not exist"),
	})

	// Act
	err := callEcho(bearer(t.Context(), testToken), conn)

	// Assert
	require.Error(t, err)
	assert.Contains(t, logs.String(), "grpc call failed")
	assert.Contains(t, logs.String(), "code=Internal")
	assert.Contains(t, logs.String(), "does not exist",
		"the mapping withholds the detail from the caller, so the log is where it must land")
}

func TestAuthorizationRefusesWhatItCannotVerify(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	service := &echo{}
	conn := serve(t, &logs, service)

	tests := []struct {
		name string
		ctx  func(context.Context) context.Context
	}{
		{
			name: "no metadata at all",
			ctx:  func(ctx context.Context) context.Context { return ctx },
		},
		{
			name: "another header but no token",
			ctx: func(ctx context.Context) context.Context {
				return metadata.AppendToOutgoingContext(ctx, "x-request-id", "1")
			},
		},
		{
			name: "a wrong token",
			ctx:  func(ctx context.Context) context.Context { return bearer(ctx, "nope") },
		},
		{
			name: "the right token without the scheme",
			ctx: func(ctx context.Context) context.Context {
				return metadata.AppendToOutgoingContext(ctx, authorizationKey, testToken)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act
			err := callEcho(test.ctx(t.Context()), conn)

			// Assert
			require.Error(t, err)
			assert.Equal(t, codes.Unauthenticated, status.Code(err))
		})
	}

	assert.Zero(t, service.calls, "an unauthorized call must not reach the handler")
}

func TestAuthorizationAcceptsTheRightToken(t *testing.T) {
	t.Parallel()

	// Arrange
	var logs bytes.Buffer

	service := &echo{}
	conn := serve(t, &logs, service)

	// Act
	err := callEcho(bearer(t.Context(), testToken), conn)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, 1, service.calls)
	assert.Contains(t, logs.String(), "grpc call served")
}

func TestAuthorizationLetsTheHealthProbeThrough(t *testing.T) {
	t.Parallel()

	// Arrange
	var logs bytes.Buffer

	conn := serve(t, &logs, &echo{})

	// Act
	response, err := healthpb.NewHealthClient(conn).
		Check(t.Context(), &healthpb.HealthCheckRequest{})

	// Assert
	require.NoError(t, err,
		"the orchestrator probes health without knowing our inter-service token")
	assert.Equal(t, healthpb.HealthCheckResponse_SERVING, response.GetStatus())

	assert.NotContains(t, logs.String(), "grpc call served",
		"probes run every few seconds and must not drown the log")
}

func TestNewRefusesAWeakToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token string
	}{
		{name: "no token", token: ""},
		{name: "a short token", token: "secret"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			cfg := testConfig(":0")
			cfg.Token = test.token

			// Act
			server, err := New(cfg, discard(), func(*grpc.Server) {})

			// Assert
			require.ErrorIs(t, err, ErrInvalidConfig)
			assert.Nil(t, server)
		})
	}
}
