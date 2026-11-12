package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

type stubVariantLister struct {
	locations []domain.Location
	err       error
}

func (s *stubVariantLister) LocationsByFileID(
	context.Context, domain.FileID,
) ([]domain.Location, error) {
	if s.err != nil {
		return nil, s.err
	}

	return append([]domain.Location(nil), s.locations...), nil
}

type stubDeleteEnqueuer struct {
	commands []DeleteObjectsCommand
	err      error
}

func (s *stubDeleteEnqueuer) EnqueueDeleteObjects(_ context.Context, cmd DeleteObjectsCommand) error {
	s.commands = append(s.commands, cmd)

	return s.err
}

type stubDeletedPublisher struct {
	facts []FileDeletedFact
	err   error
}

func (s *stubDeletedPublisher) PublishFileDeleted(_ context.Context, fact FileDeletedFact) error {
	s.facts = append(s.facts, fact)

	return s.err
}

type stubObjectDeleter struct {
	deleted  []domain.Location
	err      error
	observer *stubTransactor
	inTx     bool
}

func (s *stubObjectDeleter) Delete(_ context.Context, locations ...domain.Location) error {
	if s.observer != nil && s.observer.inside {
		s.inTx = true
	}

	s.deleted = append(s.deleted, locations...)

	return s.err
}

func testReadyFileWithVariants(t *testing.T) (domain.File, []domain.Location) {
	t.Helper()

	id, err := domain.NewFileID()
	require.NoError(t, err)

	file := testReadyFile(t, id)

	ext, err := domain.NewExtension("webp")
	require.NoError(t, err)

	locations := make([]domain.Location, 0, 2)

	for _, name := range []string{"100x100", "300x300"} {
		variantName, nameErr := domain.NewVariantName(name)
		require.NoError(t, nameErr)

		location, locErr := domain.NewVariantLocation(id, variantName, ext)
		require.NoError(t, locErr)

		locations = append(locations, location)
	}

	return file, locations
}

type deleteFileHarness struct {
	handler  *DeleteFileHandler
	files    *stubFileLoaderSaver
	variants *stubVariantLister
	enqueuer *stubDeleteEnqueuer
	tx       *stubTransactor
}

func newDeleteFileHarness(t *testing.T, files []domain.File, locations []domain.Location) deleteFileHarness {
	t.Helper()

	tx := &stubTransactor{}
	repo := &stubFileLoaderSaver{files: files}
	lister := &stubVariantLister{locations: locations}
	enqueuer := &stubDeleteEnqueuer{}

	return deleteFileHarness{
		handler:  NewDeleteFileHandler(repo, lister, enqueuer, tx, fixedClock{now: testNow}),
		files:    repo,
		variants: lister,
		enqueuer: enqueuer,
		tx:       tx,
	}
}

func TestDeleteFile_MarksDeletingAndEnqueuesCleanup(t *testing.T) {
	t.Parallel()

	file, locations := testReadyFileWithVariants(t)
	h := newDeleteFileHarness(t, []domain.File{file}, locations)

	got, err := h.handler.Handle(context.Background(), DeleteFileCommand{FileID: file.ID()})
	require.NoError(t, err)

	assert.True(t, got.Status.Equal(domain.FileStatusDeleting))

	require.Len(t, h.files.saved, 1)
	assert.True(t, h.files.saved[0].Status().Equal(domain.FileStatusDeleting))
	assert.True(t, h.files.saved[0].IsDeleted())

	require.Len(t, h.enqueuer.commands, 1)
	assert.True(t, h.enqueuer.commands[0].FileID.Equal(file.ID()))
	require.Len(t, h.enqueuer.commands[0].Locations, 3,
		"two variants plus the original are snapshotted into the command")
	assert.Equal(t, file.Location().String(),
		h.enqueuer.commands[0].Locations[2].String())
}

func TestDeleteFile_WorksWithoutVariants(t *testing.T) {
	t.Parallel()

	file := testReceivedFile(t)
	h := newDeleteFileHarness(t, []domain.File{file}, nil)

	got, err := h.handler.Handle(context.Background(), DeleteFileCommand{FileID: file.ID()})
	require.NoError(t, err)

	assert.True(t, got.Status.Equal(domain.FileStatusDeleting))
	require.Len(t, h.enqueuer.commands, 1)
	assert.Len(t, h.enqueuer.commands[0].Locations, 1,
		"a file deleted before processing still has its originals key enqueued")
}

func TestDeleteFile_IsIdempotent(t *testing.T) {
	t.Parallel()

	for _, status := range []domain.FileStatus{
		domain.FileStatusDeleting,
		domain.FileStatusDeleted,
	} {
		t.Run(status.String(), func(t *testing.T) {
			t.Parallel()

			id, err := domain.NewFileID()
			require.NoError(t, err)

			deletedAt := testNow
			location, err := domain.NewOriginalLocation(id)
			require.NoError(t, err)

			namespace, err := domain.NewNamespace("avatar")
			require.NoError(t, err)

			owner, err := domain.NewOwnerRef("user-123")
			require.NoError(t, err)

			file, err := domain.RestoreFile(domain.RestoreFileParams{
				ID:        id,
				Version:   4,
				Namespace: namespace,
				OwnerRef:  owner,
				Location:  location,
				Status:    status,
				CreatedAt: testNow,
				UpdatedAt: testNow,
				DeletedAt: &deletedAt,
			})
			require.NoError(t, err)

			h := newDeleteFileHarness(t, []domain.File{file}, nil)

			got, err := h.handler.Handle(context.Background(), DeleteFileCommand{FileID: id})
			require.NoError(t, err)
			assert.True(t, got.Status.Equal(status))
			assert.Empty(t, h.files.saved)
			assert.Empty(t, h.enqueuer.commands,
				"a stuck deletion is the reaper's job, not a second command")
		})
	}
}

func TestDeleteFile_Errors(t *testing.T) {
	t.Parallel()

	t.Run("zero file id", func(t *testing.T) {
		t.Parallel()

		h := newDeleteFileHarness(t, nil, nil)

		_, err := h.handler.Handle(context.Background(), DeleteFileCommand{})
		require.ErrorIs(t, err, ErrInvalidCommand)
		assert.Zero(t, h.files.calls)
	})

	t.Run("variant lookup fails", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("connection reset")
		file, _ := testReadyFileWithVariants(t)
		h := newDeleteFileHarness(t, []domain.File{file}, nil)
		h.variants.err = boom

		_, err := h.handler.Handle(context.Background(), DeleteFileCommand{FileID: file.ID()})
		require.ErrorIs(t, err, boom)
		assert.Empty(t, h.files.saved)
	})

	t.Run("enqueue fails inside the transaction", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("outbox unavailable")
		file, locations := testReadyFileWithVariants(t)
		h := newDeleteFileHarness(t, []domain.File{file}, locations)
		h.enqueuer.err = boom

		_, err := h.handler.Handle(context.Background(), DeleteFileCommand{FileID: file.ID()})
		require.ErrorIs(t, err, boom)
	})

	t.Run("lost the version race", func(t *testing.T) {
		t.Parallel()

		file, locations := testReadyFileWithVariants(t)

		deletedAt := testNow
		winner, err := domain.RestoreFile(domain.RestoreFileParams{
			ID:        file.ID(),
			Version:   9,
			Namespace: file.Namespace(),
			OwnerRef:  file.OwnerRef(),
			Location:  file.Location(),
			Status:    domain.FileStatusDeleting,
			CreatedAt: testNow,
			UpdatedAt: testNow,
			DeletedAt: &deletedAt,
		})
		require.NoError(t, err)

		h := newDeleteFileHarness(t, []domain.File{file, winner}, locations)
		h.files.firstSaveErr = ErrStaleVersion

		got, err := h.handler.Handle(context.Background(), DeleteFileCommand{FileID: file.ID()})
		require.NoError(t, err)
		assert.True(t, got.Status.Equal(domain.FileStatusDeleting))
		assert.Equal(t, 2, h.files.calls)
	})
}

func TestDeleteFile_RefusesAFileOfAnotherOwner(t *testing.T) {
	t.Parallel()

	// Arrange
	file, locations := testReadyFileWithVariants(t)
	h := newDeleteFileHarness(t, []domain.File{file}, locations)

	stranger, err := domain.NewOwnerRef("somebody-else")
	require.NoError(t, err)

	// Act
	_, err = h.handler.Handle(context.Background(), DeleteFileCommand{
		FileID: file.ID(), OwnerRef: stranger,
	})

	// Assert
	require.ErrorIs(t, err, ErrOwnerMismatch)
	assert.Empty(t, h.files.saved, "nothing is written")
	assert.Empty(t, h.enqueuer.commands, "and no objects are queued for deletion")
}

func TestDeleteFile_AcceptsTheOwnerItWasGiven(t *testing.T) {
	t.Parallel()

	// Arrange
	file, locations := testReadyFileWithVariants(t)
	h := newDeleteFileHarness(t, []domain.File{file}, locations)

	// Act
	got, err := h.handler.Handle(context.Background(), DeleteFileCommand{
		FileID: file.ID(), OwnerRef: file.OwnerRef(),
	})

	// Assert
	require.NoError(t, err)
	assert.True(t, got.Status.Equal(domain.FileStatusDeleting))
	require.Len(t, h.enqueuer.commands, 1)
}

func TestDeleteFile_SkipsTheOwnerCheckWhenNoneIsGiven(t *testing.T) {
	t.Parallel()

	// Arrange
	file, locations := testReadyFileWithVariants(t)
	h := newDeleteFileHarness(t, []domain.File{file}, locations)

	// Act
	got, err := h.handler.Handle(context.Background(), DeleteFileCommand{FileID: file.ID()})

	// Assert
	require.NoError(t, err)
	assert.True(t, got.Status.Equal(domain.FileStatusDeleting),
		"the grpc caller has checked ownership already and passes no owner")
}
