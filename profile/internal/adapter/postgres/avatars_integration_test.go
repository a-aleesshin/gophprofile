//go:build integration

package postgres

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

func TestAvatarsRoundTripAPendingAvatar(t *testing.T) {
	t.Parallel()

	// Arrange
	avatars := NewAvatars(sharedDB)
	created := newPendingAvatar(t, newUser(t))

	// Act
	loaded, err := avatars.ByID(t.Context(), created.ID())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, int64(1), loaded.Version())
	assert.True(t, loaded.Status().Equal(domain.AvatarStatusPending))
	assert.Equal(t, created.UploadID().String(), loaded.UploadID().String())
	assert.True(t, loaded.FileID().IsZero(), "a pending avatar has no file yet")
	assert.True(t, loaded.Snapshot().IsZero())
	assert.Empty(t, loaded.Variants())
	assert.False(t, loaded.IsDeleted())
}

func TestAvatarsRoundTripTheWholeLifecycle(t *testing.T) {
	t.Parallel()

	// Arrange
	avatars := NewAvatars(sharedDB)
	ready := newReadyAvatar(t, newUser(t))

	// Act
	loaded, err := avatars.ByID(t.Context(), ready.ID())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, int64(3), loaded.Version(), "create, start processing, mark ready")
	assert.True(t, loaded.Status().Equal(domain.AvatarStatusReady))
	assert.Equal(t, ready.FileID().String(), loaded.FileID().String())

	assert.Equal(t, "avatar.jpg", loaded.Snapshot().FileName())
	assert.Equal(t, "image/jpeg", loaded.Snapshot().MimeType())
	assert.Equal(t, int64(2048), loaded.Snapshot().SizeBytes())
	assert.Equal(t, int32(1920), loaded.Snapshot().Width())

	require.Len(t, loaded.Variants(), 2)

	variant, found := loaded.VariantByName("300x300")
	require.True(t, found, "jsonb must survive the round trip by name, not by position")
	assert.Equal(t, "jpg", variant.Ext())
	assert.Equal(t, "image/jpeg", variant.MimeType())
	assert.Equal(t, int32(300), variant.Width())
	assert.Equal(t, int32(300), variant.Height())
}

func TestAvatarsFindAnAvatarByItsFile(t *testing.T) {
	t.Parallel()

	// Arrange
	avatars := NewAvatars(sharedDB)
	ready := newReadyAvatar(t, newUser(t))

	// Act
	loaded, err := avatars.ByFileID(t.Context(), ready.FileID())

	// Assert
	require.NoError(t, err)
	assert.True(t, loaded.ID().Equal(ready.ID()),
		"this is how a kafka fact about a file finds its avatar")

	_, err = avatars.ByFileID(t.Context(), testFileRef(t))
	require.ErrorIs(t, err, domain.ErrAvatarNotFound)
}

func TestAvatarsRefuseASecondAvatarForTheSameUpload(t *testing.T) {
	t.Parallel()

	// Arrange
	avatars := NewAvatars(sharedDB)
	user := newUser(t)
	first := newPendingAvatar(t, user)

	id, err := domain.NewAvatarID()
	require.NoError(t, err)

	duplicate, err := domain.NewAvatar(domain.NewAvatarParams{
		ID:       id,
		UserID:   user,
		UploadID: first.UploadID(),
		Now:      time.Now(),
	})
	require.NoError(t, err)

	// Act
	err = avatars.Create(t.Context(), &duplicate)

	// Assert
	require.ErrorIs(t, err, domain.ErrAvatarExists,
		"a retried start-upload must not produce two avatars for one ticket")
}

func TestAvatarsDetectAStaleVersion(t *testing.T) {
	t.Parallel()

	// Arrange
	avatars := NewAvatars(sharedDB)
	avatar := newPendingAvatar(t, newUser(t))

	stale := avatar

	require.NoError(t, avatar.StartProcessing(testFileRef(t), time.Now()))
	require.NoError(t, avatars.Save(t.Context(), &avatar))

	// Act
	require.NoError(t, stale.MarkFailed("upload expired", time.Now()))
	err := avatars.Save(t.Context(), &stale)

	// Assert
	require.ErrorIs(t, err, usecase.ErrStaleVersion)

	current, err := avatars.ByID(t.Context(), avatar.ID())
	require.NoError(t, err)
	assert.True(t, current.Status().Equal(domain.AvatarStatusProcessing),
		"the loser of the race must not overwrite the winner")
}

func TestAvatarsCountOnlyWhatTheUserStillHas(t *testing.T) {
	t.Parallel()

	// Arrange
	avatars := NewAvatars(sharedDB)
	user := newUser(t)

	newPendingAvatar(t, user)
	doomed := newReadyAvatar(t, user)

	before, err := avatars.CountByUser(t.Context(), user)
	require.NoError(t, err)

	// Act
	require.NoError(t, doomed.MarkDeleted(time.Now()))
	require.NoError(t, avatars.Save(t.Context(), &doomed))

	after, err := avatars.CountByUser(t.Context(), user)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, 2, before)
	assert.Equal(t, 1, after,
		"a deleted avatar must not count against the per-user limit forever")

	deleted, err := avatars.ByID(t.Context(), doomed.ID())
	require.NoError(t, err)
	assert.True(t, deleted.IsDeleted())
	assert.True(t, deleted.Status().Equal(domain.AvatarStatusDeleted))
}

func TestAvatarsSelectWhatTheWatchdogShouldLookAt(t *testing.T) {
	t.Parallel()

	// Arrange
	avatars := NewAvatars(sharedDB)
	user := newUser(t)

	stuck := newPendingAvatar(t, user)
	require.NoError(t, stuck.StartProcessing(testFileRef(t), time.Now().Add(-time.Hour)))
	require.NoError(t, avatars.Save(t.Context(), &stuck))

	fresh := newPendingAvatar(t, user)
	done := newReadyAvatar(t, user)

	// Act
	stale, err := avatars.StaleByStatus(t.Context(),
		[]domain.AvatarStatus{domain.AvatarStatusPending, domain.AvatarStatusProcessing},
		time.Now().Add(-30*time.Minute), 50)

	// Assert
	require.NoError(t, err)

	found := make(map[string]bool, len(stale))
	for _, avatar := range stale {
		found[avatar.ID().String()] = true
	}

	assert.True(t, found[stuck.ID().String()])
	assert.False(t, found[fresh.ID().String()], "a recent upload is still allowed to finish")
	assert.False(t, found[done.ID().String()])
}
