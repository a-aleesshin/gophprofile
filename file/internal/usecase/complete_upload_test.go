package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

type stubUploadRepository struct {
	uploads []domain.Upload
	byIDErr error
	saveErr error
	saved   []domain.Upload
	calls   int
}

func (s *stubUploadRepository) ByID(context.Context, domain.UploadID) (domain.Upload, error) {
	s.calls++

	if s.byIDErr != nil {
		return domain.Upload{}, s.byIDErr
	}

	next := s.uploads[0]
	if len(s.uploads) > 1 {
		s.uploads = s.uploads[1:]
	}

	return next, nil
}

func (s *stubUploadRepository) Save(_ context.Context, upload *domain.Upload) error {
	s.saved = append(s.saved, *upload)

	return s.saveErr
}

type stubFileRepository struct {
	file       domain.File
	byIDErr    error
	createErr  error
	created    []domain.File
	byIDCalled int
}

func (s *stubFileRepository) ByID(context.Context, domain.FileID) (domain.File, error) {
	s.byIDCalled++

	return s.file, s.byIDErr
}

func (s *stubFileRepository) Create(_ context.Context, file *domain.File) error {
	s.created = append(s.created, *file)

	return s.createErr
}

type stubObjectHeader struct {
	info     ObjectInfo
	err      error
	calls    int
	sawInTx  bool
	observer *stubTransactor
}

func (s *stubObjectHeader) Head(context.Context, domain.Location) (ObjectInfo, error) {
	s.calls++

	if s.observer != nil && s.observer.inside {
		s.sawInTx = true
	}

	return s.info, s.err
}

type stubEnqueuer struct {
	commands []ProcessFileCommand
	err      error
}

func (s *stubEnqueuer) EnqueueProcessFile(_ context.Context, cmd ProcessFileCommand) error {
	s.commands = append(s.commands, cmd)

	return s.err
}

type stubTransactor struct {
	inside bool
	calls  int
	err    error
}

func (s *stubTransactor) InTx(ctx context.Context, fn func(context.Context) error) error {
	s.calls++
	s.inside = true

	defer func() { s.inside = false }()

	if s.err != nil {
		return s.err
	}

	return fn(ctx)
}

type completeHarness struct {
	handler  *CompleteUploadHandler
	uploads  *stubUploadRepository
	files    *stubFileRepository
	objects  *stubObjectHeader
	enqueuer *stubEnqueuer
	tx       *stubTransactor
}

func testCompletePolicy(t *testing.T) CompletePolicy {
	t.Helper()

	jpeg, err := domain.NewMimeType("image/jpeg")
	require.NoError(t, err)

	small, err := domain.NewVariantName("100x100")
	require.NoError(t, err)

	large, err := domain.NewVariantName("300x300")
	require.NoError(t, err)

	allowed, err := domain.NewMimeSet(jpeg)
	require.NoError(t, err)

	return CompletePolicy{
		AllowedMime: allowed,
		Variants:    []domain.VariantName{small, large},
	}
}

func newCompleteHarness(t *testing.T, uploads ...domain.Upload) completeHarness {
	t.Helper()

	tx := &stubTransactor{}
	objects := &stubObjectHeader{
		info:     ObjectInfo{SizeBytes: 2048, ContentType: "image/jpeg"},
		observer: tx,
	}
	uploadRepo := &stubUploadRepository{uploads: uploads}
	fileRepo := &stubFileRepository{}
	enqueuer := &stubEnqueuer{}

	return completeHarness{
		handler: NewCompleteUploadHandler(
			stubCompletePolicies{policy: testCompletePolicy(t)},
			uploadRepo,
			fileRepo,
			objects,
			enqueuer,
			tx,
			fixedClock{now: testNow},
		),
		uploads:  uploadRepo,
		files:    fileRepo,
		objects:  objects,
		enqueuer: enqueuer,
		tx:       tx,
	}
}

type stubCompletePolicies struct {
	policy CompletePolicy
	err    error
}

func (s stubCompletePolicies) CompletePolicy(domain.Namespace) (CompletePolicy, error) {
	return s.policy, s.err
}

func testCompleteCommand(t *testing.T, upload domain.Upload) CompleteUploadCommand {
	t.Helper()

	return CompleteUploadCommand{UploadID: upload.ID()}
}

func TestCompleteUpload_CreatesFileAndEnqueuesProcessing(t *testing.T) {
	t.Parallel()

	upload := testPendingUpload(t, testNow)
	h := newCompleteHarness(t, upload)

	got, err := h.handler.Handle(context.Background(), testCompleteCommand(t, upload))
	require.NoError(t, err)

	require.Len(t, h.files.created, 1)
	file := h.files.created[0]

	assert.True(t, file.Status().Equal(domain.FileStatusReceived))
	assert.Equal(t, upload.Namespace().String(), file.Namespace().String())
	assert.Equal(t, upload.OwnerRef().String(), file.OwnerRef().String())
	assert.Equal(t, upload.OriginalName().String(), file.OriginalName().String())
	assert.Equal(t, int64(2048), file.Size().Bytes(), "the actual size wins over the declared one")
	assert.Equal(t, "image/jpeg", file.MimeType().String())
	assert.True(t, file.Checksum().IsZero(), "the checksum is the worker's job")

	require.Len(t, h.uploads.saved, 1)
	assert.True(t, h.uploads.saved[0].Status().Equal(domain.UploadStatusUploaded))
	assert.True(t, h.uploads.saved[0].FileID().Equal(file.ID()))

	require.Len(t, h.enqueuer.commands, 1)
	assert.True(t, h.enqueuer.commands[0].FileID.Equal(file.ID()))
	assert.Equal(t, upload.Location().String(), h.enqueuer.commands[0].StagingKey.String())
	assert.Len(t, h.enqueuer.commands[0].Variants, 2)

	assert.True(t, got.FileID.Equal(file.ID()))
	assert.True(t, got.Status.Equal(domain.FileStatusReceived))
}

func TestCompleteUpload_HeadHappensOutsideTheTransaction(t *testing.T) {
	t.Parallel()

	upload := testPendingUpload(t, testNow)
	h := newCompleteHarness(t, upload)

	_, err := h.handler.Handle(context.Background(), testCompleteCommand(t, upload))
	require.NoError(t, err)

	assert.Equal(t, 1, h.objects.calls)
	assert.False(t, h.objects.sawInTx,
		"holding a database transaction across a round trip to object storage pins a connection")
	assert.Equal(t, 1, h.tx.calls)
}

func TestCompleteUpload_IsIdempotent(t *testing.T) {
	t.Parallel()

	upload := testPendingUpload(t, testNow)
	fileID, err := domain.NewFileID()
	require.NoError(t, err)
	require.NoError(t, upload.Complete(fileID, testNow))

	h := newCompleteHarness(t, upload)

	ready := testReadyFile(t, fileID)
	h.files.file = ready

	got, err := h.handler.Handle(context.Background(), testCompleteCommand(t, upload))
	require.NoError(t, err)

	assert.True(t, got.FileID.Equal(fileID))
	assert.True(t, got.Status.Equal(domain.FileStatusReady),
		"a repeated call reports the current state, not a freshly received file")
	assert.Zero(t, h.objects.calls)
	assert.Zero(t, h.tx.calls)
	assert.Empty(t, h.files.created)
}

func TestCompleteUpload_TerminalTicket(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		spoil func(*testing.T, *domain.Upload)
	}{
		{
			name:  "expired",
			spoil: func(t *testing.T, u *domain.Upload) { require.NoError(t, u.Expire(testNow)) },
		},
		{
			name:  "aborted",
			spoil: func(t *testing.T, u *domain.Upload) { require.NoError(t, u.Abort(testNow)) },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			upload := testPendingUpload(t, testNow)
			tt.spoil(t, &upload)

			h := newCompleteHarness(t, upload)

			_, err := h.handler.Handle(context.Background(), testCompleteCommand(t, upload))
			require.ErrorIs(t, err, ErrUploadNotReusable)
			assert.Zero(t, h.objects.calls)
			assert.Empty(t, h.files.created)
		})
	}
}

func TestCompleteUpload_MissingObject(t *testing.T) {
	t.Parallel()

	t.Run("called before the client uploaded", func(t *testing.T) {
		t.Parallel()

		upload := testPendingUpload(t, testNow)
		h := newCompleteHarness(t, upload)
		h.objects.err = ErrObjectNotFound

		_, err := h.handler.Handle(context.Background(), testCompleteCommand(t, upload))
		require.ErrorIs(t, err, ErrObjectNotFound)
		assert.Empty(t, h.files.created)
		assert.Zero(t, h.tx.calls)
	})

	t.Run("ticket already expired", func(t *testing.T) {
		t.Parallel()

		upload := testPendingUpload(t, testNow.Add(-time.Hour))
		h := newCompleteHarness(t, upload)
		h.objects.err = ErrObjectNotFound

		_, err := h.handler.Handle(context.Background(), testCompleteCommand(t, upload))
		require.ErrorIs(t, err, ErrUploadExpired)
	})
}

func TestCompleteUpload_RejectsObjectBeyondTheTicket(t *testing.T) {
	t.Parallel()

	t.Run("larger than the ticket limit", func(t *testing.T) {
		t.Parallel()

		upload := testPendingUpload(t, testNow)
		h := newCompleteHarness(t, upload)
		h.objects.info.SizeBytes = upload.MaxSize().Bytes() + 1

		_, err := h.handler.Handle(context.Background(), testCompleteCommand(t, upload))
		require.ErrorIs(t, err, ErrSizeExceeded)
		assert.Empty(t, h.files.created)
		assert.Zero(t, h.tx.calls)
	})

	t.Run("content type outside the allowlist", func(t *testing.T) {
		t.Parallel()

		upload := testPendingUpload(t, testNow)
		h := newCompleteHarness(t, upload)
		h.objects.info.ContentType = "image/gif"

		_, err := h.handler.Handle(context.Background(), testCompleteCommand(t, upload))
		require.ErrorIs(t, err, ErrUnsupportedMimeType)
		assert.Empty(t, h.files.created)
	})
}

func TestCompleteUpload_LostVersionRace(t *testing.T) {
	t.Parallel()

	pending := testPendingUpload(t, testNow)

	winner := testPendingUpload(t, testNow)
	winnerFileID, err := domain.NewFileID()
	require.NoError(t, err)
	require.NoError(t, winner.Complete(winnerFileID, testNow))

	h := newCompleteHarness(t, pending, winner)
	h.tx.err = ErrStaleVersion
	h.files.file = testReadyFile(t, winnerFileID)

	got, err := h.handler.Handle(context.Background(), testCompleteCommand(t, pending))
	require.NoError(t, err)

	assert.True(t, got.FileID.Equal(winnerFileID),
		"the loser returns the winner's file instead of failing")
	assert.True(t, got.Status.Equal(domain.FileStatusReady))
	assert.Equal(t, 2, h.uploads.calls)
}

func TestCompleteUpload_PropagatesErrors(t *testing.T) {
	t.Parallel()

	t.Run("upload not found", func(t *testing.T) {
		t.Parallel()

		h := newCompleteHarness(t, testPendingUpload(t, testNow))
		h.uploads.byIDErr = domain.ErrUploadNotFound

		id, err := domain.NewUploadID()
		require.NoError(t, err)

		_, err = h.handler.Handle(context.Background(), CompleteUploadCommand{UploadID: id})
		require.ErrorIs(t, err, domain.ErrUploadNotFound)
	})

	t.Run("zero upload id", func(t *testing.T) {
		t.Parallel()

		h := newCompleteHarness(t, testPendingUpload(t, testNow))

		_, err := h.handler.Handle(context.Background(), CompleteUploadCommand{})
		require.ErrorIs(t, err, ErrInvalidCommand)
		assert.Zero(t, h.uploads.calls)
	})

	t.Run("transaction fails", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("deadlock detected")
		upload := testPendingUpload(t, testNow)
		h := newCompleteHarness(t, upload)
		h.tx.err = boom

		_, err := h.handler.Handle(context.Background(), testCompleteCommand(t, upload))
		require.ErrorIs(t, err, boom)
	})

	t.Run("enqueue fails inside the transaction", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("outbox unavailable")
		upload := testPendingUpload(t, testNow)
		h := newCompleteHarness(t, upload)
		h.enqueuer.err = boom

		_, err := h.handler.Handle(context.Background(), testCompleteCommand(t, upload))
		require.ErrorIs(t, err, boom)
	})

	t.Run("head fails", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("connection reset")
		upload := testPendingUpload(t, testNow)
		h := newCompleteHarness(t, upload)
		h.objects.err = boom

		_, err := h.handler.Handle(context.Background(), testCompleteCommand(t, upload))
		require.ErrorIs(t, err, boom)
		assert.Zero(t, h.tx.calls)
	})
}

func testReadyFile(t *testing.T, id domain.FileID) domain.File {
	t.Helper()

	namespace, err := domain.NewNamespace("avatar")
	require.NoError(t, err)

	owner, err := domain.NewOwnerRef("user-123")
	require.NoError(t, err)

	name, err := domain.NewOriginalName("avatar.jpg")
	require.NoError(t, err)

	mimeType, err := domain.NewMimeType("image/jpeg")
	require.NoError(t, err)

	size, err := domain.NewSize(2048)
	require.NoError(t, err)

	location, err := domain.NewOriginalLocation(id)
	require.NoError(t, err)

	file, err := domain.RestoreFile(domain.RestoreFileParams{
		ID:           id,
		Version:      3,
		Namespace:    namespace,
		OwnerRef:     owner,
		OriginalName: name,
		MimeType:     mimeType,
		Size:         size,
		Location:     location,
		Status:       domain.FileStatusReady,
		CreatedAt:    testNow,
		UpdatedAt:    testNow,
	})
	require.NoError(t, err)

	return file
}

func TestCompleteUpload_UnusableObjectMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		info ObjectInfo
	}{
		{name: "zero size", info: ObjectInfo{SizeBytes: 0, ContentType: "image/jpeg"}},
		{name: "no content type", info: ObjectInfo{SizeBytes: 2048, ContentType: ""}},
		{name: "garbage content type", info: ObjectInfo{SizeBytes: 2048, ContentType: "image"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			upload := testPendingUpload(t, testNow)
			h := newCompleteHarness(t, upload)
			h.objects.info = tt.info

			_, err := h.handler.Handle(context.Background(), testCompleteCommand(t, upload))
			require.ErrorIs(t, err, ErrObjectUnusable,
				"the caller's argument is fine; what sits in staging is not")
			assert.Empty(t, h.files.created)
			assert.Zero(t, h.tx.calls)
		})
	}
}

func TestCompleteUpload_UnknownNamespace(t *testing.T) {
	t.Parallel()

	upload := testPendingUpload(t, testNow)
	tx := &stubTransactor{}
	uploads := &stubUploadRepository{uploads: []domain.Upload{upload}}
	files := &stubFileRepository{}
	objects := &stubObjectHeader{observer: tx}

	handler := NewCompleteUploadHandler(
		stubCompletePolicies{err: errors.New("no such namespace")},
		uploads, files, objects, &stubEnqueuer{}, tx, fixedClock{now: testNow},
	)

	_, err := handler.Handle(context.Background(), testCompleteCommand(t, upload))
	require.ErrorIs(t, err, ErrUnknownNamespace)
	assert.Zero(t, objects.calls)
}

func TestCompleteUpload_FailuresInsideTheTransaction(t *testing.T) {
	t.Parallel()

	t.Run("file insert", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("unique violation")
		upload := testPendingUpload(t, testNow)
		h := newCompleteHarness(t, upload)
		h.files.createErr = boom

		_, err := h.handler.Handle(context.Background(), testCompleteCommand(t, upload))
		require.ErrorIs(t, err, boom)
		assert.Empty(t, h.uploads.saved, "the ticket is not marked uploaded if the file insert fails")
	})

	t.Run("stale version is retried as an idempotent result", func(t *testing.T) {
		t.Parallel()

		pending := testPendingUpload(t, testNow)

		winner := testPendingUpload(t, testNow)
		winnerFileID, err := domain.NewFileID()
		require.NoError(t, err)
		require.NoError(t, winner.Complete(winnerFileID, testNow))

		h := newCompleteHarness(t, pending, winner)
		h.uploads.saveErr = ErrStaleVersion
		h.files.file = testReadyFile(t, winnerFileID)

		got, err := h.handler.Handle(context.Background(), testCompleteCommand(t, pending))
		require.NoError(t, err)
		assert.True(t, got.FileID.Equal(winnerFileID))
		assert.Equal(t, 2, h.uploads.calls)
	})

	t.Run("stale version with a still pending winner gives up", func(t *testing.T) {
		t.Parallel()

		pending := testPendingUpload(t, testNow)
		other := testPendingUpload(t, testNow)

		h := newCompleteHarness(t, pending, other)
		h.uploads.saveErr = ErrStaleVersion

		_, err := h.handler.Handle(context.Background(), testCompleteCommand(t, pending))
		require.ErrorIs(t, err, ErrStaleVersion)
	})
}

func TestCompleteUpload_IdempotentPathPropagatesFileLookupErrors(t *testing.T) {
	t.Parallel()

	upload := testPendingUpload(t, testNow)
	fileID, err := domain.NewFileID()
	require.NoError(t, err)
	require.NoError(t, upload.Complete(fileID, testNow))

	boom := errors.New("no rows")
	h := newCompleteHarness(t, upload)
	h.files.byIDErr = boom

	_, err = h.handler.Handle(context.Background(), testCompleteCommand(t, upload))
	require.ErrorIs(t, err, boom)
}
