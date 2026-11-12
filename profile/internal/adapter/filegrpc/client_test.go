package filegrpc

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	filev1 "github.com/a-aleesshin/gophprofile/file/gen/file/v1"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

const testToken = "0123456789abcdef0123456789abcdef"

var testExpiry = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

type fakeFileService struct {
	filev1.UnimplementedFileServiceServer

	ticket     *filev1.CreateUploadResponse
	completion *filev1.CompleteUploadResponse
	err        error

	failures atomic.Int32
	calls    atomic.Int32
	delay    time.Duration

	request  *filev1.CreateUploadRequest
	deadline bool
}

func (s *fakeFileService) CreateUpload(
	ctx context.Context,
	request *filev1.CreateUploadRequest,
) (*filev1.CreateUploadResponse, error) {
	s.calls.Add(1)
	s.request = request
	_, s.deadline = ctx.Deadline()

	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if s.failures.Load() > 0 {
		s.failures.Add(-1)

		return nil, status.Error(codes.Unavailable, "file is restarting")
	}

	if s.err != nil {
		return nil, s.err
	}

	return s.ticket, nil
}

func (s *fakeFileService) CompleteUpload(
	context.Context,
	*filev1.CompleteUploadRequest,
) (*filev1.CompleteUploadResponse, error) {
	s.calls.Add(1)

	if s.err != nil {
		return nil, s.err
	}

	return s.completion, nil
}

func testClient(t *testing.T, service *fakeFileService) (*Client, *[]string) {
	t.Helper()

	return testClientWithTimeout(t, service, 2*time.Second)
}

func testClientWithTimeout(
	t *testing.T, service *fakeFileService, timeout time.Duration,
) (*Client, *[]string) {
	t.Helper()

	tokens := new([]string)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := grpc.NewServer(grpc.UnaryInterceptor(func(
		ctx context.Context,
		request any,
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		*tokens = append(*tokens, md.Get("authorization")...)

		return handler(ctx, request)
	}))

	filev1.RegisterFileServiceServer(server, service)

	go func() { _ = server.Serve(listener) }()

	t.Cleanup(server.Stop)

	client, err := New(Config{
		Addr:    listener.Addr().String(),
		Token:   testToken,
		Timeout: timeout,
	})
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, client.Close()) })

	return client, tokens
}

func TestClientIssuesAnUploadTicket(t *testing.T) {
	t.Parallel()

	// Arrange
	service := &fakeFileService{ticket: &filev1.CreateUploadResponse{
		UploadId:  "0193abcd-ef01-7a23-8b45-0123456789ab",
		Url:       "https://domain.com/s3/gophprofile",
		Fields:    map[string]string{"key": "staging/ticket", "policy": "base64"},
		ExpiresAt: timestamppb.New(testExpiry),
	}}

	client, tokens := testClient(t, service)

	// Act
	ticket, err := client.CreateUpload(t.Context(), usecase.CreateUploadParams{
		Namespace:      "avatar",
		OwnerRef:       "user-1",
		OriginalName:   "avatar.jpg",
		DeclaredMime:   "image/jpeg",
		MaxSizeBytes:   2048,
		IdempotencyKey: "0193abcd-ef01-7a23-8b45-0123456789ab",
	})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "0193abcd-ef01-7a23-8b45-0123456789ab", ticket.UploadID)
	assert.Equal(t, "https://domain.com/s3/gophprofile", ticket.URL)
	assert.Equal(t, "staging/ticket", ticket.Fields["key"])
	assert.True(t, ticket.ExpiresAt.Equal(testExpiry))

	assert.Equal(t, "avatar", service.request.GetNamespace())
	assert.Equal(t, "user-1", service.request.GetOwnerRef())
	assert.Equal(t, "image/jpeg", service.request.GetDeclaredMime())
	assert.Equal(t, int64(2048), service.request.GetMaxSizeBytes())
	assert.Equal(t, "0193abcd-ef01-7a23-8b45-0123456789ab", service.request.GetIdempotencyKey(),
		"the key is what makes a retried start-upload return the same ticket")

	require.Len(t, *tokens, 1)
	assert.Equal(t, "Bearer "+testToken, (*tokens)[0],
		"this exact shape is what the server compares in constant time")

	assert.True(t, service.deadline, "no call may reach file without a deadline")
}

func TestClientConfirmsAnUpload(t *testing.T) {
	t.Parallel()

	// Arrange
	client, _ := testClient(t, &fakeFileService{
		completion: &filev1.CompleteUploadResponse{
			FileId: "0193abcd-ef01-7a23-8b45-0123456789ab",
			Status: filev1.FileStatus_FILE_STATUS_RECEIVED,
		},
	})

	// Act
	confirmation, err := client.CompleteUpload(t.Context(), "upload-1")

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "0193abcd-ef01-7a23-8b45-0123456789ab", confirmation.FileID)
}

func TestClientRefusesAnEmptyUploadID(t *testing.T) {
	t.Parallel()

	service := &fakeFileService{}
	client, _ := testClient(t, service)

	_, err := client.CompleteUpload(t.Context(), "")

	require.ErrorIs(t, err, usecase.ErrInvalidCommand)
	assert.Zero(t, service.calls.Load(), "an empty id must not cost a round trip")
}

func TestClientTranslatesWhatFileAnswers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code codes.Code
		want error
	}{
		{name: "invalid argument", code: codes.InvalidArgument, want: usecase.ErrInvalidCommand},
		{name: "not found", code: codes.NotFound, want: usecase.ErrUploadNotFound},
		{
			name: "failed precondition",
			code: codes.FailedPrecondition,
			want: usecase.ErrUploadNotUsable,
		},
		{name: "resource exhausted", code: codes.ResourceExhausted, want: usecase.ErrFileTooLarge},
		{
			name: "unavailable",
			code: codes.Unavailable,
			want: usecase.ErrFileServiceUnavailable,
		},
		{
			name: "deadline exceeded",
			code: codes.DeadlineExceeded,
			want: usecase.ErrFileServiceTimeout,
		},
		{name: "canceled", code: codes.Canceled, want: context.Canceled},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			client, _ := testClient(t, &fakeFileService{
				err: status.Error(test.code, "nope"),
			})

			// Act
			_, err := client.CompleteUpload(t.Context(), "upload-1")

			// Assert
			require.ErrorIs(t, err, test.want,
				"the http layer branches on these, a bare status would become 500")
		})
	}
}

func TestClientKeepsAnUnknownFailureOpaque(t *testing.T) {
	t.Parallel()

	client, _ := testClient(t, &fakeFileService{
		err: status.Error(codes.Internal, "boom"),
	})

	_, err := client.CompleteUpload(t.Context(), "upload-1")

	require.Error(t, err)
	assert.NotErrorIs(t, err, usecase.ErrUploadNotUsable)
	assert.NotErrorIs(t, err, usecase.ErrFileServiceUnavailable,
		"an internal error of file is our bug, not a retryable condition")
	assert.ErrorContains(t, err, "Internal")
}

func TestClientRefusesATicketItCannotUse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ticket *filev1.CreateUploadResponse
	}{
		{
			name: "no upload id",
			ticket: &filev1.CreateUploadResponse{
				Url: "https://domain.com/s3", ExpiresAt: timestamppb.New(testExpiry),
			},
		},
		{
			name: "no url",
			ticket: &filev1.CreateUploadResponse{
				UploadId: "upload-1", ExpiresAt: timestamppb.New(testExpiry),
			},
		},
		{
			name: "no expiry",
			ticket: &filev1.CreateUploadResponse{
				UploadId: "upload-1", Url: "https://domain.com/s3",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client, _ := testClient(t, &fakeFileService{ticket: test.ticket})

			_, err := client.CreateUpload(t.Context(), usecase.CreateUploadParams{})

			require.ErrorIs(t, err, usecase.ErrUploadNotUsable,
				"storing a broken ticket would strand the avatar in pending forever")
		})
	}
}

func TestClientRetriesWhileFileRestarts(t *testing.T) {
	t.Parallel()

	// Arrange
	service := &fakeFileService{ticket: &filev1.CreateUploadResponse{
		UploadId:  "upload-1",
		Url:       "https://domain.com/s3",
		ExpiresAt: timestamppb.New(testExpiry),
	}}
	service.failures.Store(2)

	client, _ := testClient(t, service)

	// Act
	ticket, err := client.CreateUpload(t.Context(), usecase.CreateUploadParams{
		IdempotencyKey: "avatar-1",
	})

	// Assert
	require.NoError(t, err, "create upload is idempotent by key, so a retry is free")
	assert.Equal(t, "upload-1", ticket.UploadID)
	assert.Equal(t, int32(3), service.calls.Load())
}

func TestClientGivesUpWhenFileStaysDown(t *testing.T) {
	t.Parallel()

	// Arrange
	service := &fakeFileService{}
	service.failures.Store(10)

	client, _ := testClient(t, service)

	// Act
	_, err := client.CreateUpload(t.Context(), usecase.CreateUploadParams{})

	// Assert
	require.ErrorIs(t, err, usecase.ErrFileServiceUnavailable)
	assert.Equal(t, int32(3), service.calls.Load(), "three attempts, not forever")
}

func TestClientStopsWaitingOnItsOwnDeadline(t *testing.T) {
	t.Parallel()

	// Arrange
	service := &fakeFileService{delay: time.Second}
	client, _ := testClientWithTimeout(t, service, 100*time.Millisecond)

	started := time.Now()

	// Act
	_, err := client.CreateUpload(t.Context(), usecase.CreateUploadParams{})

	// Assert
	require.ErrorIs(t, err, usecase.ErrFileServiceTimeout,
		"a timeout and an outage become different http codes, 504 and 503")
	assert.Less(t, time.Since(started), 900*time.Millisecond,
		"a slow file must not hold an http request of profile open")
}

func TestClientKeepsTheCallersDeadline(t *testing.T) {
	t.Parallel()

	// Arrange
	service := &fakeFileService{delay: time.Second}
	client, _ := testClientWithTimeout(t, service, time.Minute)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	started := time.Now()

	// Act
	_, err := client.CreateUpload(ctx, usecase.CreateUploadParams{})

	// Assert
	require.Error(t, err)
	assert.Less(t, time.Since(started), 900*time.Millisecond,
		"the client timeout is a ceiling: a tighter caller deadline still wins, which "+
			"stops holding true the moment the interceptor detaches the context")
}

func TestNewRejectsIncompleteConfig(t *testing.T) {
	t.Parallel()

	full := Config{Addr: "file:9090", Token: testToken, Timeout: time.Second}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "address", mutate: func(c *Config) { c.Addr = "" }},
		{name: "token", mutate: func(c *Config) { c.Token = "" }},
		{name: "short token", mutate: func(c *Config) { c.Token = "too-short" }},
		{name: "timeout", mutate: func(c *Config) { c.Timeout = 0 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg := full
			test.mutate(&cfg)

			_, err := New(cfg)
			require.ErrorIs(t, err, ErrInvalidConfig)
		})
	}

	client, err := New(full)
	require.NoError(t, err)
	require.NoError(t, client.Close())
}

func TestNewWithConnRejectsNothingToTalkTo(t *testing.T) {
	t.Parallel()

	_, err := NewWithConn(nil, time.Second)
	require.ErrorIs(t, err, ErrInvalidConfig)

	_, err = NewWithConn(&grpc.ClientConn{}, 0)
	require.ErrorIs(t, err, ErrInvalidConfig)
}

func TestClientClosesWithoutAConnection(t *testing.T) {
	t.Parallel()

	client, err := NewWithConn(&grpc.ClientConn{}, time.Second)
	require.NoError(t, err)

	assert.NoError(t, client.Close(), "a client that borrowed a connection must not close it")
}

func TestClientFitsThePortsItWasWrittenFor(t *testing.T) {
	t.Parallel()

	client, err := NewWithConn(&grpc.ClientConn{}, time.Second)
	require.NoError(t, err)

	// The ports are unexported, so handing the client to the handlers that declare
	// them is the only way to assert the fit, and it fails at compile time.
	assert.NotNil(t, usecase.NewStartUploadHandler(nil, nil, nil, client, nil, nil))
	assert.NotNil(t, usecase.NewCompleteUploadHandler(nil, client, nil, nil))
}
