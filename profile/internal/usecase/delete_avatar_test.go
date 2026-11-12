package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type deleteAvatarHarness struct {
	handler   *DeleteAvatarHandler
	avatars   *stubAvatars
	profiles  *stubProfiles
	publisher *stubFactPublisher
	tx        *stubTransactor
	avatar    domain.Avatar
	profile   domain.Profile
}

func newDeleteAvatarHarness(
	t *testing.T,
	avatar domain.Avatar,
	current bool,
) deleteAvatarHarness {
	t.Helper()

	profile, err := domain.NewProfile(avatar.UserID(), testNow)
	require.NoError(t, err)

	if current {
		require.NoError(t, profile.SetCurrentAvatar(avatar.ID(), testNow))
	}

	avatars := &stubAvatars{avatars: map[string]domain.Avatar{avatar.ID().String(): avatar}}
	profiles := &stubProfiles{
		profiles: map[string]domain.Profile{avatar.UserID().String(): profile},
	}
	tx := &stubTransactor{}
	publisher := &stubFactPublisher{keeper: tx}

	return deleteAvatarHarness{
		handler: NewDeleteAvatarHandler(
			avatars, profiles, publisher, tx, fixedClock{now: testNow}),
		avatars:   avatars,
		profiles:  profiles,
		publisher: publisher,
		tx:        tx,
		avatar:    avatar,
		profile:   profile,
	}
}

func testProcessingAvatar(t *testing.T, user domain.UserID) domain.Avatar {
	t.Helper()

	avatar := testPendingAvatar(t, user)
	require.NoError(t, avatar.StartProcessing(testFileRef(t), testNow))

	return avatar
}

func TestDeleteAvatar_PublishesTheFactInsideTheTransaction(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")
	h := newDeleteAvatarHarness(t, testProcessingAvatar(t, user), true)

	// Act
	got, err := h.handler.Handle(context.Background(), DeleteAvatarCommand{
		AvatarID: h.avatar.ID(),
		UserID:   user,
	})

	// Assert
	require.NoError(t, err)
	assert.True(t, got.Status.Equal(domain.AvatarStatusDeleted))

	require.Len(t, h.publisher.facts, 1)
	assert.True(t, h.publisher.inTx,
		"the row and the fact must commit together, or file never hears about the deletion")
	assert.True(t, h.publisher.facts[0].FileID.Equal(h.avatar.FileID()))

	require.Len(t, h.profiles.saved, 1)
	assert.True(t, h.profiles.saved[0].CurrentAvatarID().IsZero(),
		"deleting the current avatar must not leave a pointer to a deleted row")
}

func TestDeleteAvatar_LeavesAnotherCurrentAvatarAlone(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")
	h := newDeleteAvatarHarness(t, testProcessingAvatar(t, user), false)

	other, err := domain.NewAvatarID()
	require.NoError(t, err)

	profile := h.profile
	require.NoError(t, profile.SetCurrentAvatar(other, testNow))
	h.profiles.profiles[user.String()] = profile

	// Act
	_, err = h.handler.Handle(context.Background(), DeleteAvatarCommand{
		AvatarID: h.avatar.ID(),
		UserID:   user,
	})

	// Assert
	require.NoError(t, err)
	assert.Empty(t, h.profiles.saved, "the profile points elsewhere, there is nothing to clear")

	untouched := h.profiles.profiles[user.String()]
	assert.True(t, untouched.CurrentAvatarID().Equal(other))
}

func TestDeleteAvatar_SaysNothingToFileAboutAPendingAvatar(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")
	h := newDeleteAvatarHarness(t, testPendingAvatar(t, user), false)

	// Act
	got, err := h.handler.Handle(context.Background(), DeleteAvatarCommand{
		AvatarID: h.avatar.ID(),
		UserID:   user,
	})

	// Assert
	require.NoError(t, err)
	assert.True(t, got.Status.Equal(domain.AvatarStatusDeleted))
	assert.Empty(t, h.publisher.facts,
		"the upload never completed, so there is no file to delete on the other side")
	require.Len(t, h.avatars.saved, 1)
}

func TestDeleteAvatar_IsIdempotent(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")

	avatar := testProcessingAvatar(t, user)
	require.NoError(t, avatar.MarkDeleted(testNow))

	h := newDeleteAvatarHarness(t, avatar, false)

	// Act
	got, err := h.handler.Handle(context.Background(), DeleteAvatarCommand{
		AvatarID: avatar.ID(),
		UserID:   user,
	})

	// Assert
	require.NoError(t, err)
	assert.True(t, got.Status.Equal(domain.AvatarStatusDeleted))
	assert.Empty(t, h.avatars.saved)
	assert.Empty(t, h.publisher.facts, "a second delete must not publish a second fact")
	assert.Zero(t, h.tx.calls)
}

func TestDeleteAvatar_RefusesSomebodyElsesAvatar(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newDeleteAvatarHarness(t, testProcessingAvatar(t, testUserID(t, "user-1")), false)

	// Act
	_, err := h.handler.Handle(context.Background(), DeleteAvatarCommand{
		AvatarID: h.avatar.ID(),
		UserID:   testUserID(t, "user-2"),
	})

	// Assert
	require.ErrorIs(t, err, ErrForbidden)
	assert.Empty(t, h.avatars.saved)
	assert.Empty(t, h.publisher.facts)
}

func TestDeleteAvatar_SurvivesALostRace(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")
	h := newDeleteAvatarHarness(t, testProcessingAvatar(t, user), false)
	h.avatars.saveErr = ErrStaleVersion

	winner := h.avatar
	require.NoError(t, winner.MarkDeleted(testNow))
	h.avatars.avatars[winner.ID().String()] = winner

	// Act
	got, err := h.handler.Handle(context.Background(), DeleteAvatarCommand{
		AvatarID: h.avatar.ID(),
		UserID:   user,
	})

	// Assert
	require.NoError(t, err)
	assert.True(t, got.Status.Equal(domain.AvatarStatusDeleted))
}
