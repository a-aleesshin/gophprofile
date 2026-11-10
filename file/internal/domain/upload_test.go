package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testUploadTTL = 15 * time.Minute

func testUploadID(t *testing.T) UploadID {
	t.Helper()

	id, err := ParseUploadID("01a19119-c7c0-748d-b97c-ebfd9d4263d7")
	require.NoError(t, err)

	return id
}

func testNewUploadParams(t *testing.T) NewUploadParams {
	t.Helper()

	namespace, err := NewNamespace("avatar")
	require.NoError(t, err)

	key, err := NewIdempotencyKey("01a18818-c65b-732b-9e0a-e8603949ed3d")
	require.NoError(t, err)

	originalName, err := NewOriginalName("avatar.jpg")
	require.NoError(t, err)

	declaredMime, err := NewMimeType("image/jpeg")
	require.NoError(t, err)

	maxSize, err := NewSize(10 << 20)
	require.NoError(t, err)

	return NewUploadParams{
		ID:             testUploadID(t),
		Namespace:      namespace,
		OwnerRef:       testOwnerRef(t),
		IdempotencyKey: key,
		OriginalName:   originalName,
		DeclaredMime:   declaredMime,
		MaxSize:        maxSize,
		TTL:            testUploadTTL,
		Now:            testNow,
	}
}

func newTestUpload(t *testing.T) Upload {
	t.Helper()

	u, err := NewUpload(testNewUploadParams(t))
	require.NoError(t, err)

	return u
}

func TestNewUpload(t *testing.T) {
	t.Parallel()

	t.Run("initial state", func(t *testing.T) {
		t.Parallel()

		u := newTestUpload(t)

		assert.Equal(t, int64(1), u.Version())
		assert.True(t, u.Status().Equal(UploadStatusPending))
		assert.True(t, u.IsPending())
		assert.True(t, u.FileID().IsZero())
		assert.Equal(t, int64(10<<20), u.MaxSize().Bytes())
		assert.Equal(t, "image/jpeg", u.DeclaredMime().String())
		assert.Equal(t, testNow, u.CreatedAt())
		assert.Equal(t, testNow.Add(testUploadTTL), u.ExpiresAt())
	})

	t.Run("location is derived from the id", func(t *testing.T) {
		t.Parallel()

		u := newTestUpload(t)

		want, err := NewStagingLocation(u.ID())
		require.NoError(t, err)
		assert.True(t, u.Location().Equal(want))
		assert.Equal(t, "staging/01a19119-c7c0-748d-b97c-ebfd9d4263d7", u.Location().String())
	})

	t.Run("declared mime is optional", func(t *testing.T) {
		t.Parallel()

		p := testNewUploadParams(t)
		p.DeclaredMime = MimeType{}

		u, err := NewUpload(p)
		require.NoError(t, err)
		assert.True(t, u.DeclaredMime().IsZero())
	})

	t.Run("timestamps are normalized to utc", func(t *testing.T) {
		t.Parallel()

		p := testNewUploadParams(t)
		p.Now = testNow.In(time.FixedZone("MSK", 3*60*60))

		u, err := NewUpload(p)
		require.NoError(t, err)
		assert.Equal(t, time.UTC, u.CreatedAt().Location())
		assert.True(t, u.ExpiresAt().Equal(testNow.Add(testUploadTTL)))
	})

	t.Run("required fields", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*NewUploadParams)
		}{
			{name: "id", mutate: func(p *NewUploadParams) { p.ID = UploadID{} }},
			{name: "namespace", mutate: func(p *NewUploadParams) { p.Namespace = Namespace{} }},
			{name: "owner ref", mutate: func(p *NewUploadParams) { p.OwnerRef = OwnerRef{} }},
			{name: "idempotency key", mutate: func(p *NewUploadParams) { p.IdempotencyKey = IdempotencyKey{} }},
			{name: "original name", mutate: func(p *NewUploadParams) { p.OriginalName = OriginalName{} }},
			{name: "max size", mutate: func(p *NewUploadParams) { p.MaxSize = Size{} }},
			{name: "zero ttl", mutate: func(p *NewUploadParams) { p.TTL = 0 }},
			{name: "negative ttl", mutate: func(p *NewUploadParams) { p.TTL = -time.Minute }},
			{name: "now", mutate: func(p *NewUploadParams) { p.Now = time.Time{} }},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				p := testNewUploadParams(t)
				tt.mutate(&p)

				u, err := NewUpload(p)
				require.ErrorIs(t, err, ErrInvalidUpload)
				assert.Equal(t, Upload{}, u)
			})
		}
	})
}

func TestUpload_Complete(t *testing.T) {
	t.Parallel()

	t.Run("links the file", func(t *testing.T) {
		t.Parallel()

		u := newTestUpload(t)
		later := testNow.Add(time.Minute)

		require.NoError(t, u.Complete(testFileID(t), later))
		assert.True(t, u.Status().Equal(UploadStatusUploaded))
		assert.True(t, u.FileID().Equal(testFileID(t)))
		assert.False(t, u.IsPending())
		assert.Equal(t, later, u.UpdatedAt())
	})

	t.Run("file id is required", func(t *testing.T) {
		t.Parallel()

		u := newTestUpload(t)

		err := u.Complete(FileID{}, testNow)
		require.ErrorIs(t, err, ErrInvalidUpload)
		assert.True(t, u.Status().Equal(UploadStatusPending))
		assert.True(t, u.FileID().IsZero())
	})

	t.Run("expiry does not block completion", func(t *testing.T) {
		t.Parallel()

		u := newTestUpload(t)
		wayLater := u.ExpiresAt().Add(time.Hour)
		require.True(t, u.IsExpired(wayLater))

		require.NoError(t, u.Complete(testFileID(t), wayLater))
		assert.True(t, u.Status().Equal(UploadStatusUploaded))
	})

	t.Run("not allowed twice", func(t *testing.T) {
		t.Parallel()

		u := newTestUpload(t)
		require.NoError(t, u.Complete(testFileID(t), testNow))

		err := u.Complete(testFileID(t), testNow)
		require.ErrorIs(t, err, ErrForbiddenUploadTransition)
	})
}

func TestUpload_ExpireAndAbort(t *testing.T) {
	t.Parallel()

	t.Run("expire", func(t *testing.T) {
		t.Parallel()

		u := newTestUpload(t)
		require.NoError(t, u.Expire(testNow))
		assert.True(t, u.Status().Equal(UploadStatusExpired))
		assert.True(t, u.Status().IsTerminal())
	})

	t.Run("abort", func(t *testing.T) {
		t.Parallel()

		u := newTestUpload(t)
		require.NoError(t, u.Abort(testNow))
		assert.True(t, u.Status().Equal(UploadStatusAborted))
	})

	t.Run("terminal states accept nothing", func(t *testing.T) {
		t.Parallel()

		u := newTestUpload(t)
		require.NoError(t, u.Expire(testNow))

		require.ErrorIs(t, u.Complete(testFileID(t), testNow), ErrForbiddenUploadTransition)
		require.ErrorIs(t, u.Abort(testNow), ErrForbiddenUploadTransition)
		require.ErrorIs(t, u.Expire(testNow), ErrForbiddenUploadTransition)
		assert.True(t, u.Status().Equal(UploadStatusExpired))
	})

	t.Run("now is required", func(t *testing.T) {
		t.Parallel()

		u := newTestUpload(t)

		require.ErrorIs(t, u.Expire(time.Time{}), ErrInvalidUpload)
		require.ErrorIs(t, u.Abort(time.Time{}), ErrInvalidUpload)
		require.ErrorIs(t, u.Complete(testFileID(t), time.Time{}), ErrInvalidUpload)
		assert.True(t, u.IsPending())
	})
}

func TestUpload_IsExpired(t *testing.T) {
	t.Parallel()

	u := newTestUpload(t)

	assert.False(t, u.IsExpired(testNow))
	assert.False(t, u.IsExpired(u.ExpiresAt()))
	assert.True(t, u.IsExpired(u.ExpiresAt().Add(time.Nanosecond)))

	require.NoError(t, u.Complete(testFileID(t), testNow))
	assert.False(t, u.IsExpired(u.ExpiresAt().Add(time.Hour)),
		"only a pending upload can expire")
}

func TestUpload_SetVersion(t *testing.T) {
	t.Parallel()

	u := newTestUpload(t)

	require.NoError(t, u.SetVersion(2))
	assert.Equal(t, int64(2), u.Version())

	require.ErrorIs(t, u.SetVersion(2), ErrInvalidUpload)
	require.ErrorIs(t, u.SetVersion(1), ErrInvalidUpload)
	assert.Equal(t, int64(2), u.Version())
}

func TestUpload_DomainNeverBumpsVersion(t *testing.T) {
	t.Parallel()

	u := newTestUpload(t)
	require.NoError(t, u.Complete(testFileID(t), testNow))
	assert.Equal(t, int64(1), u.Version())
}

func testRestoreUploadParams(t *testing.T) RestoreUploadParams {
	t.Helper()

	p := testNewUploadParams(t)

	location, err := NewStagingLocation(p.ID)
	require.NoError(t, err)

	return RestoreUploadParams{
		ID:             p.ID,
		Version:        4,
		Namespace:      p.Namespace,
		OwnerRef:       p.OwnerRef,
		IdempotencyKey: p.IdempotencyKey,
		OriginalName:   p.OriginalName,
		DeclaredMime:   p.DeclaredMime,
		MaxSize:        p.MaxSize,
		Location:       location,
		Status:         UploadStatusPending,
		ExpiresAt:      testNow.Add(testUploadTTL),
		CreatedAt:      testNow,
		UpdatedAt:      testNow,
	}
}

func TestRestoreUpload(t *testing.T) {
	t.Parallel()

	t.Run("round trip", func(t *testing.T) {
		t.Parallel()

		u, err := RestoreUpload(testRestoreUploadParams(t))
		require.NoError(t, err)
		assert.Equal(t, int64(4), u.Version())
		assert.True(t, u.IsPending())
		assert.True(t, u.FileID().IsZero())
	})

	t.Run("completed upload keeps its file id", func(t *testing.T) {
		t.Parallel()

		p := testRestoreUploadParams(t)
		p.Status = UploadStatusUploaded
		p.FileID = testFileID(t)

		u, err := RestoreUpload(p)
		require.NoError(t, err)
		assert.True(t, u.FileID().Equal(testFileID(t)))
	})

	t.Run("creation rules are not reapplied", func(t *testing.T) {
		t.Parallel()

		p := testRestoreUploadParams(t)
		p.OriginalName = OriginalName{}
		p.MaxSize = Size{}
		p.DeclaredMime = MimeType{}

		u, err := RestoreUpload(p)
		require.NoError(t, err)
		assert.True(t, u.MaxSize().IsZero())
	})

	t.Run("required fields", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*RestoreUploadParams)
		}{
			{name: "id", mutate: func(p *RestoreUploadParams) { p.ID = UploadID{} }},
			{name: "zero version", mutate: func(p *RestoreUploadParams) { p.Version = 0 }},
			{name: "namespace", mutate: func(p *RestoreUploadParams) { p.Namespace = Namespace{} }},
			{name: "owner ref", mutate: func(p *RestoreUploadParams) { p.OwnerRef = OwnerRef{} }},
			{name: "idempotency key", mutate: func(p *RestoreUploadParams) { p.IdempotencyKey = IdempotencyKey{} }},
			{name: "location", mutate: func(p *RestoreUploadParams) { p.Location = Location{} }},
			{name: "zero status", mutate: func(p *RestoreUploadParams) { p.Status = UploadStatus{} }},
			{name: "unknown status", mutate: func(p *RestoreUploadParams) { p.Status = UploadStatus{value: "garbage"} }},
			{name: "expires at", mutate: func(p *RestoreUploadParams) { p.ExpiresAt = time.Time{} }},
			{name: "created at", mutate: func(p *RestoreUploadParams) { p.CreatedAt = time.Time{} }},
			{name: "updated at", mutate: func(p *RestoreUploadParams) { p.UpdatedAt = time.Time{} }},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				p := testRestoreUploadParams(t)
				tt.mutate(&p)

				u, err := RestoreUpload(p)
				require.ErrorIs(t, err, ErrInvalidUpload)
				assert.Equal(t, Upload{}, u)
			})
		}
	})
}

func TestUpload_IsOwnedBy(t *testing.T) {
	t.Parallel()

	u := newTestUpload(t)

	other, err := NewOwnerRef("user-456")
	require.NoError(t, err)

	assert.True(t, u.IsOwnedBy(testOwnerRef(t)))
	assert.False(t, u.IsOwnedBy(other))
	assert.False(t, u.IsOwnedBy(OwnerRef{}))
}

func TestUpload_MarshalJSON(t *testing.T) {
	t.Parallel()

	u := newTestUpload(t)

	data, err := json.Marshal(&u)
	require.Error(t, err)
	assert.Empty(t, data)
}
