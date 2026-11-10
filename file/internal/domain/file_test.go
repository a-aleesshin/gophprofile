package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testNow = time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)

func testOwnerRef(t *testing.T) OwnerRef {
	t.Helper()

	owner, err := NewOwnerRef("user-123")
	require.NoError(t, err)

	return owner
}

func testNewFileParams(t *testing.T) NewFileParams {
	t.Helper()

	namespace, err := NewNamespace("avatar")
	require.NoError(t, err)

	originalName, err := NewOriginalName("avatar.jpg")
	require.NoError(t, err)

	mimeType, err := NewMimeType("image/jpeg")
	require.NoError(t, err)

	size, err := NewSize(1024)
	require.NoError(t, err)

	return NewFileParams{
		ID:           testFileID(t),
		Namespace:    namespace,
		OwnerRef:     testOwnerRef(t),
		OriginalName: originalName,
		MimeType:     mimeType,
		Size:         size,
		Now:          testNow,
	}
}

func newTestFile(t *testing.T) File {
	t.Helper()

	f, err := NewFile(testNewFileParams(t))
	require.NoError(t, err)

	return f
}

func testChecksum(t *testing.T) Checksum {
	t.Helper()

	c, err := RestoreChecksum("ab" + "cd" + "ef01" + "23456789abcdef0123456789abcdef0123456789abcdef0123456789")
	require.NoError(t, err)

	return c
}

func testDimensions(t *testing.T) Dimensions {
	t.Helper()

	d, err := NewDimensions(1920, 1080)
	require.NoError(t, err)

	return d
}

func TestNewFile(t *testing.T) {
	t.Parallel()

	t.Run("initial state", func(t *testing.T) {
		t.Parallel()

		f := newTestFile(t)

		assert.Equal(t, int64(1), f.Version())
		assert.True(t, f.Status().Equal(FileStatusReceived))
		assert.Equal(t, "avatar", f.Namespace().String())
		assert.Equal(t, "user-123", f.OwnerRef().String())
		assert.Equal(t, "avatar.jpg", f.OriginalName().String())
		assert.Equal(t, "image/jpeg", f.MimeType().String())
		assert.Equal(t, int64(1024), f.Size().Bytes())
		assert.True(t, f.Checksum().IsZero())
		assert.True(t, f.Dimensions().IsZero())
		assert.True(t, f.RejectReason().IsZero())
		assert.False(t, f.IsDeleted())
		assert.False(t, f.IsServable())
		assert.Equal(t, testNow, f.CreatedAt())
		assert.Equal(t, testNow, f.UpdatedAt())

		_, deleted := f.DeletedAt()
		assert.False(t, deleted)
	})

	t.Run("location is derived from the id", func(t *testing.T) {
		t.Parallel()

		f := newTestFile(t)

		want, err := NewOriginalLocation(f.ID())
		require.NoError(t, err)
		assert.True(t, f.Location().Equal(want))
	})

	t.Run("timestamps are normalized to utc", func(t *testing.T) {
		t.Parallel()

		moscow := time.FixedZone("MSK", 3*60*60)

		p := testNewFileParams(t)
		p.Now = testNow.In(moscow)

		f, err := NewFile(p)
		require.NoError(t, err)
		assert.Equal(t, time.UTC, f.CreatedAt().Location())
		assert.True(t, f.CreatedAt().Equal(testNow))
	})

	t.Run("required fields", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*NewFileParams)
		}{
			{name: "id", mutate: func(p *NewFileParams) { p.ID = FileID{} }},
			{name: "namespace", mutate: func(p *NewFileParams) { p.Namespace = Namespace{} }},
			{name: "owner ref", mutate: func(p *NewFileParams) { p.OwnerRef = OwnerRef{} }},
			{name: "original name", mutate: func(p *NewFileParams) { p.OriginalName = OriginalName{} }},
			{name: "mime type", mutate: func(p *NewFileParams) { p.MimeType = MimeType{} }},
			{name: "size", mutate: func(p *NewFileParams) { p.Size = Size{} }},
			{name: "now", mutate: func(p *NewFileParams) { p.Now = time.Time{} }},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				p := testNewFileParams(t)
				tt.mutate(&p)

				f, err := NewFile(p)
				require.ErrorIs(t, err, ErrInvalidFile)
				assert.Equal(t, File{}, f)
			})
		}
	})
}

func TestFile_HappyPath(t *testing.T) {
	t.Parallel()

	f := newTestFile(t)
	later := testNow.Add(time.Minute)

	require.NoError(t, f.StartProcessing(later))
	assert.True(t, f.Status().Equal(FileStatusProcessing))
	assert.Equal(t, later, f.UpdatedAt())

	ready := later.Add(time.Minute)
	require.NoError(t, f.MarkReady(testChecksum(t), testDimensions(t), ready))
	assert.True(t, f.Status().Equal(FileStatusReady))
	assert.True(t, f.IsServable())
	assert.False(t, f.Checksum().IsZero())
	assert.Equal(t, "1920x1080", f.Dimensions().String())
	assert.Equal(t, int64(1), f.Version())
}

func TestFile_DocumentHasNoDimensions(t *testing.T) {
	t.Parallel()

	f := newTestFile(t)
	require.NoError(t, f.StartProcessing(testNow))
	require.NoError(t, f.MarkReady(testChecksum(t), Dimensions{}, testNow))

	assert.True(t, f.Status().Equal(FileStatusReady))
	assert.True(t, f.Dimensions().IsZero())
}

func TestFile_MarkReady(t *testing.T) {
	t.Parallel()

	t.Run("checksum is required", func(t *testing.T) {
		t.Parallel()

		f := newTestFile(t)
		require.NoError(t, f.StartProcessing(testNow))

		err := f.MarkReady(Checksum{}, testDimensions(t), testNow)
		require.ErrorIs(t, err, ErrInvalidFile)
		assert.True(t, f.Status().Equal(FileStatusProcessing))
		assert.True(t, f.Dimensions().IsZero())
	})

	t.Run("not allowed straight from received", func(t *testing.T) {
		t.Parallel()

		f := newTestFile(t)

		err := f.MarkReady(testChecksum(t), testDimensions(t), testNow)
		require.ErrorIs(t, err, ErrForbiddenFileTransition)
		assert.True(t, f.Status().Equal(FileStatusReceived))
		assert.True(t, f.Checksum().IsZero())
	})

	t.Run("lost race against deletion", func(t *testing.T) {
		t.Parallel()

		f := newTestFile(t)
		require.NoError(t, f.StartProcessing(testNow))
		require.NoError(t, f.MarkForDeletion(testNow))

		err := f.MarkReady(testChecksum(t), testDimensions(t), testNow)
		require.ErrorIs(t, err, ErrForbiddenFileTransition)
		assert.True(t, f.Status().Equal(FileStatusDeleting))
		assert.True(t, f.Checksum().IsZero())
	})
}

func TestFile_Reject(t *testing.T) {
	t.Parallel()

	t.Run("records the reason", func(t *testing.T) {
		t.Parallel()

		f := newTestFile(t)
		require.NoError(t, f.StartProcessing(testNow))
		require.NoError(t, f.Reject(RejectReasonDecodeFailed, testNow))

		assert.True(t, f.Status().Equal(FileStatusRejected))
		assert.True(t, f.RejectReason().Equal(RejectReasonDecodeFailed))
	})

	t.Run("reason is required", func(t *testing.T) {
		t.Parallel()

		f := newTestFile(t)
		require.NoError(t, f.StartProcessing(testNow))

		err := f.Reject(RejectReason{}, testNow)
		require.ErrorIs(t, err, ErrInvalidFile)
		assert.True(t, f.Status().Equal(FileStatusProcessing))
		assert.True(t, f.RejectReason().IsZero())
	})

	t.Run("rejected file can still be cleaned up", func(t *testing.T) {
		t.Parallel()

		f := newTestFile(t)
		require.NoError(t, f.StartProcessing(testNow))
		require.NoError(t, f.Reject(RejectReasonInternalError, testNow))
		require.NoError(t, f.MarkForDeletion(testNow))
		require.NoError(t, f.MarkDeleted(testNow))

		assert.True(t, f.Status().Equal(FileStatusDeleted))
		assert.True(t, f.RejectReason().Equal(RejectReasonInternalError))
	})
}

func TestFile_Deletion(t *testing.T) {
	t.Parallel()

	t.Run("sets deleted at and stops being servable", func(t *testing.T) {
		t.Parallel()

		f := newTestFile(t)
		require.NoError(t, f.StartProcessing(testNow))
		require.NoError(t, f.MarkReady(testChecksum(t), testDimensions(t), testNow))
		require.True(t, f.IsServable())

		deletedAt := testNow.Add(time.Hour)
		require.NoError(t, f.MarkForDeletion(deletedAt))

		assert.True(t, f.IsDeleted())
		assert.False(t, f.IsServable())

		got, ok := f.DeletedAt()
		require.True(t, ok)
		assert.Equal(t, deletedAt, got)
	})

	t.Run("delete while processing", func(t *testing.T) {
		t.Parallel()

		f := newTestFile(t)
		require.NoError(t, f.StartProcessing(testNow))
		require.NoError(t, f.MarkForDeletion(testNow))

		assert.True(t, f.Status().Equal(FileStatusDeleting))
	})

	t.Run("deleted is terminal", func(t *testing.T) {
		t.Parallel()

		f := newTestFile(t)
		require.NoError(t, f.MarkForDeletion(testNow))
		require.NoError(t, f.MarkDeleted(testNow))

		require.ErrorIs(t, f.MarkDeleted(testNow), ErrForbiddenFileTransition)
		require.ErrorIs(t, f.StartProcessing(testNow), ErrForbiddenFileTransition)
		assert.True(t, f.Status().IsTerminal())
	})
}

func TestFile_NowIsRequired(t *testing.T) {
	t.Parallel()

	f := newTestFile(t)

	require.ErrorIs(t, f.StartProcessing(time.Time{}), ErrInvalidFile)
	require.ErrorIs(t, f.MarkForDeletion(time.Time{}), ErrInvalidFile)
	assert.True(t, f.Status().Equal(FileStatusReceived))
}

func TestFile_SetVersion(t *testing.T) {
	t.Parallel()

	f := newTestFile(t)

	require.NoError(t, f.SetVersion(2))
	assert.Equal(t, int64(2), f.Version())

	require.ErrorIs(t, f.SetVersion(2), ErrInvalidFile)
	require.ErrorIs(t, f.SetVersion(1), ErrInvalidFile)
	assert.Equal(t, int64(2), f.Version())
}

func TestFile_DomainNeverBumpsVersion(t *testing.T) {
	t.Parallel()

	f := newTestFile(t)

	require.NoError(t, f.StartProcessing(testNow))
	require.NoError(t, f.MarkReady(testChecksum(t), testDimensions(t), testNow))
	require.NoError(t, f.MarkForDeletion(testNow))
	require.NoError(t, f.MarkDeleted(testNow))

	assert.Equal(t, int64(1), f.Version())
}

func testRestoreFileParams(t *testing.T) RestoreFileParams {
	t.Helper()

	p := testNewFileParams(t)

	location, err := NewOriginalLocation(p.ID)
	require.NoError(t, err)

	return RestoreFileParams{
		ID:           p.ID,
		Version:      7,
		Namespace:    p.Namespace,
		OwnerRef:     p.OwnerRef,
		OriginalName: p.OriginalName,
		MimeType:     p.MimeType,
		Size:         p.Size,
		Checksum:     testChecksum(t),
		Dimensions:   testDimensions(t),
		Location:     location,
		Status:       FileStatusReady,
		CreatedAt:    testNow,
		UpdatedAt:    testNow.Add(time.Minute),
	}
}

func TestRestoreFile(t *testing.T) {
	t.Parallel()

	t.Run("round trip", func(t *testing.T) {
		t.Parallel()

		p := testRestoreFileParams(t)

		f, err := RestoreFile(p)
		require.NoError(t, err)
		assert.Equal(t, int64(7), f.Version())
		assert.True(t, f.Status().Equal(FileStatusReady))
		assert.True(t, f.IsServable())
		assert.False(t, f.IsDeleted())
	})

	t.Run("deleted at is copied, not aliased", func(t *testing.T) {
		t.Parallel()

		deletedAt := testNow.Add(time.Hour)

		p := testRestoreFileParams(t)
		p.Status = FileStatusDeleted
		p.DeletedAt = &deletedAt

		f, err := RestoreFile(p)
		require.NoError(t, err)

		deletedAt = deletedAt.Add(24 * time.Hour)

		got, ok := f.DeletedAt()
		require.True(t, ok)
		assert.Equal(t, testNow.Add(time.Hour), got)
	})

	t.Run("creation rules are not reapplied", func(t *testing.T) {
		t.Parallel()

		p := testRestoreFileParams(t)
		p.OriginalName = OriginalName{}
		p.MimeType = MimeType{}
		p.Size = Size{}
		p.Checksum = Checksum{}

		f, err := RestoreFile(p)
		require.NoError(t, err)
		assert.True(t, f.MimeType().IsZero())
	})

	t.Run("required fields", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*RestoreFileParams)
		}{
			{name: "id", mutate: func(p *RestoreFileParams) { p.ID = FileID{} }},
			{name: "zero version", mutate: func(p *RestoreFileParams) { p.Version = 0 }},
			{name: "negative version", mutate: func(p *RestoreFileParams) { p.Version = -1 }},
			{name: "namespace", mutate: func(p *RestoreFileParams) { p.Namespace = Namespace{} }},
			{name: "owner ref", mutate: func(p *RestoreFileParams) { p.OwnerRef = OwnerRef{} }},
			{name: "location", mutate: func(p *RestoreFileParams) { p.Location = Location{} }},
			{name: "zero status", mutate: func(p *RestoreFileParams) { p.Status = FileStatus{} }},
			{name: "unknown status", mutate: func(p *RestoreFileParams) { p.Status = FileStatus{value: "garbage"} }},
			{name: "created at", mutate: func(p *RestoreFileParams) { p.CreatedAt = time.Time{} }},
			{name: "updated at", mutate: func(p *RestoreFileParams) { p.UpdatedAt = time.Time{} }},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				p := testRestoreFileParams(t)
				tt.mutate(&p)

				f, err := RestoreFile(p)
				require.ErrorIs(t, err, ErrInvalidFile)
				assert.Equal(t, File{}, f)
			})
		}
	})
}

func TestFile_IsOwnedBy(t *testing.T) {
	t.Parallel()

	f := newTestFile(t)

	other, err := NewOwnerRef("user-456")
	require.NoError(t, err)

	assert.True(t, f.IsOwnedBy(testOwnerRef(t)))
	assert.False(t, f.IsOwnedBy(other))
	assert.False(t, f.IsOwnedBy(OwnerRef{}))
}

func TestFile_MarshalJSON(t *testing.T) {
	t.Parallel()

	f := newTestFile(t)

	data, err := json.Marshal(&f)
	require.Error(t, err)
	assert.Empty(t, data)

	data, err = json.Marshal(struct {
		File *File `json:"file"`
	}{File: &f})
	require.Error(t, err)
	assert.Empty(t, data)

	data, err = json.Marshal(f)
	require.NoError(t, err)
	assert.JSONEq(t, "{}", string(data),
		"a pointer receiver does not cover a File value; the aggregate is only ever passed as *File")
}

func TestFile_MustBePassedByPointer(t *testing.T) {
	t.Parallel()

	byValue := func(f File) error { return f.StartProcessing(testNow) }
	byPointer := func(f *File) error { return f.StartProcessing(testNow) }

	f := newTestFile(t)

	require.NoError(t, byValue(f))
	assert.True(t, f.Status().Equal(FileStatusReceived),
		"mutating a copy must not affect the original; the aggregate is only ever passed as *File")

	require.NoError(t, byPointer(&f))
	assert.True(t, f.Status().Equal(FileStatusProcessing))
}
