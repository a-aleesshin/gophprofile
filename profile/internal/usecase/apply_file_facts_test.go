package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type factsHarness struct {
	processed *ApplyFileProcessedHandler
	rejected  *ApplyFileRejectedHandler
	deleted   *ApplyFileDeletedHandler
	avatars   *stubAvatars
	profiles  *stubProfiles
	tx        *stubTransactor
	avatar    domain.Avatar
}

func newFactsHarness(t *testing.T, avatar domain.Avatar, current bool) factsHarness {
	t.Helper()

	profile, err := domain.NewProfile(avatar.UserID(), testNow)
	require.NoError(t, err)

	if current {
		require.NoError(t, profile.SetCurrentAvatar(avatar.ID(), testNow))
	}

	avatars := &stubAvatars{
		avatars: map[string]domain.Avatar{avatar.ID().String(): avatar},
		byFile:  map[string]domain.Avatar{},
	}

	if !avatar.FileID().IsZero() {
		avatars.byFile[avatar.FileID().String()] = avatar
	}

	profiles := &stubProfiles{
		profiles: map[string]domain.Profile{avatar.UserID().String(): profile},
	}
	tx := &stubTransactor{}
	now := fixedClock{now: testNow.Add(time.Minute)}

	return factsHarness{
		processed: NewApplyFileProcessedHandler(avatars, profiles, tx, now),
		rejected:  NewApplyFileRejectedHandler(avatars, tx, now),
		deleted:   NewApplyFileDeletedHandler(avatars, profiles, tx, now),
		avatars:   avatars,
		profiles:  profiles,
		tx:        tx,
		avatar:    avatar,
	}
}

func TestApplyFileProcessed_MakesTheAvatarReadyAndCurrent(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newFactsHarness(t, testProcessingAvatar(t, testUserID(t, "user-1")), false)

	// Act
	got, err := h.processed.Handle(context.Background(), FileProcessedFact{
		FileID:   h.avatar.FileID(),
		Snapshot: testSnapshot(t),
		Variants: testVariants(t),
	})

	// Assert
	require.NoError(t, err)
	assert.False(t, got.Skipped)
	assert.True(t, got.Status.Equal(domain.AvatarStatusReady))

	require.Len(t, h.avatars.saved, 1)
	assert.Len(t, h.avatars.saved[0].Variants(), 1,
		"the variants are denormalised here so the gallery needs one query to our own base")

	require.Len(t, h.profiles.saved, 1)
	assert.True(t, h.profiles.saved[0].CurrentAvatarID().Equal(h.avatar.ID()),
		"a freshly ready avatar becomes the current one")
}

func TestApplyFileProcessed_IgnoresAFileThatIsNotOurs(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newFactsHarness(t, testProcessingAvatar(t, testUserID(t, "user-1")), false)

	// Act
	got, err := h.processed.Handle(context.Background(), FileProcessedFact{
		FileID:   testFileRef(t),
		Snapshot: testSnapshot(t),
		Variants: testVariants(t),
	})

	// Assert
	require.NoError(t, err,
		"the topic carries every file of the platform, most of them are not avatars")
	assert.True(t, got.Skipped)
	assert.Empty(t, h.avatars.saved)
}

func TestApplyFileProcessed_AcceptsThatTheUserDeletedItFirst(t *testing.T) {
	t.Parallel()

	// Arrange
	avatar := testProcessingAvatar(t, testUserID(t, "user-1"))
	fileID := avatar.FileID()

	require.NoError(t, avatar.MarkDeleted(testNow))

	h := newFactsHarness(t, avatar, false)
	h.avatars.byFile[fileID.String()] = avatar

	// Act
	got, err := h.processed.Handle(context.Background(), FileProcessedFact{
		FileID:   fileID,
		Snapshot: testSnapshot(t),
		Variants: testVariants(t),
	})

	// Assert
	require.NoError(t, err,
		"the transition table settles the race, and a refused ready is not an error")
	assert.True(t, got.Skipped)
	assert.True(t, got.Status.Equal(domain.AvatarStatusDeleted))
	assert.Empty(t, h.avatars.saved)
	assert.Empty(t, h.profiles.saved, "a deleted avatar must never become the current one")
}

func TestApplyFileRejected_CarriesTheReasonToTheUser(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newFactsHarness(t, testProcessingAvatar(t, testUserID(t, "user-1")), false)

	// Act
	got, err := h.rejected.Handle(context.Background(), FileRejectedFact{
		FileID: h.avatar.FileID(),
		Reason: "decode_failed",
	})

	// Assert
	require.NoError(t, err)
	assert.True(t, got.Status.Equal(domain.AvatarStatusFailed))

	require.Len(t, h.avatars.saved, 1)
	assert.Equal(t, "decode_failed", h.avatars.saved[0].FailedReason())
}

func TestApplyFileDeleted_ClearsTheCurrentPointer(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newFactsHarness(t, testProcessingAvatar(t, testUserID(t, "user-1")), true)

	// Act
	got, err := h.deleted.Handle(context.Background(), FileDeletedFact{
		FileID: h.avatar.FileID(),
	})

	// Assert
	require.NoError(t, err)
	assert.True(t, got.Status.Equal(domain.AvatarStatusDeleted))

	require.Len(t, h.profiles.saved, 1)
	assert.True(t, h.profiles.saved[0].CurrentAvatarID().IsZero())
}

func TestApplyFileDeleted_IsIdempotent(t *testing.T) {
	t.Parallel()

	// Arrange
	avatar := testProcessingAvatar(t, testUserID(t, "user-1"))
	fileID := avatar.FileID()

	require.NoError(t, avatar.MarkDeleted(testNow))

	h := newFactsHarness(t, avatar, false)
	h.avatars.byFile[fileID.String()] = avatar

	// Act
	got, err := h.deleted.Handle(context.Background(), FileDeletedFact{FileID: fileID})

	// Assert
	require.NoError(t, err)
	assert.True(t, got.Skipped)
	assert.Empty(t, h.avatars.saved)
	assert.Zero(t, h.tx.calls)
}

func TestApplyFactsRejectAnEmptyFact(t *testing.T) {
	t.Parallel()

	h := newFactsHarness(t, testProcessingAvatar(t, testUserID(t, "user-1")), false)

	// Act
	_, noFileProcessed := h.processed.Handle(context.Background(), FileProcessedFact{
		Snapshot: testSnapshot(t), Variants: testVariants(t),
	})

	_, noSnapshot := h.processed.Handle(context.Background(), FileProcessedFact{
		FileID: h.avatar.FileID(), Variants: testVariants(t),
	})

	_, noVariants := h.processed.Handle(context.Background(), FileProcessedFact{
		FileID: h.avatar.FileID(), Snapshot: testSnapshot(t),
	})

	_, noReason := h.rejected.Handle(context.Background(), FileRejectedFact{
		FileID: h.avatar.FileID(),
	})

	_, noFileDeleted := h.deleted.Handle(context.Background(), FileDeletedFact{})

	// Assert
	for name, err := range map[string]error{
		"no file in processed": noFileProcessed,
		"no snapshot":          noSnapshot,
		"no variants":          noVariants,
		"no reason":            noReason,
		"no file in deleted":   noFileDeleted,
	} {
		require.ErrorIs(t, err, ErrInvalidCommand, name)
	}
}

func TestApplyFileProcessed_SkipsALostRaceOnSave(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newFactsHarness(t, testProcessingAvatar(t, testUserID(t, "user-1")), false)
	h.avatars.saveErr = ErrStaleVersion

	// Act
	got, err := h.processed.Handle(context.Background(), FileProcessedFact{
		FileID:   h.avatar.FileID(),
		Snapshot: testSnapshot(t),
		Variants: testVariants(t),
	})

	// Assert
	require.NoError(t, err,
		"somebody else moved the row, and the fact will be redelivered if it still matters")
	assert.True(t, got.Skipped)
}
