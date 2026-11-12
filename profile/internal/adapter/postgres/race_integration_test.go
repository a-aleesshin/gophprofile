//go:build integration

package postgres

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/internal/clock"
	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

// Running two goroutines and hoping they collide produces a test that passes
// for the wrong reason: the second request almost always reads the profile the
// first one already committed and never tries to insert. This wrapper makes the
// collision certain — the row appears between the handler's read and its
// insert, which is exactly the window the fix is about.
type racingProfiles struct {
	*Profiles

	winner domain.Profile
	once   sync.Once
}

func (r *racingProfiles) ByUserID(
	ctx context.Context,
	userID domain.UserID,
) (domain.Profile, error) {
	profile, err := r.Profiles.ByUserID(ctx, userID)
	if err == nil {
		return profile, nil
	}

	r.once.Do(func() { _ = r.Profiles.Create(ctx, &r.winner) })

	return domain.Profile{}, err
}

func newRacingProfiles(t *testing.T, user domain.UserID) *racingProfiles {
	t.Helper()

	winner, err := domain.NewProfile(user, time.Now())
	require.NoError(t, err)

	return &racingProfiles{Profiles: NewProfiles(sharedDB), winner: winner}
}

func TestBindEmailKeepsTheEmailAfterLosingTheInsert(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t)
	email := testEmail(t)
	profiles := newRacingProfiles(t, user)

	handler := usecase.NewBindEmailHandler(profiles, sharedDB, clock.System{})

	// Act
	got, err := handler.Handle(t.Context(), usecase.BindEmailCommand{
		UserID: user, Email: email,
	})

	// Assert
	require.NoError(t, err, "the profile exists now, which is what the caller wanted")
	assert.Equal(t, email.Hash().String(), got.EmailHash.String())

	stored, err := NewProfiles(sharedDB).ByUserID(t.Context(), user)
	require.NoError(t, err)
	assert.Equal(t, email.String(), stored.Email().String(),
		"losing the insert must not drop the email on the floor")

	byHash, err := NewProfiles(sharedDB).ByEmailHash(t.Context(), email.Hash())
	require.NoError(t, err)
	assert.Equal(t, user.String(), byHash.UserID().String(),
		"and the public lookup must find it")
}

func TestStartUploadProceedsAfterLosingTheInsert(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t)
	profiles := newRacingProfiles(t, user)
	avatars := NewAvatars(sharedDB)

	handler := usecase.NewStartUploadHandler(
		fixedPolicy{}, profiles, avatars, stubTickets{}, sharedDB, clock.System{})

	// Act
	got, err := handler.Handle(t.Context(), usecase.StartUploadCommand{
		UserID: user, FileName: "avatar.jpg", MimeType: "image/jpeg", SizeBytes: 2048,
	})

	// Assert
	require.NoError(t, err,
		"an upload must not fail because something else created the profile first")
	assert.False(t, got.AvatarID.IsZero())

	count, err := avatars.CountByUser(t.Context(), user)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "the avatar is inserted in the same transaction and survives")
}

type fixedPolicy struct{}

func (fixedPolicy) UploadPolicy() usecase.UploadPolicy {
	return usecase.UploadPolicy{
		MaxSizeBytes:      10 << 20,
		AllowedMime:       []string{"image/jpeg"},
		MaxAvatarsPerUser: 20,
	}
}

type stubTickets struct{}

func (stubTickets) CreateUpload(
	_ context.Context,
	params usecase.CreateUploadParams,
) (usecase.CreateUploadTicket, error) {
	id, err := domain.NewAvatarID()
	if err != nil {
		return usecase.CreateUploadTicket{}, err
	}

	return usecase.CreateUploadTicket{
		UploadID:  id.String(),
		URL:       "https://domain.com/s3/gophprofile/",
		Fields:    map[string]string{"key": "staging/" + params.IdempotencyKey},
		ExpiresAt: time.Now().Add(time.Minute),
	}, nil
}
