package grpcserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
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
	"google.golang.org/grpc/reflection"
	reflectpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
)

const (
	testToken        = "0123456789abcdef0123456789abcdef"
	echoMethod       = "/gophprofile.test.v1.Echo/Ping"
	echoStreamMethod = "/gophprofile.test.v1.Echo/Stream"
	reflectionMethod = "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo"
)

type safeBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *safeBuffer) Write(entry []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buffer.Write(entry)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buffer.String()
}

type echo struct {
	panics      bool
	err         error
	calls       int
	streamCalls int
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

func (e *echo) stream() error {
	e.streamCalls++

	if e.panics {
		panic("the stream gave up")
	}

	return e.err
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
	Streams: []grpc.StreamDesc{{
		StreamName:    "Stream",
		ServerStreams: true,
		Handler: func(srv any, stream grpc.ServerStream) error {
			service, ok := srv.(*echo)
			if !ok {
				return errors.New("unexpected service")
			}

			if err := service.stream(); err != nil {
				return err
			}

			return stream.SendMsg(&healthpb.HealthCheckResponse{
				Status: healthpb.HealthCheckResponse_SERVING,
			})
		},
	}},
}

func serve(t *testing.T, logs *safeBuffer, service *echo) *grpc.ClientConn {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	addr := freeAddr(t)

	server, err := New(testConfig(addr), logger, func(s *grpc.Server) {
		s.RegisterService(&echoDesc, service)
		healthpb.RegisterHealthServer(s, grpchealth.NewServer())
		reflection.Register(s)
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- server.Run(ctx) }()

	t.Cleanup(func() {
		cancel()

		assert.ErrorIs(t, <-done, context.Canceled)
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

func callEchoStream(ctx context.Context, conn *grpc.ClientConn) error {
	stream, err := conn.NewStream(ctx, &echoDesc.Streams[0], echoStreamMethod)
	if err != nil {
		return err
	}

	if err := stream.SendMsg(&healthpb.HealthCheckRequest{}); err != nil &&
		!errors.Is(err, io.EOF) {
		return err
	}

	return stream.RecvMsg(&healthpb.HealthCheckResponse{})
}

func listServices(ctx context.Context, conn *grpc.ClientConn) ([]string, error) {
	stream, err := reflectpb.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, err
	}

	request := &reflectpb.ServerReflectionRequest{
		MessageRequest: &reflectpb.ServerReflectionRequest_ListServices{},
	}

	if err := stream.Send(request); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	response, err := stream.Recv()
	if err != nil {
		return nil, err
	}

	services := response.GetListServicesResponse().GetService()
	names := make([]string, 0, len(services))

	for _, service := range services {
		names = append(names, service.GetName())
	}

	return names, nil
}

func TestRecoveryKeepsTheProcessAlive(t *testing.T) {
	t.Parallel()

	// Arrange
	var logs safeBuffer

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
	var logs safeBuffer

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

	var logs safeBuffer

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
	var logs safeBuffer

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
	var logs safeBuffer

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

func TestAuthorizationRefusesReflectionWithoutAToken(t *testing.T) {
	t.Parallel()

	// Arrange
	var logs safeBuffer

	conn := serve(t, &logs, &echo{})

	// Act
	names, err := listServices(t.Context(), conn)

	// Assert
	require.Error(t, err,
		"reflection is a stream, so a unary-only chain used to hand out the whole schema")
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Empty(t, names)
}

func TestAuthorizationLetsReflectionThroughWithTheToken(t *testing.T) {
	t.Parallel()

	// Arrange
	var logs safeBuffer

	conn := serve(t, &logs, &echo{})

	// Act
	names, err := listServices(bearer(t.Context(), testToken), conn)

	// Assert
	require.NoError(t, err, "grpcurl with the token stays usable, which is the point of keeping it")
	assert.Contains(t, names, echoDesc.ServiceName)
}

func TestAuthorizationRefusesAStreamWithoutAToken(t *testing.T) {
	t.Parallel()

	// Arrange
	var logs safeBuffer

	service := &echo{}
	conn := serve(t, &logs, service)

	// Act
	err := callEchoStream(t.Context(), conn)

	// Assert
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Zero(t, service.streamCalls, "an unauthorized stream must not reach the handler")
}

func TestAuthorizationAcceptsAStreamWithTheRightToken(t *testing.T) {
	t.Parallel()

	// Arrange
	var logs safeBuffer

	service := &echo{}
	conn := serve(t, &logs, service)

	// Act
	err := callEchoStream(bearer(t.Context(), testToken), conn)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, 1, service.streamCalls)
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		assert.Contains(collect, logs.String(), "kind=stream")
	}, time.Second, 10*time.Millisecond,
		"a stream reports under its own kind so its minutes stay out of the unary histogram")
}

func TestRecoveryKeepsTheProcessAliveOnAStream(t *testing.T) {
	t.Parallel()

	// Arrange
	var logs safeBuffer

	service := &echo{panics: true}
	conn := serve(t, &logs, service)

	// Act
	err := callEchoStream(bearer(t.Context(), testToken), conn)

	// Assert
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.Equal(t, "internal error", status.Convert(err).Message())

	assert.Contains(t, logs.String(), "a grpc handler panicked")
	assert.Contains(t, logs.String(), "the stream gave up")

	service.panics = false

	require.NoError(t, callEchoStream(bearer(t.Context(), testToken), conn),
		"grpc-go does not recover a stream handler either")
}

func TestAuthorizationLetsTheHealthWatchThrough(t *testing.T) {
	t.Parallel()

	// Arrange
	var logs safeBuffer

	conn := serve(t, &logs, &echo{})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Act
	watch, err := healthpb.NewHealthClient(conn).Watch(ctx, &healthpb.HealthCheckRequest{})
	require.NoError(t, err)

	response, err := watch.Recv()

	// Assert
	require.NoError(t, err,
		"Watch is a stream, and the exemption has to cover the whole health service")
	assert.Equal(t, healthpb.HealthCheckResponse_SERVING, response.GetStatus())
}
