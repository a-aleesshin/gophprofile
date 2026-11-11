package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

type deleteObjectsHarness struct {
	handler   *DeleteObjectsHandler
	files     *stubFileLoaderSaver
	objects   *stubObjectDeleter
	publisher *stubDeletedPublisher
	tx        *stubTransactor
}

func testDeletingFile(t *testing.T, status domain.FileStatus) domain.File {
	t.Helper()

	id, err := domain.NewFileID()
	require.NoError(t, err)

	namespace, err := domain.NewNamespace("avatar")
	require.NoError(t, err)

	owner, err := domain.NewOwnerRef("user-123")
	require.NoError(t, err)

	location, err := domain.NewOriginalLocation(id)
	require.NoError(t, err)

	deletedAt := testNow

	file, err := domain.RestoreFile(domain.RestoreFileParams{
		ID:        id,
		Version:   6,
		Namespace: namespace,
		OwnerRef:  owner,
		Location:  location,
		Status:    status,
		CreatedAt: testNow,
		UpdatedAt: testNow,
		DeletedAt: &deletedAt,
	})
	require.NoError(t, err)

	return file
}

func newDeleteObjectsHarness(t *testing.T, files []domain.File) deleteObjectsHarness {
	t.Helper()

	tx := &stubTransactor{}
	repo := &stubFileLoaderSaver{files: files}
	objects := &stubObjectDeleter{observer: tx}
	publisher := &stubDeletedPublisher{}

	return deleteObjectsHarness{
		handler:   NewDeleteObjectsHandler(repo, objects, publisher, tx, fixedClock{now: testNow}),
		files:     repo,
		objects:   objects,
		publisher: publisher,
		tx:        tx,
	}
}

func testDeleteObjectsCommand(t *testing.T, file domain.File) DeleteObjectsCommand {
	t.Helper()

	return DeleteObjectsCommand{
		FileID:    file.ID(),
		Locations: []domain.Location{file.Location()},
	}
}

func TestDeleteObjects_RemovesObjectsThenCommits(t *testing.T) {
	t.Parallel()

	file := testDeletingFile(t, domain.FileStatusDeleting)
	h := newDeleteObjectsHarness(t, []domain.File{file})

	got, err := h.handler.Handle(context.Background(), testDeleteObjectsCommand(t, file))
	require.NoError(t, err)

	assert.True(t, got.Status.Equal(domain.FileStatusDeleted))
	assert.Len(t, h.objects.deleted, 1)
	assert.False(t, h.objects.inTx,
		"object storage is not part of the database transaction")

	require.Len(t, h.files.saved, 1)
	assert.True(t, h.files.saved[0].Status().Equal(domain.FileStatusDeleted))

	require.Len(t, h.publisher.facts, 1)
	assert.True(t, h.publisher.facts[0].FileID.Equal(file.ID()))
}

func TestDeleteObjects_IsIdempotent(t *testing.T) {
	t.Parallel()

	file := testDeletingFile(t, domain.FileStatusDeleted)
	h := newDeleteObjectsHarness(t, []domain.File{file})

	got, err := h.handler.Handle(context.Background(), testDeleteObjectsCommand(t, file))
	require.NoError(t, err)

	assert.True(t, got.Status.Equal(domain.FileStatusDeleted))
	assert.Empty(t, h.objects.deleted)
	assert.Empty(t, h.files.saved)
	assert.Empty(t, h.publisher.facts)
}

func TestDeleteObjects_StorageFailureIsRetried(t *testing.T) {
	t.Parallel()

	boom := errors.New("service unavailable")
	file := testDeletingFile(t, domain.FileStatusDeleting)
	h := newDeleteObjectsHarness(t, []domain.File{file})
	h.objects.err = boom

	_, err := h.handler.Handle(context.Background(), testDeleteObjectsCommand(t, file))
	require.ErrorIs(t, err, boom)
	assert.Empty(t, h.files.saved,
		"the file stays deleting so a retry removes the objects again")
	assert.Zero(t, h.tx.calls)
}

func TestDeleteObjects_CommitFailureLeavesStatusDeleting(t *testing.T) {
	t.Parallel()

	boom := errors.New("deadlock detected")
	file := testDeletingFile(t, domain.FileStatusDeleting)
	h := newDeleteObjectsHarness(t, []domain.File{file})
	h.tx.err = boom

	_, err := h.handler.Handle(context.Background(), testDeleteObjectsCommand(t, file))
	require.ErrorIs(t, err, boom)
	assert.Len(t, h.objects.deleted, 1,
		"deleting an absent object succeeds, so a retry converges")
}

func TestDeleteObjects_UnexpectedStatusGoesToTheDeadLetterQueue(t *testing.T) {
	t.Parallel()

	file := testReadyFile(t, mustFileID(t))
	h := newDeleteObjectsHarness(t, []domain.File{file})

	_, err := h.handler.Handle(context.Background(), testDeleteObjectsCommand(t, file))
	require.ErrorIs(t, err, domain.ErrForbiddenFileTransition,
		"ready -> deleted is impossible, and a state this wrong should be looked at by a human")
}

func TestDeleteObjects_RejectsEmptyCommand(t *testing.T) {
	t.Parallel()

	file := testDeletingFile(t, domain.FileStatusDeleting)
	h := newDeleteObjectsHarness(t, []domain.File{file})

	_, err := h.handler.Handle(context.Background(), DeleteObjectsCommand{})
	require.ErrorIs(t, err, ErrInvalidCommand)

	_, err = h.handler.Handle(context.Background(), DeleteObjectsCommand{FileID: file.ID()})
	require.ErrorIs(t, err, ErrInvalidCommand)
	assert.Zero(t, h.files.calls)
}

func mustFileID(t *testing.T) domain.FileID {
	t.Helper()

	id, err := domain.NewFileID()
	require.NoError(t, err)

	return id
}
