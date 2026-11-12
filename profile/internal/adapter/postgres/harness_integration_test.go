//go:build integration

package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/a-aleesshin/gophprofile/internal/pgtx"
	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
	"github.com/a-aleesshin/gophprofile/profile/migrations"
)

const testImage = "postgres:17-alpine"

var sharedDB *pgtx.DB

func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, testImage,
		tcpostgres.WithDatabase("profile"),
		tcpostgres.WithUsername("profile"),
		tcpostgres.WithPassword("profile"),
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

func testUserID(t *testing.T) domain.UserID {
	t.Helper()

	value, err := domain.NewUserID("user-" + uuid.NewString())
	require.NoError(t, err)

	return value
}

func testEmail(t *testing.T) domain.Email {
	t.Helper()

	value, err := domain.NewEmail(uuid.NewString() + "@example.com")
	require.NoError(t, err)

	return value
}

func testFileRef(t *testing.T) domain.FileRef {
	t.Helper()

	value, err := domain.ParseFileRef(uuid.Must(uuid.NewV7()).String())
	require.NoError(t, err)

	return value
}

func testUploadRef(t *testing.T) domain.UploadRef {
	t.Helper()

	value, err := domain.ParseUploadRef(uuid.Must(uuid.NewV7()).String())
	require.NoError(t, err)

	return value
}

func testSnapshot(t *testing.T) domain.FileSnapshot {
	t.Helper()

	snapshot, err := domain.NewFileSnapshot(domain.NewFileSnapshotParams{
		FileName:  "avatar.jpg",
		MimeType:  "image/jpeg",
		SizeBytes: 2048,
		Width:     1920,
		Height:    1080,
	})
	require.NoError(t, err)

	return snapshot
}

func testVariants(t *testing.T) []domain.Variant {
	t.Helper()

	specs := []domain.NewVariantParams{
		{Name: "100x100", Ext: "jpg", MimeType: "image/jpeg", Width: 100, Height: 100},
		{Name: "300x300", Ext: "jpg", MimeType: "image/jpeg", Width: 300, Height: 300},
	}

	variants := make([]domain.Variant, 0, len(specs))

	for _, spec := range specs {
		variant, err := domain.NewVariant(spec)
		require.NoError(t, err)

		variants = append(variants, variant)
	}

	return variants
}

func newProfile(t *testing.T) domain.Profile {
	t.Helper()

	profile, err := domain.NewProfile(testUserID(t), time.Now())
	require.NoError(t, err)

	require.NoError(t, NewProfiles(sharedDB).Create(t.Context(), &profile))

	return profile
}

func newUser(t *testing.T) domain.UserID {
	t.Helper()

	profile := newProfile(t)

	return profile.UserID()
}

func newPendingAvatar(t *testing.T, user domain.UserID) domain.Avatar {
	t.Helper()

	return newPendingAvatarAt(t, user, time.Now())
}

func newPendingAvatarAt(t *testing.T, user domain.UserID, created time.Time) domain.Avatar {
	t.Helper()

	id, err := domain.NewAvatarID()
	require.NoError(t, err)

	avatar, err := domain.NewAvatar(domain.NewAvatarParams{
		ID:       id,
		UserID:   user,
		UploadID: testUploadRef(t),
		Now:      created,
	})
	require.NoError(t, err)

	require.NoError(t, NewAvatars(sharedDB).Create(t.Context(), &avatar))

	return avatar
}

func newReadyAvatar(t *testing.T, user domain.UserID) domain.Avatar {
	t.Helper()

	avatars := NewAvatars(sharedDB)
	avatar := newPendingAvatar(t, user)

	require.NoError(t, avatar.StartProcessing(testFileRef(t), time.Now()))
	require.NoError(t, avatars.Save(t.Context(), &avatar))

	require.NoError(t, avatar.MarkReady(testSnapshot(t), testVariants(t), time.Now()))
	require.NoError(t, avatars.Save(t.Context(), &avatar))

	return avatar
}
