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

func TestApplyFileProcessed_RetriesALostRaceOnTheAvatar(t *testing.T) {
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
	require.ErrorIs(t, err, ErrStaleVersion,
		"a conflict means the fact was not applied, so the consumer has to deliver it again")
	assert.False(t, got.Skipped,
		"reporting it as skipped would ack the message and lose the fact for good")
}

func TestApplyFileProcessed_RetriesALostRaceOnTheProfile(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newFactsHarness(t, testProcessingAvatar(t, testUserID(t, "user-1")), false)
	h.profiles.saveErr = ErrStaleVersion

	// Act
	got, err := h.processed.Handle(context.Background(), FileProcessedFact{
		FileID:   h.avatar.FileID(),
		Snapshot: testSnapshot(t),
		Variants: testVariants(t),
	})

	// Assert
	require.ErrorIs(t, err, ErrStaleVersion,
		"a change to the profile for an unrelated reason must not cost the avatar its variants")
	assert.False(t, got.Skipped)
	assert.Empty(t, h.avatars.saved,
		"nothing is written, and the retry reads both rows again")
}

func TestApplyFileRejected_RetriesALostRace(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newFactsHarness(t, testProcessingAvatar(t, testUserID(t, "user-1")), false)
	h.avatars.saveErr = ErrStaleVersion

	// Act
	got, err := h.rejected.Handle(context.Background(), FileRejectedFact{
		FileID: h.avatar.FileID(),
		Reason: "too_large",
	})

	// Assert
	require.ErrorIs(t, err, ErrStaleVersion,
		"the user has to learn why the upload failed, so the fact cannot be dropped")
	assert.False(t, got.Skipped)
}

func TestApplyFileDeleted_RetriesALostRace(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newFactsHarness(t, testProcessingAvatar(t, testUserID(t, "user-1")), true)
	h.avatars.saveErr = ErrStaleVersion

	// Act
	got, err := h.deleted.Handle(context.Background(), FileDeletedFact{
		FileID: h.avatar.FileID(),
	})

	// Assert
	require.ErrorIs(t, err, ErrStaleVersion,
		"the objects are already gone, so the row has to follow them even after a conflict")
	assert.False(t, got.Skipped)
}

func testAvatarAt(t *testing.T, user domain.UserID, created time.Time) domain.Avatar {
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

	require.NoError(t, avatar.StartProcessing(testFileRef(t), created))

	return avatar
}

func newOrderingHarness(t *testing.T, older, newer domain.Avatar) factsHarness {
	t.Helper()

	profile, err := domain.NewProfile(older.UserID(), testNow)
	require.NoError(t, err)

	require.NoError(t, newer.MarkReady(testSnapshot(t), testVariants(t), newer.CreatedAt()))
	require.NoError(t, profile.SetCurrentAvatar(newer.ID(), newer.CreatedAt()))

	avatars := &stubAvatars{
		avatars: map[string]domain.Avatar{
			older.ID().String(): older,
			newer.ID().String(): newer,
		},
		byFile: map[string]domain.Avatar{
			older.FileID().String(): older,
			newer.FileID().String(): newer,
		},
	}

	profiles := &stubProfiles{
		profiles: map[string]domain.Profile{older.UserID().String(): profile},
	}
	tx := &stubTransactor{}
	now := fixedClock{now: testNow.Add(2 * time.Hour)}

	return factsHarness{
		processed: NewApplyFileProcessedHandler(avatars, profiles, tx, now),
		avatars:   avatars,
		profiles:  profiles,
		tx:        tx,
		avatar:    older,
	}
}

func TestApplyFileProcessed_KeepsTheNewerAvatarCurrent(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")
	older := testAvatarAt(t, user, testNow)
	newer := testAvatarAt(t, user, testNow.Add(time.Hour))

	h := newOrderingHarness(t, older, newer)

	// Act
	got, err := h.processed.Handle(context.Background(), FileProcessedFact{
		FileID:   older.FileID(),
		Snapshot: testSnapshot(t),
		Variants: testVariants(t),
	})

	// Assert
	require.NoError(t, err)
	assert.False(t, got.Promoted,
		"the facts are keyed by file, so the older upload can be applied last")
	assert.True(t, got.Status.Equal(domain.AvatarStatusReady),
		"it still becomes ready, it just does not become current")

	assert.Empty(t, h.profiles.saved,
		"nothing about the profile changed, so it must not take a version bump either")
	require.Len(t, h.avatars.saved, 1)
	assert.True(t, h.avatars.saved[0].ID().Equal(older.ID()))
}

func TestApplyFileProcessed_PromotesANewerAvatarOverTheCurrentOne(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")
	older := testAvatarAt(t, user, testNow)
	newer := testAvatarAt(t, user, testNow.Add(time.Hour))

	h := newOrderingHarness(t, newer, older)

	// Act
	got, err := h.processed.Handle(context.Background(), FileProcessedFact{
		FileID:   newer.FileID(),
		Snapshot: testSnapshot(t),
		Variants: testVariants(t),
	})

	// Assert
	require.NoError(t, err)
	assert.True(t, got.Promoted)

	require.Len(t, h.profiles.saved, 1)
	assert.True(t, h.profiles.saved[0].CurrentAvatarID().Equal(newer.ID()),
		"the newest ready avatar is the one the gallery and the redirect agree on")
}

func TestApplyFileProcessed_PromotesWhenTheProfileHasNoCurrentAvatar(t *testing.T) {
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
	assert.True(t, got.Promoted)
	require.Len(t, h.profiles.saved, 1)
	assert.True(t, h.profiles.saved[0].CurrentAvatarID().Equal(h.avatar.ID()))
}

func TestApplyFileProcessed_PromotesOverADeletedCurrentAvatar(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")
	older := testAvatarAt(t, user, testNow)
	newer := testAvatarAt(t, user, testNow.Add(time.Hour))

	h := newOrderingHarness(t, older, newer)

	gone := h.avatars.avatars[newer.ID().String()]
	require.NoError(t, gone.MarkDeleted(testNow.Add(90*time.Minute)))
	h.avatars.avatars[newer.ID().String()] = gone

	// Act
	got, err := h.processed.Handle(context.Background(), FileProcessedFact{
		FileID:   older.FileID(),
		Snapshot: testSnapshot(t),
		Variants: testVariants(t),
	})

	// Assert
	require.NoError(t, err)
	assert.True(t, got.Promoted,
		"a deleted avatar is not a reason to keep the user on the placeholder")
	require.Len(t, h.profiles.saved, 1)
	assert.True(t, h.profiles.saved[0].CurrentAvatarID().Equal(older.ID()))
}
