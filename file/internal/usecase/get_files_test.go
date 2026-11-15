package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

func testFileViews(t *testing.T, n int) []FileView {
	t.Helper()

	views := make([]FileView, 0, n)
	for range n {
		views = append(views, testFileView(t))
	}

	return views
}

func idsOf(views []FileView) []domain.FileID {
	ids := make([]domain.FileID, 0, len(views))
	for _, view := range views {
		ids = append(ids, view.ID)
	}

	return ids
}

func TestGetFiles(t *testing.T) {
	t.Parallel()

	t.Run("one query per table regardless of batch size", func(t *testing.T) {
		t.Parallel()

		views := testFileViews(t, 5)
		variants := map[domain.FileID][]VariantView{
			views[0].ID: {{Name: "300x300", Ext: "webp"}},
			views[3].ID: {{Name: "100x100", Ext: "webp"}, {Name: "300x300", Ext: "webp"}},
		}

		files := &stubFileViews{views: views}
		lister := &stubVariantViews{views: variants}
		handler := NewGetFilesHandler(files, lister)

		got, err := handler.Handle(context.Background(), GetFilesCommand{FileIDs: idsOf(views)})
		require.NoError(t, err)

		assert.Len(t, got.Files, 5)
		assert.Equal(t, 1, files.calls, "a batch call must not become n+1 inside the service")
		assert.Equal(t, 1, lister.calls)
		assert.Len(t, got.Files[0].Variants, 1)
		assert.Len(t, got.Files[3].Variants, 2)
		assert.Empty(t, got.Files[1].Variants)
	})

	t.Run("keeps the requested order", func(t *testing.T) {
		t.Parallel()

		views := testFileViews(t, 3)
		reversed := []domain.FileID{views[2].ID, views[1].ID, views[0].ID}

		handler := NewGetFilesHandler(
			&stubFileViews{views: views},
			&stubVariantViews{},
		)

		got, err := handler.Handle(context.Background(), GetFilesCommand{FileIDs: reversed})
		require.NoError(t, err)

		require.Len(t, got.Files, 3)
		for i, id := range reversed {
			assert.True(t, got.Files[i].File.ID.Equal(id),
				"the caller matches results positionally, so order must survive the round trip")
		}
	})

	t.Run("missing files are omitted, not an error", func(t *testing.T) {
		t.Parallel()

		views := testFileViews(t, 3)

		absent, err := domain.NewFileID()
		require.NoError(t, err)

		handler := NewGetFilesHandler(
			&stubFileViews{views: views[:2]},
			&stubVariantViews{},
		)

		got, err := handler.Handle(context.Background(), GetFilesCommand{
			FileIDs: []domain.FileID{views[0].ID, absent, views[1].ID},
		})
		require.NoError(t, err)

		assert.Len(t, got.Files, 2,
			"a deleted or unknown file is an omission; the caller diffs against what it asked for")
	})

	t.Run("duplicates are collapsed before querying", func(t *testing.T) {
		t.Parallel()

		views := testFileViews(t, 1)
		files := &stubFileViews{views: views}

		handler := NewGetFilesHandler(files, &stubVariantViews{})

		got, err := handler.Handle(context.Background(), GetFilesCommand{
			FileIDs: []domain.FileID{views[0].ID, views[0].ID, views[0].ID},
		})
		require.NoError(t, err)

		assert.Len(t, files.batchIDs, 1)
		assert.Len(t, got.Files, 1)
	})

	t.Run("rejects a bad batch", func(t *testing.T) {
		t.Parallel()

		views := testFileViews(t, 1)

		tests := []struct {
			name string
			ids  []domain.FileID
		}{
			{name: "empty", ids: nil},
			{name: "zero id", ids: []domain.FileID{views[0].ID, {}}},
			{name: "over the cap", ids: idsOf(testFileViews(t, MaxFileBatch+1))},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				files := &stubFileViews{views: views}
				handler := NewGetFilesHandler(files, &stubVariantViews{})

				_, err := handler.Handle(context.Background(), GetFilesCommand{FileIDs: tt.ids})
				require.ErrorIs(t, err, ErrInvalidCommand)
				assert.Zero(t, files.calls,
					"a batch without a cap just moves the problem into the database")
			})
		}
	})

	t.Run("propagates repository failures", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("connection reset")
		views := testFileViews(t, 2)

		handler := NewGetFilesHandler(&stubFileViews{err: boom}, &stubVariantViews{})
		_, err := handler.Handle(context.Background(), GetFilesCommand{FileIDs: idsOf(views)})
		require.ErrorIs(t, err, boom)

		handler = NewGetFilesHandler(
			&stubFileViews{views: views}, &stubVariantViews{err: boom},
		)
		_, err = handler.Handle(context.Background(), GetFilesCommand{FileIDs: idsOf(views)})
		require.ErrorIs(t, err, boom)
	})
}
