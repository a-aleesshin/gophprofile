package usecase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

type stubFileLoaderSaver struct {
	files        []domain.File
	byIDErr      error
	firstSaveErr error
	saveErr      error
	saved        []domain.File
	calls        int
}

func (s *stubFileLoaderSaver) ByID(context.Context, domain.FileID) (domain.File, error) {
	s.calls++

	if s.byIDErr != nil {
		return domain.File{}, s.byIDErr
	}

	next := s.files[0]
	if len(s.files) > 1 {
		s.files = s.files[1:]
	}

	return next, nil
}

func (s *stubFileLoaderSaver) Save(_ context.Context, file *domain.File) error {
	s.saved = append(s.saved, *file)

	if len(s.saved) == 1 {
		return s.firstSaveErr
	}

	return s.saveErr
}

type stubVariantCreator struct {
	created []domain.Variant
	err     error
}

func (s *stubVariantCreator) CreateAll(_ context.Context, variants []domain.Variant) error {
	s.created = append(s.created, variants...)

	return s.err
}

type stubObjectReadWriter struct {
	getErr   error
	putErr   error
	copyErr  error
	put      []domain.Location
	copied   [][2]domain.Location
	deleted  []domain.Location
	observer *stubTransactor
	putInTx  bool
}

func (s *stubObjectReadWriter) Get(context.Context, domain.Location) (io.ReadCloser, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}

	return io.NopCloser(bytes.NewReader([]byte("original bytes"))), nil
}

func (s *stubObjectReadWriter) Put(_ context.Context, params PutObjectParams) error {
	if s.observer != nil && s.observer.inside {
		s.putInTx = true
	}

	s.put = append(s.put, params.Location)

	return s.putErr
}

func (s *stubObjectReadWriter) Copy(_ context.Context, from, to domain.Location) error {
	s.copied = append(s.copied, [2]domain.Location{from, to})

	return s.copyErr
}

func (s *stubObjectReadWriter) Delete(_ context.Context, locations ...domain.Location) error {
	s.deleted = append(s.deleted, locations...)

	return nil
}

type stubProcessor struct {
	content ProcessedContent
	err     error
	params  ProcessParams
	calls   int
}

func (s *stubProcessor) Process(_ context.Context, params ProcessParams) (ProcessedContent, error) {
	s.calls++
	s.params = params

	return s.content, s.err
}

type stubPublisher struct {
	processed []FileProcessedFact
	rejected  []FileRejectedFact
	err       error
}

func (s *stubPublisher) PublishFileProcessed(_ context.Context, fact FileProcessedFact) error {
	s.processed = append(s.processed, fact)

	return s.err
}

func (s *stubPublisher) PublishFileRejected(_ context.Context, fact FileRejectedFact) error {
	s.rejected = append(s.rejected, fact)

	return s.err
}

type processHarness struct {
	handler   *ProcessFileHandler
	files     *stubFileLoaderSaver
	variants  *stubVariantCreator
	objects   *stubObjectReadWriter
	processor *stubProcessor
	publisher *stubPublisher
	tx        *stubTransactor
}

func testReceivedFile(t *testing.T) domain.File {
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

	id, err := domain.NewFileID()
	require.NoError(t, err)

	file, err := domain.NewFile(domain.NewFileParams{
		ID:           id,
		Namespace:    namespace,
		OwnerRef:     owner,
		OriginalName: name,
		MimeType:     mimeType,
		Size:         size,
		Now:          testNow,
	})
	require.NoError(t, err)

	return file
}

func testRenderedVariants(t *testing.T) []RenderedVariant {
	t.Helper()

	webp, err := domain.NewMimeType("image/webp")
	require.NoError(t, err)

	ext, err := domain.NewExtension("webp")
	require.NoError(t, err)

	small, err := domain.NewVariantName("100x100")
	require.NoError(t, err)

	large, err := domain.NewVariantName("300x300")
	require.NoError(t, err)

	smallDims, err := domain.NewDimensions(100, 100)
	require.NoError(t, err)

	largeDims, err := domain.NewDimensions(300, 300)
	require.NoError(t, err)

	size, err := domain.NewSize(4096)
	require.NoError(t, err)

	return []RenderedVariant{
		{Name: small, Ext: ext, MimeType: webp, Dimensions: smallDims, Size: size, Checksum: testUsecaseChecksum(t), Body: []byte("small")},
		{Name: large, Ext: ext, MimeType: webp, Dimensions: largeDims, Size: size, Checksum: testUsecaseChecksum(t), Body: []byte("large")},
	}
}

func testUsecaseChecksum(t *testing.T) domain.Checksum {
	t.Helper()

	c, err := domain.RestoreChecksum("abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789")
	require.NoError(t, err)

	return c
}

func newProcessHarness(t *testing.T, file domain.File) processHarness {
	t.Helper()

	jpg, err := domain.NewExtension("jpg")
	require.NoError(t, err)

	dims, err := domain.NewDimensions(1920, 1080)
	require.NoError(t, err)

	tx := &stubTransactor{}
	objects := &stubObjectReadWriter{observer: tx}
	files := &stubFileLoaderSaver{files: []domain.File{file}}
	variants := &stubVariantCreator{}
	publisher := &stubPublisher{}
	processor := &stubProcessor{content: ProcessedContent{
		Checksum:    testUsecaseChecksum(t),
		Dimensions:  dims,
		OriginalExt: jpg,
		Variants:    testRenderedVariants(t),
	}}

	return processHarness{
		handler: NewProcessFileHandler(
			files, variants, objects, processor, publisher, tx, fixedClock{now: testNow},
		),
		files:     files,
		variants:  variants,
		objects:   objects,
		processor: processor,
		publisher: publisher,
		tx:        tx,
	}
}

func testProcessCommand(t *testing.T, file domain.File) ProcessFileCommand {
	t.Helper()

	id, err := domain.NewUploadID()
	require.NoError(t, err)

	staging, err := domain.NewStagingLocation(id)
	require.NoError(t, err)

	return ProcessFileCommand{FileID: file.ID(), StagingKey: staging}
}

func TestProcessFile_HappyPath(t *testing.T) {
	t.Parallel()

	file := testReceivedFile(t)
	h := newProcessHarness(t, file)
	cmd := testProcessCommand(t, file)

	got, err := h.handler.Handle(context.Background(), cmd)
	require.NoError(t, err)

	assert.True(t, got.Status.Equal(domain.FileStatusReady))

	require.Len(t, h.files.saved, 2)
	assert.True(t, h.files.saved[0].Status().Equal(domain.FileStatusProcessing),
		"processing is marked in its own transaction before any heavy work")
	assert.True(t, h.files.saved[1].Status().Equal(domain.FileStatusReady))
	assert.False(t, h.files.saved[1].Checksum().IsZero())

	require.Len(t, h.variants.created, 3)
	assert.True(t, h.variants.created[2].IsOriginal(),
		"the original is recorded as a variant so the serving path has no special case")
	assert.Equal(t, "originals/"+file.ID().String(), h.variants.created[2].Location().String())

	require.Len(t, h.objects.copied, 1)
	assert.Equal(t, cmd.StagingKey.String(), h.objects.copied[0][0].String())
	assert.Equal(t, file.Location().String(), h.objects.copied[0][1].String())

	assert.Len(t, h.objects.put, 2, "only derived variants are uploaded; the original is copied")
	assert.Contains(t, h.objects.deleted, cmd.StagingKey)

	require.Len(t, h.publisher.processed, 1)
	assert.Len(t, h.publisher.processed[0].Variants, 3)
}

func TestProcessFile_ObjectWorkStaysOutsideTheTransaction(t *testing.T) {
	t.Parallel()

	file := testReceivedFile(t)
	h := newProcessHarness(t, file)

	_, err := h.handler.Handle(context.Background(), testProcessCommand(t, file))
	require.NoError(t, err)

	assert.False(t, h.objects.putInTx,
		"uploading variants inside a database transaction would pin a connection for the whole upload")
	assert.Equal(t, 1, h.tx.calls)
}

func TestProcessFile_PassesDeclaredMimeToTheProcessor(t *testing.T) {
	t.Parallel()

	file := testReceivedFile(t)
	h := newProcessHarness(t, file)

	small, err := domain.NewVariantName("100x100")
	require.NoError(t, err)

	cmd := testProcessCommand(t, file)
	cmd.Variants = []domain.VariantName{small}

	_, err = h.handler.Handle(context.Background(), cmd)
	require.NoError(t, err)

	assert.Equal(t, "image/jpeg", h.processor.params.MimeType.String(),
		"the processor needs the declared type to detect a magic byte mismatch")
	assert.Equal(t, "avatar", h.processor.params.Namespace.String())
	assert.Len(t, h.processor.params.Variants, 1)
}

func TestProcessFile_BusinessRejection(t *testing.T) {
	t.Parallel()

	file := testReceivedFile(t)
	h := newProcessHarness(t, file)
	h.processor.content = ProcessedContent{Reason: domain.RejectReasonDecodeFailed}
	cmd := testProcessCommand(t, file)

	got, err := h.handler.Handle(context.Background(), cmd)
	require.NoError(t, err, "a rejected file is a business outcome, not a delivery failure")

	assert.True(t, got.Status.Equal(domain.FileStatusRejected))
	assert.True(t, got.Reason.Equal(domain.RejectReasonDecodeFailed))

	require.Len(t, h.files.saved, 2)
	assert.True(t, h.files.saved[1].Status().Equal(domain.FileStatusRejected))
	assert.True(t, h.files.saved[1].RejectReason().Equal(domain.RejectReasonDecodeFailed))

	require.Len(t, h.publisher.rejected, 1)
	assert.True(t, h.publisher.rejected[0].Reason.Equal(domain.RejectReasonDecodeFailed))
	assert.Empty(t, h.publisher.processed)
	assert.Empty(t, h.variants.created)
	assert.Contains(t, h.objects.deleted, cmd.StagingKey)
}

func TestProcessFile_MissingStagingObject(t *testing.T) {
	t.Parallel()

	file := testReceivedFile(t)
	h := newProcessHarness(t, file)
	h.objects.getErr = ErrObjectNotFound

	got, err := h.handler.Handle(context.Background(), testProcessCommand(t, file))
	require.NoError(t, err)

	assert.True(t, got.Status.Equal(domain.FileStatusRejected))
	assert.True(t, got.Reason.Equal(domain.RejectReasonInternalError),
		"a vanished staging object is a technical failure, not a content fault")
	assert.Zero(t, h.processor.calls)
}

func TestProcessFile_InfrastructureFailuresAreRetried(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		spoil func(*processHarness, error)
	}{
		{name: "get", spoil: func(h *processHarness, e error) { h.objects.getErr = e }},
		{name: "processor", spoil: func(h *processHarness, e error) { h.processor.err = e }},
		{name: "put", spoil: func(h *processHarness, e error) { h.objects.putErr = e }},
		{name: "copy", spoil: func(h *processHarness, e error) { h.objects.copyErr = e }},
		{name: "variants", spoil: func(h *processHarness, e error) { h.variants.err = e }},
		{name: "publisher", spoil: func(h *processHarness, e error) { h.publisher.err = e }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			boom := errors.New("connection reset")
			file := testReceivedFile(t)
			h := newProcessHarness(t, file)
			tt.spoil(&h, boom)

			_, err := h.handler.Handle(context.Background(), testProcessCommand(t, file))
			require.ErrorIs(t, err, boom,
				"an infrastructure failure must surface so the message is retried")
			assert.False(t, h.files.saved[len(h.files.saved)-1].Status().Equal(domain.FileStatusRejected),
				"a technical failure must not be recorded as a content rejection")
		})
	}
}

func TestProcessFile_IsIdempotent(t *testing.T) {
	t.Parallel()

	for _, status := range []domain.FileStatus{
		domain.FileStatusReady,
		domain.FileStatusRejected,
		domain.FileStatusDeleting,
		domain.FileStatusDeleted,
	} {
		t.Run(status.String(), func(t *testing.T) {
			t.Parallel()

			file := testReceivedFile(t)

			restored, err := domain.RestoreFile(domain.RestoreFileParams{
				ID:        file.ID(),
				Version:   5,
				Namespace: file.Namespace(),
				OwnerRef:  file.OwnerRef(),
				Location:  file.Location(),
				Status:    status,
				CreatedAt: testNow,
				UpdatedAt: testNow,
			})
			require.NoError(t, err)

			h := newProcessHarness(t, restored)

			got, err := h.handler.Handle(context.Background(), testProcessCommand(t, restored))
			require.NoError(t, err)
			assert.True(t, got.Status.Equal(status))
			assert.Empty(t, h.files.saved)
			assert.Zero(t, h.processor.calls)
			assert.Empty(t, h.objects.put)
		})
	}
}

func TestProcessFile_DeletedWhileProcessing(t *testing.T) {
	t.Parallel()

	file := testReceivedFile(t)
	h := newProcessHarness(t, file)
	h.files.saveErr = ErrStaleVersion

	got, err := h.handler.Handle(context.Background(), testProcessCommand(t, file))
	require.NoError(t, err)

	assert.True(t, got.Status.Equal(domain.FileStatusDeleting))
	assert.Empty(t, h.publisher.processed)
	assert.Len(t, h.objects.deleted, 3,
		"variants already written to storage must be cleaned up: a rollback cannot undo an upload")
}

func TestProcessFile_RejectsEmptyCommand(t *testing.T) {
	t.Parallel()

	file := testReceivedFile(t)
	h := newProcessHarness(t, file)

	_, err := h.handler.Handle(context.Background(), ProcessFileCommand{})
	require.ErrorIs(t, err, ErrInvalidCommand)

	_, err = h.handler.Handle(context.Background(), ProcessFileCommand{FileID: file.ID()})
	require.ErrorIs(t, err, ErrInvalidCommand)
	assert.Zero(t, h.files.calls)
}

func TestProcessFile_AnotherWorkerAlreadyStarted(t *testing.T) {
	t.Parallel()

	file := testReceivedFile(t)
	h := newProcessHarness(t, file)
	h.files.firstSaveErr = ErrStaleVersion

	got, err := h.handler.Handle(context.Background(), testProcessCommand(t, file))
	require.NoError(t, err, "losing the marker race is not a delivery failure")

	assert.True(t, got.Status.Equal(domain.FileStatusProcessing))
	assert.Zero(t, h.processor.calls, "the winner is already doing the work")
	assert.Empty(t, h.objects.put)
}

func TestProcessFile_DeletedWhileRejecting(t *testing.T) {
	t.Parallel()

	file := testReceivedFile(t)
	h := newProcessHarness(t, file)
	h.processor.content = ProcessedContent{Reason: domain.RejectReasonTooManyPixels}
	h.files.saveErr = ErrStaleVersion

	got, err := h.handler.Handle(context.Background(), testProcessCommand(t, file))
	require.NoError(t, err)

	assert.True(t, got.Status.Equal(domain.FileStatusDeleting))
	assert.Empty(t, h.publisher.rejected)
	assert.Empty(t, h.objects.deleted,
		"nothing was written to storage, so there is nothing to clean up")
}
