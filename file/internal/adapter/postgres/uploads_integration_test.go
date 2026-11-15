//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
	"github.com/a-aleesshin/gophprofile/file/migrations"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

const testImage = "postgres:17-alpine"

var sharedDB *pgtx.DB

func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, testImage,
		tcpostgres.WithDatabase("file"),
		tcpostgres.WithUsername("file"),
		tcpostgres.WithPassword("file"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run postgres: %v\n", err)
		os.Exit(1)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "connection string: %v\n", err)
		os.Exit(1)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new pool: %v\n", err)
		os.Exit(1)
	}

	if err := migrations.Up(ctx, pool); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}

	sharedDB = pgtx.New(pool)

	code := m.Run()

	pool.Close()

	if err := container.Terminate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "terminate postgres: %v\n", err)
	}

	os.Exit(code)
}

func testUpload(t *testing.T) domain.Upload {
	t.Helper()

	return testUploadAt(t, time.Now())
}

func testUploadAt(t *testing.T, now time.Time) domain.Upload {
	t.Helper()

	id, err := domain.NewUploadID()
	require.NoError(t, err)

	namespace, err := domain.NewNamespace("avatar")
	require.NoError(t, err)

	owner, err := domain.NewOwnerRef("user-" + uuid.NewString())
	require.NoError(t, err)

	key, err := domain.NewIdempotencyKey(uuid.NewString())
	require.NoError(t, err)

	name, err := domain.NewOriginalName("avatar.jpg")
	require.NoError(t, err)

	mime, err := domain.NewMimeType("image/jpeg")
	require.NoError(t, err)

	size, err := domain.NewSize(10 << 20)
	require.NoError(t, err)

	upload, err := domain.NewUpload(domain.NewUploadParams{
		ID:             id,
		Namespace:      namespace,
		OwnerRef:       owner,
		IdempotencyKey: key,
		OriginalName:   name,
		DeclaredMime:   mime,
		MaxSize:        size,
		TTL:            15 * time.Minute,
		Now:            now,
	})
	require.NoError(t, err)

	return upload
}

func insertFile(t *testing.T, id domain.FileID) {
	t.Helper()

	location, err := domain.NewOriginalLocation(id)
	require.NoError(t, err)

	_, err = sharedDB.Pool().Exec(t.Context(), `
		INSERT INTO files (id, namespace, owner_ref, original_name, mime_type,
		                   size_bytes, storage_key)
		VALUES ($1, 'avatar', 'user-1', 'avatar.jpg', 'image/jpeg', 2048, $2)`,
		id.UUID(), location.String())
	require.NoError(t, err)
}

func TestUploads_RoundTrip(t *testing.T) {
	t.Parallel()

	repo := NewUploads(sharedDB)
	ctx := t.Context()

	want := testUpload(t)
	require.NoError(t, repo.Create(ctx, &want))

	got, err := repo.ByID(ctx, want.ID())
	require.NoError(t, err)

	assert.True(t, got.ID().Equal(want.ID()))
	assert.Equal(t, want.Version(), got.Version())
	assert.Equal(t, want.Namespace().String(), got.Namespace().String())
	assert.Equal(t, want.OwnerRef().String(), got.OwnerRef().String())
	assert.Equal(t, want.IdempotencyKey().String(), got.IdempotencyKey().String())
	assert.Equal(t, want.OriginalName().String(), got.OriginalName().String())
	assert.Equal(t, want.DeclaredMime().String(), got.DeclaredMime().String())
	assert.Equal(t, want.MaxSize().Bytes(), got.MaxSize().Bytes())
	assert.Equal(t, want.Location().String(), got.Location().String())
	assert.True(t, got.IsPending())
	assert.True(t, got.FileID().IsZero())
	assert.True(t, want.ExpiresAt().Equal(got.ExpiresAt()))

	byKey, err := repo.ByIdempotencyKey(ctx, want.Namespace(), want.IdempotencyKey())
	require.NoError(t, err)
	assert.True(t, byKey.ID().Equal(want.ID()))
}

func TestUploads_OptionalDeclaredMime(t *testing.T) {
	t.Parallel()

	repo := NewUploads(sharedDB)
	ctx := t.Context()

	upload := testUpload(t)

	params := domain.NewUploadParams{
		ID:             upload.ID(),
		Namespace:      upload.Namespace(),
		OwnerRef:       upload.OwnerRef(),
		IdempotencyKey: upload.IdempotencyKey(),
		OriginalName:   upload.OriginalName(),
		MaxSize:        upload.MaxSize(),
		TTL:            time.Minute,
		Now:            time.Now(),
	}

	without, err := domain.NewUpload(params)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, &without))

	got, err := repo.ByID(ctx, without.ID())
	require.NoError(t, err)
	assert.True(t, got.DeclaredMime().IsZero(), "a null column must round trip as the zero value")
}

func TestUploads_DuplicateIdempotencyKey(t *testing.T) {
	t.Parallel()

	repo := NewUploads(sharedDB)
	ctx := t.Context()

	first := testUpload(t)
	require.NoError(t, repo.Create(ctx, &first))

	id, err := domain.NewUploadID()
	require.NoError(t, err)

	second, err := domain.NewUpload(domain.NewUploadParams{
		ID:             id,
		Namespace:      first.Namespace(),
		OwnerRef:       first.OwnerRef(),
		IdempotencyKey: first.IdempotencyKey(),
		OriginalName:   first.OriginalName(),
		MaxSize:        first.MaxSize(),
		TTL:            time.Minute,
		Now:            time.Now(),
	})
	require.NoError(t, err)

	err = repo.Create(ctx, &second)
	require.ErrorIs(t, err, domain.ErrUploadAlreadyExists,
		"the unique index is what makes CreateUpload idempotent under concurrency")
	assert.Contains(t, err.Error(), "uq_uploads_idem")
}

func TestUploads_NotFound(t *testing.T) {
	t.Parallel()

	repo := NewUploads(sharedDB)
	ctx := t.Context()

	id, err := domain.NewUploadID()
	require.NoError(t, err)

	_, err = repo.ByID(ctx, id)
	require.ErrorIs(t, err, domain.ErrUploadNotFound)

	namespace, err := domain.NewNamespace("avatar")
	require.NoError(t, err)

	key, err := domain.NewIdempotencyKey(uuid.NewString())
	require.NoError(t, err)

	_, err = repo.ByIdempotencyKey(ctx, namespace, key)
	require.ErrorIs(t, err, domain.ErrUploadNotFound)
}

func TestUploads_SaveBumpsVersion(t *testing.T) {
	t.Parallel()

	repo := NewUploads(sharedDB)
	ctx := t.Context()

	upload := testUpload(t)
	require.NoError(t, repo.Create(ctx, &upload))

	fileID, err := domain.NewFileID()
	require.NoError(t, err)
	insertFile(t, fileID)

	require.NoError(t, upload.Complete(fileID, time.Now()))
	require.NoError(t, repo.Save(ctx, &upload))

	assert.Equal(t, int64(2), upload.Version(),
		"the repository writes the new version back, so a second save in the same request works")

	stored, err := repo.ByID(ctx, upload.ID())
	require.NoError(t, err)
	assert.Equal(t, int64(2), stored.Version())
	assert.True(t, stored.Status().Equal(domain.UploadStatusUploaded))
	assert.True(t, stored.FileID().Equal(fileID))
}

func TestUploads_StaleVersionIsRejected(t *testing.T) {
	t.Parallel()

	repo := NewUploads(sharedDB)
	ctx := t.Context()

	upload := testUpload(t)
	require.NoError(t, repo.Create(ctx, &upload))

	winner, err := repo.ByID(ctx, upload.ID())
	require.NoError(t, err)
	require.NoError(t, winner.Expire(time.Now()))
	require.NoError(t, repo.Save(ctx, &winner))

	require.NoError(t, upload.Abort(time.Now()))

	err = repo.Save(ctx, &upload)
	require.ErrorIs(t, err, usecase.ErrStaleVersion,
		"the loser must not overwrite the winner's state")

	stored, err := repo.ByID(ctx, upload.ID())
	require.NoError(t, err)
	assert.True(t, stored.Status().Equal(domain.UploadStatusExpired))
}

func TestUploads_PendingExpiredBefore(t *testing.T) {
	t.Parallel()

	repo := NewUploads(sharedDB)
	ctx := t.Context()

	long := time.Now().Add(-2 * staleWindow)

	oldest := testUploadAt(t, long.Add(-time.Hour))
	require.NoError(t, repo.Create(ctx, &oldest))

	expired := testUploadAt(t, long)
	require.NoError(t, repo.Create(ctx, &expired))

	fresh := testUpload(t)
	require.NoError(t, repo.Create(ctx, &fresh))

	gone := testUploadAt(t, long)
	require.NoError(t, repo.Create(ctx, &gone))
	require.NoError(t, gone.Expire(time.Now()))
	require.NoError(t, repo.Save(ctx, &gone))

	before := time.Now().Add(-staleWindow)

	found, err := repo.PendingExpiredBefore(ctx, before, 100)
	require.NoError(t, err)

	ids := make(map[string]bool, len(found))
	for _, upload := range found {
		ids[upload.ID().String()] = true
		assert.True(t, upload.IsPending(), "only pending tickets are reconciled")
	}

	assert.True(t, ids[oldest.ID().String()])
	assert.True(t, ids[expired.ID().String()])
	assert.False(t, ids[gone.ID().String()], "a terminal ticket is already settled")
	assert.False(t, ids[fresh.ID().String()], "a ticket that has not expired yet can be used")

	position := make(map[string]int, len(found))
	for i, upload := range found {
		position[upload.ID().String()] = i
	}

	assert.Less(t, position[oldest.ID().String()], position[expired.ID().String()],
		"the sweep takes the longest expired first")

	limited, err := repo.PendingExpiredBefore(ctx, before, 1)
	require.NoError(t, err)
	assert.Len(t, limited, 1, "the limit caps what one sweep takes on")
}

func TestUploads_TransactionRollsBack(t *testing.T) {
	t.Parallel()

	repo := NewUploads(sharedDB)
	ctx := t.Context()

	upload := testUpload(t)
	boom := errors.New("business rule said no")

	err := sharedDB.InTx(ctx, func(ctx context.Context) error {
		if err := repo.Create(ctx, &upload); err != nil {
			return err
		}

		return boom
	})
	require.ErrorIs(t, err, boom)

	_, err = repo.ByID(ctx, upload.ID())
	require.ErrorIs(t, err, domain.ErrUploadNotFound,
		"a failed transaction must leave nothing behind")
}

func TestUploads_TransactionCommits(t *testing.T) {
	t.Parallel()

	repo := NewUploads(sharedDB)
	ctx := t.Context()

	upload := testUpload(t)

	require.NoError(t, sharedDB.InTx(ctx, func(ctx context.Context) error {
		return repo.Create(ctx, &upload)
	}))

	_, err := repo.ByID(ctx, upload.ID())
	require.NoError(t, err)
}

func TestSchema_StatusCheckRejectsGarbage(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	_, err := sharedDB.Pool().Exec(ctx, `
		INSERT INTO uploads (id, namespace, owner_ref, idempotency_key, original_name,
		                     staging_key, max_size_bytes, status, expires_at)
		VALUES ($1, 'avatar', 'user-1', $2, 'avatar.jpg', $3, 1024, 'garbage', now())`,
		uuid.New(), uuid.NewString(), "staging/"+uuid.NewString())

	require.Error(t, err, "a check constraint is the reason to keep varchar over a bare column")
	assert.Contains(t, err.Error(), "uploads_status_check")

	_, err = sharedDB.Pool().Exec(ctx, `
		INSERT INTO files (id, namespace, owner_ref, original_name, mime_type,
		                   size_bytes, storage_key, status)
		VALUES ($1, 'avatar', 'user-1', 'avatar.jpg', 'image/jpeg', 2048, $2, 'garbage')`,
		uuid.New(), "originals/"+uuid.NewString())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "files_status_check")
}

func TestSchema_VersionFitsInInteger(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	var dataType string

	require.NoError(t, sharedDB.Pool().QueryRow(ctx, `
		SELECT data_type FROM information_schema.columns
		 WHERE table_name = 'uploads' AND column_name = 'version'`).Scan(&dataType))

	assert.Equal(t, "integer", dataType,
		"a version counter advances once per aggregate write and never approaches 2.1 billion")
}

func TestUploads_OtherUniqueViolationsStayGeneric(t *testing.T) {
	t.Parallel()

	repo := NewUploads(sharedDB)
	ctx := t.Context()

	first := testUpload(t)
	require.NoError(t, repo.Create(ctx, &first))

	err := repo.Create(ctx, &first)
	require.Error(t, err)
	require.NotErrorIs(t, err, domain.ErrUploadAlreadyExists,
		"CreateUpload recovers from a duplicate by re-reading the idempotency key; "+
			"reporting a primary key clash the same way would send it looking for a row that is not there")
	assert.Contains(t, err.Error(), "uploads_pkey")
}
