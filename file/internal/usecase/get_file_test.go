package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

type stubFileViews struct {
	views    []FileView
	err      error
	calls    int
	batchIDs []domain.FileID
}

func (s *stubFileViews) ViewsByIDs(
	_ context.Context, ids []domain.FileID,
) ([]FileView, error) {
	s.calls++
	s.batchIDs = ids

	return s.views, s.err
}

type stubVariantViews struct {
	views map[domain.FileID][]VariantView
	err   error
	calls int
}

func (s *stubVariantViews) ViewsByFileIDs(
	context.Context, []domain.FileID,
) (map[domain.FileID][]VariantView, error) {
	s.calls++

	return s.views, s.err
}

func testFileView(t *testing.T) FileView {
	t.Helper()

	id, err := domain.NewFileID()
	require.NoError(t, err)

	namespace, err := domain.NewNamespace("avatar")
	require.NoError(t, err)

	return FileView{
		ID:           id,
		Namespace:    namespace,
		OwnerRef:     "user-123",
		OriginalName: "avatar.jpg",
		MimeType:     "image/jpeg",
		SizeBytes:    2048,
		Width:        1920,
		Height:       1080,
		Status:       domain.FileStatusReady,
		CreatedAt:    testNow,
		UpdatedAt:    testNow,
	}
}

func TestGetFile(t *testing.T) {
	t.Parallel()

	t.Run("returns a projection, not an aggregate", func(t *testing.T) {
		t.Parallel()

		view := testFileView(t)
		handler := NewGetFileHandler(
			&stubFileViews{views: []FileView{view}},
			&stubVariantViews{views: map[domain.FileID][]VariantView{view.ID: {
				{Name: "100x100", Ext: "webp", MimeType: "image/webp", SizeBytes: 4096},
				{Name: "300x300", Ext: "webp", MimeType: "image/webp", SizeBytes: 8192},
			}}},
		)

		got, err := handler.Handle(context.Background(), GetFileCommand{FileID: view.ID})
		require.NoError(t, err)

		assert.True(t, got.File.ID.Equal(view.ID))
		assert.Equal(t, "image/jpeg", got.File.MimeType)
		assert.Equal(t, int64(2048), got.File.SizeBytes)
		assert.Len(t, got.Variants, 2)
		assert.Equal(t, "300x300", got.Variants[1].Name)
	})

	t.Run("a soft deleted file is reported as absent", func(t *testing.T) {
		t.Parallel()

		id, err := domain.NewFileID()
		require.NoError(t, err)

		handler := NewGetFileHandler(&stubFileViews{}, &stubVariantViews{})

		_, err = handler.Handle(context.Background(), GetFileCommand{FileID: id})
		require.ErrorIs(t, err, domain.ErrFileNotFound,
			"the query filters deleted rows, so a caller cannot tell a deleted file from one that never existed")
	})

	t.Run("zero file id", func(t *testing.T) {
		t.Parallel()

		files := &stubFileViews{}
		handler := NewGetFileHandler(files, &stubVariantViews{})

		_, err := handler.Handle(context.Background(), GetFileCommand{})
		require.ErrorIs(t, err, ErrInvalidCommand)
		assert.Zero(t, files.calls)
	})

	t.Run("variant lookup fails", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("connection reset")
		view := testFileView(t)
		handler := NewGetFileHandler(
			&stubFileViews{views: []FileView{view}},
			&stubVariantViews{err: boom},
		)

		_, err := handler.Handle(context.Background(), GetFileCommand{FileID: view.ID})
		require.ErrorIs(t, err, boom)
	})
}
