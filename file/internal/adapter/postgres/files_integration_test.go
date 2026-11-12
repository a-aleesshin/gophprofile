//go:build integration

package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
)

const staleWindow = 50 * time.Hour

func testFile(t *testing.T) domain.File {
	t.Helper()

	id, err := domain.NewFileID()
	require.NoError(t, err)

	namespace, err := domain.NewNamespace("avatar")
	require.NoError(t, err)

	owner, err := domain.NewOwnerRef("user-" + uuid.NewString())
	require.NoError(t, err)

	name, err := domain.NewOriginalName("avatar.jpg")
	require.NoError(t, err)

	mime, err := domain.NewMimeType("image/jpeg")
	require.NoError(t, err)

	size, err := domain.NewSize(2048)
	require.NoError(t, err)

	file, err := domain.NewFile(domain.NewFileParams{
		ID:           id,
		Namespace:    namespace,
		OwnerRef:     owner,
		OriginalName: name,
		MimeType:     mime,
		Size:         size,
		Now:          time.Now(),
	})
	require.NoError(t, err)

	return file
}

func testChecksum(t *testing.T) domain.Checksum {
	t.Helper()

	sum, err := domain.RestoreChecksum(
		"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789")
	require.NoError(t, err)

	return sum
}

func readyFileWithVariant(t *testing.T) (domain.File, domain.Variant) {
	t.Helper()

	ctx := t.Context()
	files, variants := NewFiles(sharedDB), NewVariants(sharedDB)

	file := testFile(t)
	require.NoError(t, files.Create(ctx, &file))
	require.NoError(t, file.StartProcessing(time.Now()))
	require.NoError(t, files.Save(ctx, &file))

	dimensions, err := domain.NewDimensions(300, 300)
	require.NoError(t, err)

	require.NoError(t, file.MarkReady(testChecksum(t), dimensions, time.Now()))
	require.NoError(t, files.Save(ctx, &file))

	name, err := domain.NewVariantName("300x300")
	require.NoError(t, err)

	ext, err := domain.NewExtension("webp")
	require.NoError(t, err)

	webp, err := domain.NewMimeType("image/webp")
	require.NoError(t, err)

	size, err := domain.NewSize(4096)
	require.NoError(t, err)

	variant, err := domain.NewVariant(domain.NewVariantParams{
		FileID:     file.ID(),
		Name:       name,
		Ext:        ext,
		MimeType:   webp,
		Dimensions: dimensions,
		Size:       size,
		Checksum:   testChecksum(t),
		Now:        time.Now(),
	})
	require.NoError(t, err)
	require.NoError(t, variants.CreateAll(ctx, []domain.Variant{variant}))

	return file, variant
}

func TestFiles_RoundTrip(t *testing.T) {
	t.Parallel()

	repo := NewFiles(sharedDB)
	ctx := t.Context()

	want := testFile(t)
	require.NoError(t, repo.Create(ctx, &want))

	got, err := repo.ByID(ctx, want.ID())
	require.NoError(t, err)

	assert.True(t, got.ID().Equal(want.ID()))
	assert.Equal(t, want.OwnerRef().String(), got.OwnerRef().String())
	assert.Equal(t, want.MimeType().String(), got.MimeType().String())
	assert.Equal(t, want.Size().Bytes(), got.Size().Bytes())
	assert.Equal(t, want.Location().String(), got.Location().String())
	assert.True(t, got.Status().Equal(domain.FileStatusReceived))
	assert.True(t, got.Checksum().IsZero(), "a null checksum round trips as the zero value")
	assert.True(t, got.Dimensions().IsZero())
	assert.True(t, got.RejectReason().IsZero())
	assert.False(t, got.IsDeleted())
}

func TestFiles_SaveWritesProcessingResults(t *testing.T) {
	t.Parallel()

	repo := NewFiles(sharedDB)
	ctx := t.Context()

	file, _ := readyFileWithVariant(t)

	stored, err := repo.ByID(ctx, file.ID())
	require.NoError(t, err)

	assert.True(t, stored.Status().Equal(domain.FileStatusReady))
	assert.Equal(t, testChecksum(t).String(), stored.Checksum().String())
	assert.Equal(t, "300x300", stored.Dimensions().String())
	assert.Equal(t, int64(3), stored.Version(), "two saves advanced the version twice")
	assert.True(t, stored.IsServable())
}

func TestFiles_SaveRecordsDeletion(t *testing.T) {
	t.Parallel()

	repo := NewFiles(sharedDB)
	ctx := t.Context()

	file := testFile(t)
	require.NoError(t, repo.Create(ctx, &file))

	deletedAt := time.Now().Truncate(time.Microsecond)
	require.NoError(t, file.MarkForDeletion(deletedAt))
	require.NoError(t, repo.Save(ctx, &file))

	stored, err := repo.ByID(ctx, file.ID())
	require.NoError(t, err)

	assert.True(t, stored.IsDeleted())
	assert.False(t, stored.IsServable())

	got, ok := stored.DeletedAt()
	require.True(t, ok)
	assert.True(t, deletedAt.Equal(got))
}

func TestFiles_SaveRecordsRejection(t *testing.T) {
	t.Parallel()

	repo := NewFiles(sharedDB)
	ctx := t.Context()

	file := testFile(t)
	require.NoError(t, repo.Create(ctx, &file))
	require.NoError(t, file.StartProcessing(time.Now()))
	require.NoError(t, repo.Save(ctx, &file))
	require.NoError(t, file.Reject(domain.RejectReasonTooManyPixels, time.Now()))
	require.NoError(t, repo.Save(ctx, &file))

	stored, err := repo.ByID(ctx, file.ID())
	require.NoError(t, err)
	assert.True(t, stored.RejectReason().Equal(domain.RejectReasonTooManyPixels))
}

func TestFiles_StaleVersionIsRejected(t *testing.T) {
	t.Parallel()

	repo := NewFiles(sharedDB)
	ctx := t.Context()

	file := testFile(t)
	require.NoError(t, repo.Create(ctx, &file))

	winner, err := repo.ByID(ctx, file.ID())
	require.NoError(t, err)
	require.NoError(t, winner.MarkForDeletion(time.Now()))
	require.NoError(t, repo.Save(ctx, &winner))

	require.NoError(t, file.StartProcessing(time.Now()))

	err = repo.Save(ctx, &file)
	require.ErrorIs(t, err, usecase.ErrStaleVersion,
		"the worker must not resurrect a file that is being deleted")
}

func TestFiles_NotFound(t *testing.T) {
	t.Parallel()

	id, err := domain.NewFileID()
	require.NoError(t, err)

	_, err = NewFiles(sharedDB).ByID(t.Context(), id)
	require.ErrorIs(t, err, domain.ErrFileNotFound)
}

func TestFiles_ViewsByIDs(t *testing.T) {
	t.Parallel()

	repo := NewFiles(sharedDB)
	ctx := t.Context()

	first, _ := readyFileWithVariant(t)
	second := testFile(t)
	require.NoError(t, repo.Create(ctx, &second))

	deleted := testFile(t)
	require.NoError(t, repo.Create(ctx, &deleted))
	require.NoError(t, deleted.MarkForDeletion(time.Now()))
	require.NoError(t, repo.Save(ctx, &deleted))

	absent, err := domain.NewFileID()
	require.NoError(t, err)

	views, err := repo.ViewsByIDs(ctx, []domain.FileID{
		first.ID(), second.ID(), deleted.ID(), absent,
	})
	require.NoError(t, err)

	byID := make(map[string]usecase.FileView, len(views))
	for _, view := range views {
		byID[view.ID.String()] = view
	}

	require.Len(t, views, 2, "deleted and unknown ids are omitted, not errors")
	assert.Contains(t, byID, first.ID().String())
	assert.Contains(t, byID, second.ID().String())

	ready := byID[first.ID().String()]
	assert.Equal(t, "avatar", ready.Namespace.String())
	assert.Equal(t, int32(300), ready.Width)
	assert.True(t, ready.Status.Equal(domain.FileStatusReady))
}

func TestFiles_StaleByStatus(t *testing.T) {
	t.Parallel()

	repo := NewFiles(sharedDB)
	ctx := t.Context()

	long := time.Now().Add(-2 * staleWindow)

	oldest := testFile(t)
	require.NoError(t, repo.Create(ctx, &oldest))
	require.NoError(t, oldest.StartProcessing(long.Add(-time.Hour)))
	require.NoError(t, repo.Save(ctx, &oldest))

	stuck := testFile(t)
	require.NoError(t, repo.Create(ctx, &stuck))
	require.NoError(t, stuck.StartProcessing(long))
	require.NoError(t, repo.Save(ctx, &stuck))

	fresh := testFile(t)
	require.NoError(t, repo.Create(ctx, &fresh))
	require.NoError(t, fresh.StartProcessing(time.Now()))
	require.NoError(t, repo.Save(ctx, &fresh))

	before := time.Now().Add(-staleWindow)

	found, err := repo.StaleByStatus(ctx,
		[]domain.FileStatus{domain.FileStatusProcessing, domain.FileStatusDeleting},
		before, 100)
	require.NoError(t, err)

	ids := make(map[string]bool, len(found))
	for _, file := range found {
		ids[file.ID().String()] = true
	}

	assert.True(t, ids[oldest.ID().String()])
	assert.True(t, ids[stuck.ID().String()])
	assert.False(t, ids[fresh.ID().String()], "a file updated a moment ago is not stale")

	position := make(map[string]int, len(found))
	for i, file := range found {
		position[file.ID().String()] = i
	}

	assert.Less(t, position[oldest.ID().String()], position[stuck.ID().String()],
		"the sweep takes the oldest first, so the limit cannot starve a stuck row")

	limited, err := repo.StaleByStatus(ctx,
		[]domain.FileStatus{domain.FileStatusProcessing}, before, 1)
	require.NoError(t, err)
	assert.Len(t, limited, 1, "the limit caps what one sweep takes on")
}
