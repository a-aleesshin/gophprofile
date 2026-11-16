package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

func newRacedDeleteHarness(
	t *testing.T, staleSaves int,
) (*DeleteAvatarHandler, *stubAvatars, domain.Avatar) {
	t.Helper()

	user := testUserID(t, "user-1")
	avatar := testProcessingAvatar(t, user)

	profile, err := domain.NewProfile(user, testNow)
	require.NoError(t, err)

	avatars := &stubAvatars{
		avatars:    map[string]domain.Avatar{avatar.ID().String(): avatar},
		staleSaves: staleSaves,
	}

	profiles := &stubProfiles{profiles: map[string]domain.Profile{user.String(): profile}}

	handler := NewDeleteAvatarHandler(avatars, profiles,
		&stubFactPublisher{}, &stubTransactor{}, fixedClock{now: testNow})

	return handler, avatars, avatar
}

func TestDeleteAvatar_RetriesAfterLosingTheRace(t *testing.T) {
	t.Parallel()

	// Arrange: the first save loses to a fact that arrived in the same moment.
	handler, avatars, avatar := newRacedDeleteHarness(t, 1)

	// Act
	got, err := handler.Handle(context.Background(), DeleteAvatarCommand{
		AvatarID: avatar.ID(), UserID: avatar.UserID(),
	})

	// Assert
	require.NoError(t, err)
	assert.True(t, got.Status.Equal(domain.AvatarStatusDeleted),
		"the caller is told the avatar is gone only when it really is")
	assert.Len(t, avatars.saved, 2, "the first attempt rolled back, the second one wrote")
}

func TestDeleteAvatar_RefusesToClaimSuccessItDidNotAchieve(t *testing.T) {
	t.Parallel()

	// Arrange: every attempt loses.
	handler, avatars, avatar := newRacedDeleteHarness(t, deleteAvatarAttempts)

	// Act
	_, err := handler.Handle(context.Background(), DeleteAvatarCommand{
		AvatarID: avatar.ID(), UserID: avatar.UserID(),
	})

	// Assert
	require.ErrorIs(t, err, ErrStaleVersion,
		"answering 204 on a live avatar is worse than asking the client to retry")
	assert.Len(t, avatars.saved, deleteAvatarAttempts)
}

func TestDeleteAvatar_StopsWhenSomebodyElseDeletedIt(t *testing.T) {
	t.Parallel()

	// Arrange: the race is lost because a parallel delete got there first.
	handler, avatars, avatar := newRacedDeleteHarness(t, 1)

	avatars.onStale = func() {
		deleted := avatars.avatars[avatar.ID().String()]
		require.NoError(t, deleted.MarkDeleted(testNow))
		avatars.avatars[avatar.ID().String()] = deleted
	}

	// Act
	got, err := handler.Handle(context.Background(), DeleteAvatarCommand{
		AvatarID: avatar.ID(), UserID: avatar.UserID(),
	})

	// Assert
	require.NoError(t, err, "delete is idempotent, and the avatar is gone either way")
	assert.True(t, got.Status.Equal(domain.AvatarStatusDeleted))
	assert.Len(t, avatars.saved, 1, "the retry saw it was already deleted and wrote nothing")
}
