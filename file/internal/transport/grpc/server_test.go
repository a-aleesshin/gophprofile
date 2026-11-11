package grpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
	filev1 "github.com/a-aleesshin/gophprofile/gen/file/v1"
)

const testFileID = "0193abcd-ef01-7a23-8b45-0123456789ab"

type stubCreateUpload struct {
	cmd    usecase.CreateUploadCommand
	result usecase.CreateUploadResult
	err    error
	calls  int
}

func (s *stubCreateUpload) Handle(
	_ context.Context,
	cmd usecase.CreateUploadCommand,
) (usecase.CreateUploadResult, error) {
	s.cmd = cmd
	s.calls++

	return s.result, s.err
}

type stubCompleteUpload struct {
	result usecase.CompleteUploadResult
	err    error
}

func (s *stubCompleteUpload) Handle(
	context.Context,
	usecase.CompleteUploadCommand,
) (usecase.CompleteUploadResult, error) {
	return s.result, s.err
}

type stubGetFile struct {
	result usecase.GetFileResult
	err    error
}

func (s *stubGetFile) Handle(
	context.Context,
	usecase.GetFileCommand,
) (usecase.GetFileResult, error) {
	return s.result, s.err
}

type stubGetFiles struct {
	cmd    usecase.GetFilesCommand
	result usecase.GetFilesResult
	err    error
}

func (s *stubGetFiles) Handle(
	_ context.Context,
	cmd usecase.GetFilesCommand,
) (usecase.GetFilesResult, error) {
	s.cmd = cmd

	return s.result, s.err
}

type stubCreateLink struct {
	cmd    usecase.CreateAccessLinkCommand
	result usecase.CreateAccessLinkResult
	err    error
}

func (s *stubCreateLink) Handle(
	_ context.Context,
	cmd usecase.CreateAccessLinkCommand,
) (usecase.CreateAccessLinkResult, error) {
	s.cmd = cmd

	return s.result, s.err
}

type stubDeleteFile struct {
	cmd    usecase.DeleteFileCommand
	result usecase.DeleteFileResult
	err    error
}

func (s *stubDeleteFile) Handle(
	_ context.Context,
	cmd usecase.DeleteFileCommand,
) (usecase.DeleteFileResult, error) {
	s.cmd = cmd

	return s.result, s.err
}

func fullHandlers() Handlers {
	return Handlers{
		CreateUpload:     &stubCreateUpload{},
		CompleteUpload:   &stubCompleteUpload{},
		GetFile:          &stubGetFile{},
		GetFiles:         &stubGetFiles{},
		CreateAccessLink: &stubCreateLink{},
		DeleteFile:       &stubDeleteFile{},
	}
}

func dial(t *testing.T, handlers Handlers) filev1.FileServiceClient {
	t.Helper()

	server, err := NewServer(handlers)
	require.NoError(t, err)

	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()

	filev1.RegisterFileServiceServer(grpcServer, server)

	go func() { _ = grpcServer.Serve(listener) }()

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = conn.Close()
		grpcServer.Stop()
	})

	return filev1.NewFileServiceClient(conn)
}

func testFileView(t *testing.T) usecase.FileView {
	t.Helper()

	fileID, err := domain.ParseFileID(testFileID)
	require.NoError(t, err)

	namespace, err := domain.NewNamespace("avatar")
	require.NoError(t, err)

	return usecase.FileView{
		ID:        fileID,
		Namespace: namespace,
		OwnerRef:  "user-1",
		MimeType:  "image/jpeg",
		SizeBytes: 2048,
		Width:     800,
		Height:    600,
		Status:    domain.FileStatusReady,
		CreatedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
	}
}

func TestCreateUpload(t *testing.T) {
	t.Parallel()

	// Arrange
	uploadID, err := domain.NewUploadID()
	require.NoError(t, err)

	expiresAt := time.Date(2026, 11, 1, 12, 15, 0, 0, time.UTC)

	creator := &stubCreateUpload{result: usecase.CreateUploadResult{
		UploadID:  uploadID,
		URL:       "https://domain.com/s3/gophprofile",
		Fields:    map[string]string{"key": "staging/" + uploadID.String()},
		ExpiresAt: expiresAt,
	}}

	handlers := fullHandlers()
	handlers.CreateUpload = creator

	client := dial(t, handlers)

	// Act
	response, err := client.CreateUpload(t.Context(), &filev1.CreateUploadRequest{
		Namespace:      "avatar",
		OwnerRef:       "user-1",
		OriginalName:   "photo.jpg",
		DeclaredMime:   "image/jpeg",
		MaxSizeBytes:   1024,
		IdempotencyKey: "avatar-1",
	})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, uploadID.String(), response.GetUploadId())
	assert.Equal(t, "https://domain.com/s3/gophprofile", response.GetUrl())
	assert.Equal(t, map[string]string{"key": "staging/" + uploadID.String()},
		response.GetFields())
	assert.Equal(t, expiresAt, response.GetExpiresAt().AsTime())

	assert.Equal(t, "avatar", creator.cmd.Namespace.String())
	assert.Equal(t, "user-1", creator.cmd.OwnerRef.String())
	assert.Equal(t, "photo.jpg", creator.cmd.OriginalName.String())
	assert.Equal(t, "image/jpeg", creator.cmd.DeclaredMime.String())
	assert.Equal(t, int64(1024), creator.cmd.MaxSize.Bytes())
	assert.Equal(t, "avatar-1", creator.cmd.IdempotencyKey.String())
}

func TestCreateUploadLeavesOptionalFieldsZero(t *testing.T) {
	t.Parallel()

	// Arrange
	creator := &stubCreateUpload{}

	handlers := fullHandlers()
	handlers.CreateUpload = creator

	client := dial(t, handlers)

	// Act
	_, err := client.CreateUpload(t.Context(), &filev1.CreateUploadRequest{
		Namespace:      "avatar",
		OwnerRef:       "user-1",
		OriginalName:   "photo.jpg",
		IdempotencyKey: "avatar-1",
	})

	// Assert
	require.NoError(t, err)
	assert.True(t, creator.cmd.DeclaredMime.IsZero(),
		"an absent mime must stay absent, not become an empty value object")
	assert.True(t, creator.cmd.MaxSize.IsZero())
}

func TestRejectsBrokenArgumentsBeforeTheUseCase(t *testing.T) {
	t.Parallel()

	creator := &stubCreateUpload{}

	handlers := fullHandlers()
	handlers.CreateUpload = creator

	client := dial(t, handlers)

	tests := []struct {
		name    string
		request *filev1.CreateUploadRequest
	}{
		{
			name: "no namespace",
			request: &filev1.CreateUploadRequest{
				OwnerRef: "user-1", OriginalName: "a.jpg", IdempotencyKey: "k",
			},
		},
		{
			name: "no owner",
			request: &filev1.CreateUploadRequest{
				Namespace: "avatar", OriginalName: "a.jpg", IdempotencyKey: "k",
			},
		},
		{
			name: "broken mime",
			request: &filev1.CreateUploadRequest{
				Namespace: "avatar", OwnerRef: "user-1", OriginalName: "a.jpg",
				IdempotencyKey: "k", DeclaredMime: "not a mime",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act
			_, err := client.CreateUpload(t.Context(), test.request)

			// Assert
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}

	assert.Zero(t, creator.calls, "a broken request must not reach the use case")
}

func TestCompleteUpload(t *testing.T) {
	t.Parallel()

	// Arrange
	fileID, err := domain.ParseFileID(testFileID)
	require.NoError(t, err)

	uploadID, err := domain.NewUploadID()
	require.NoError(t, err)

	handlers := fullHandlers()
	handlers.CompleteUpload = &stubCompleteUpload{result: usecase.CompleteUploadResult{
		FileID: fileID,
		Status: domain.FileStatusProcessing,
	}}

	client := dial(t, handlers)

	// Act
	response, err := client.CompleteUpload(t.Context(), &filev1.CompleteUploadRequest{
		UploadId: uploadID.String(),
	})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, testFileID, response.GetFileId())
	assert.Equal(t, filev1.FileStatus_FILE_STATUS_PROCESSING, response.GetStatus())
}

func TestGetFile(t *testing.T) {
	t.Parallel()

	// Arrange
	handlers := fullHandlers()
	handlers.GetFile = &stubGetFile{result: usecase.GetFileResult{
		File: testFileView(t),
		Variants: []usecase.VariantView{{
			Name: "300x300", Ext: "jpg", MimeType: "image/jpeg",
			Width: 300, Height: 300, SizeBytes: 512, ETag: "abc",
		}},
	}}

	client := dial(t, handlers)

	// Act
	response, err := client.GetFile(t.Context(), &filev1.GetFileRequest{FileId: testFileID})

	// Assert
	require.NoError(t, err)

	found := response.GetFile()
	require.NotNil(t, found)

	assert.Equal(t, testFileID, found.GetId())
	assert.Equal(t, "avatar", found.GetNamespace())
	assert.Equal(t, "user-1", found.GetOwnerRef())
	assert.Equal(t, int64(2048), found.GetSizeBytes())
	assert.Equal(t, int32(800), found.GetWidth())
	assert.Equal(t, filev1.FileStatus_FILE_STATUS_READY, found.GetStatus())

	require.Len(t, found.GetVariants(), 1)
	assert.Equal(t, "300x300", found.GetVariants()[0].GetName())
	assert.Equal(t, "abc", found.GetVariants()[0].GetEtag())
}

func TestGetFiles(t *testing.T) {
	t.Parallel()

	// Arrange
	reader := &stubGetFiles{result: usecase.GetFilesResult{
		Files: []usecase.FileWithVariants{{File: testFileView(t)}},
	}}

	handlers := fullHandlers()
	handlers.GetFiles = reader

	client := dial(t, handlers)

	// Act
	response, err := client.GetFiles(t.Context(), &filev1.GetFilesRequest{
		FileIds: []string{testFileID},
	})

	// Assert
	require.NoError(t, err)
	require.Len(t, response.GetFiles(), 1)
	assert.Equal(t, testFileID, response.GetFiles()[0].GetId())
	require.Len(t, reader.cmd.FileIDs, 1)
	assert.Equal(t, testFileID, reader.cmd.FileIDs[0].String())
}

func TestCreateAccessLink(t *testing.T) {
	t.Parallel()

	t.Run("a public namespace has no expiry", func(t *testing.T) {
		t.Parallel()

		// Arrange
		links := &stubCreateLink{result: usecase.CreateAccessLinkResult{
			URL: "https://domain.com/upload/" + testFileID + "/300x300.jpg",
		}}

		handlers := fullHandlers()
		handlers.CreateAccessLink = links

		client := dial(t, handlers)

		// Act
		response, err := client.CreateAccessLink(t.Context(), &filev1.CreateAccessLinkRequest{
			FileId: testFileID, Variant: "300x300", TtlSeconds: 900,
		})

		// Assert
		require.NoError(t, err)
		assert.NotEmpty(t, response.GetUrl())
		assert.Nil(t, response.GetExpiresAt(),
			"an empty expiry tells profile the link never needs refreshing")
		assert.Equal(t, 15*time.Minute, links.cmd.TTL)
	})

	t.Run("a signed namespace carries the expiry", func(t *testing.T) {
		t.Parallel()

		// Arrange
		expiresAt := time.Date(2026, 11, 1, 12, 15, 0, 0, time.UTC)

		handlers := fullHandlers()
		handlers.CreateAccessLink = &stubCreateLink{result: usecase.CreateAccessLinkResult{
			URL: "https://domain.com/upload/x", ExpiresAt: expiresAt,
		}}

		client := dial(t, handlers)

		// Act
		response, err := client.CreateAccessLink(t.Context(), &filev1.CreateAccessLinkRequest{
			FileId: testFileID, Variant: "original",
		})

		// Assert
		require.NoError(t, err)
		assert.Equal(t, expiresAt, response.GetExpiresAt().AsTime())
	})
}

func TestDeleteFile(t *testing.T) {
	t.Parallel()

	// Arrange
	deleter := &stubDeleteFile{result: usecase.DeleteFileResult{
		Status: domain.FileStatusDeleting,
	}}

	handlers := fullHandlers()
	handlers.DeleteFile = deleter

	client := dial(t, handlers)

	// Act
	response, err := client.DeleteFile(t.Context(), &filev1.DeleteFileRequest{
		FileId: testFileID,
	})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, testFileID, deleter.cmd.FileID.String())
	assert.Equal(t, filev1.FileStatus_FILE_STATUS_DELETING, response.GetStatus(),
		"the caller needs to know whether the file went to deleting or was already gone")
}

func TestMapsUseCaseErrorsToCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want codes.Code
	}{
		{name: "invalid command", err: usecase.ErrInvalidCommand, want: codes.InvalidArgument},
		{
			name: "unsupported mime",
			err:  usecase.ErrUnsupportedMimeType,
			want: codes.InvalidArgument,
		},
		{name: "unknown namespace", err: usecase.ErrUnknownNamespace, want: codes.InvalidArgument},
		{name: "no such file", err: domain.ErrFileNotFound, want: codes.NotFound},
		{name: "no such upload", err: domain.ErrUploadNotFound, want: codes.NotFound},
		{name: "size exceeded", err: usecase.ErrSizeExceeded, want: codes.ResourceExhausted},
		{name: "ticket expired", err: usecase.ErrUploadExpired, want: codes.FailedPrecondition},
		{name: "object missing", err: usecase.ErrObjectNotFound, want: codes.FailedPrecondition},
		{name: "not servable", err: usecase.ErrFileNotServable, want: codes.FailedPrecondition},
		{name: "storage down", err: usecase.ErrStorageUnavailable, want: codes.Unavailable},
		{name: "anything else", err: errors.New("boom"), want: codes.Internal},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			handlers := fullHandlers()
			handlers.GetFile = &stubGetFile{err: fmt.Errorf("%w: context", test.err)}

			client := dial(t, handlers)

			// Act
			_, err := client.GetFile(t.Context(), &filev1.GetFileRequest{FileId: testFileID})

			// Assert
			assert.Equal(t, test.want, status.Code(err))
		})
	}
}

func TestNewServerRefusesAMissingHandler(t *testing.T) {
	t.Parallel()

	// Arrange
	handlers := fullHandlers()
	handlers.GetFiles = nil

	// Act
	server, err := NewServer(handlers)

	// Assert
	require.ErrorIs(t, err, ErrIncompleteServer)
	assert.ErrorContains(t, err, "GetFiles")
	assert.Nil(t, server)
}
