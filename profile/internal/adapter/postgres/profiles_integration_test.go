//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

func TestProfilesRoundTripAnEmptyProfile(t *testing.T) {
	t.Parallel()

	// Arrange
	profiles := NewProfiles(sharedDB)
	created := newProfile(t)

	// Act
	loaded, err := profiles.ByUserID(t.Context(), created.UserID())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, created.UserID().String(), loaded.UserID().String())
	assert.Equal(t, int64(1), loaded.Version())
	assert.False(t, loaded.HasEmail())
	assert.True(t, loaded.CurrentAvatarID().IsZero())
}

func TestProfilesReportAMissingProfile(t *testing.T) {
	t.Parallel()

	profiles := NewProfiles(sharedDB)

	_, err := profiles.ByUserID(t.Context(), testUserID(t))
	require.ErrorIs(t, err, domain.ErrProfileNotFound)

	_, err = profiles.ByEmailHash(t.Context(), testEmail(t).Hash())
	require.ErrorIs(t, err, domain.ErrProfileNotFound)
}

func TestProfilesRoundTripAnEmailAndFindItByHash(t *testing.T) {
	t.Parallel()

	// Arrange
	profiles := NewProfiles(sharedDB)
	profile := newProfile(t)
	email := testEmail(t)

	require.NoError(t, profile.SetEmail(email, time.Now()))
	require.NoError(t, profiles.Save(t.Context(), &profile))

	// Act
	loaded, err := profiles.ByEmailHash(t.Context(), email.Hash())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, email.String(), loaded.Email().String())
	assert.Equal(t, email.Hash().String(), loaded.EmailHash().String())
	assert.Equal(t, int64(2), loaded.Version(), "a save bumps the version")
}

func TestProfilesRefuseAnEmailTakenByAnotherProfile(t *testing.T) {
	t.Parallel()

	// Arrange
	profiles := NewProfiles(sharedDB)
	email := testEmail(t)

	first := newProfile(t)
	require.NoError(t, first.SetEmail(email, time.Now()))
	require.NoError(t, profiles.Save(t.Context(), &first))

	second := newProfile(t)
	require.NoError(t, second.SetEmail(email, time.Now()))

	// Act
	err := profiles.Save(t.Context(), &second)

	// Assert
	require.ErrorIs(t, err, domain.ErrEmailTaken,
		"the unique index is the only thing that makes resolve-by-email unambiguous")
}

func TestProfilesRefuseAnEmailTakenOnInsert(t *testing.T) {
	t.Parallel()

	// Arrange
	profiles := NewProfiles(sharedDB)
	email := testEmail(t)

	first := newProfile(t)
	require.NoError(t, first.SetEmail(email, time.Now()))
	require.NoError(t, profiles.Save(t.Context(), &first))

	second, err := domain.NewProfile(testUserID(t), time.Now())
	require.NoError(t, err)
	require.NoError(t, second.SetEmail(email, time.Now()))

	// Act
	err = profiles.Create(t.Context(), &second)

	// Assert
	require.ErrorIs(t, err, domain.ErrEmailTaken)
}

func TestProfilesReportAnExistingProfileWithoutKillingTheTransaction(t *testing.T) {
	t.Parallel()

	// Arrange
	profiles, avatars := NewProfiles(sharedDB), NewAvatars(sharedDB)
	existing := newProfile(t)

	again, err := domain.NewProfile(existing.UserID(), time.Now())
	require.NoError(t, err)

	id, err := domain.NewAvatarID()
	require.NoError(t, err)

	avatar, err := domain.NewAvatar(domain.NewAvatarParams{
		ID:       id,
		UserID:   existing.UserID(),
		UploadID: testUploadRef(t),
		Now:      time.Now(),
	})
	require.NoError(t, err)

	// Act
	err = sharedDB.InTx(t.Context(), func(ctx context.Context) error {
		if createErr := profiles.Create(ctx, &again); createErr != nil {
			require.ErrorIs(t, createErr, domain.ErrProfileExists)
		}

		return avatars.Create(ctx, &avatar)
	})

	// Assert
	require.NoError(t, err,
		"start upload inserts the profile and the avatar together: a racing insert of the "+
			"same profile must not abort the transaction, which a raw unique violation would")

	stored, err := avatars.ByID(t.Context(), avatar.ID())
	require.NoError(t, err)
	assert.True(t, stored.Status().Equal(domain.AvatarStatusPending))
}

func TestProfilesRoundTripTheCurrentAvatarPointer(t *testing.T) {
	t.Parallel()

	// Arrange
	profiles := NewProfiles(sharedDB)
	profile := newProfile(t)
	avatar := newReadyAvatar(t, profile.UserID())

	// Act
	require.NoError(t, profile.SetCurrentAvatar(avatar.ID(), time.Now()))
	require.NoError(t, profiles.Save(t.Context(), &profile))

	pointed, err := profiles.ByUserID(t.Context(), profile.UserID())
	require.NoError(t, err)

	require.NoError(t, profile.ClearCurrentAvatar(time.Now()))
	require.NoError(t, profiles.Save(t.Context(), &profile))

	cleared, err := profiles.ByUserID(t.Context(), profile.UserID())
	require.NoError(t, err)

	// Assert
	assert.True(t, pointed.CurrentAvatarID().Equal(avatar.ID()))
	assert.True(t, cleared.CurrentAvatarID().IsZero(), "a cleared pointer is NULL, not a nil uuid")
}

func TestProfilesRefuseAPointerToAnAbsentAvatar(t *testing.T) {
	t.Parallel()

	// Arrange
	profiles := NewProfiles(sharedDB)
	profile := newProfile(t)

	absent, err := domain.NewAvatarID()
	require.NoError(t, err)

	require.NoError(t, profile.SetCurrentAvatar(absent, time.Now()))

	// Act
	err = profiles.Save(t.Context(), &profile)

	// Assert
	require.Error(t, err,
		"the foreign key is what keeps the gallery from pointing at nothing")
}

func TestProfilesDetectALostUpdateOfThePointer(t *testing.T) {
	t.Parallel()

	// Arrange: the avatar of a fact and the avatar being deleted are different rows,
	// so their own versions cannot protect the pointer that both writers touch.
	profiles := NewProfiles(sharedDB)
	profile := newProfile(t)

	oldAvatar := newReadyAvatar(t, profile.UserID())
	newAvatar := newReadyAvatar(t, profile.UserID())

	require.NoError(t, profile.SetCurrentAvatar(oldAvatar.ID(), time.Now()))
	require.NoError(t, profiles.Save(t.Context(), &profile))

	deleter, err := profiles.ByUserID(t.Context(), profile.UserID())
	require.NoError(t, err)

	applier, err := profiles.ByUserID(t.Context(), profile.UserID())
	require.NoError(t, err)

	// Act: the fact wins the race and points the profile at the new avatar.
	require.NoError(t, applier.SetCurrentAvatar(newAvatar.ID(), time.Now()))
	require.NoError(t, profiles.Save(t.Context(), &applier))

	// The deletion of the old avatar then tries to clear the pointer it read.
	require.NoError(t, deleter.ClearCurrentAvatar(time.Now()))
	err = profiles.Save(t.Context(), &deleter)

	// Assert
	require.ErrorIs(t, err, usecase.ErrStaleVersion,
		"without this the ready avatar would silently lose its pointer")

	current, err := profiles.ByUserID(t.Context(), profile.UserID())
	require.NoError(t, err)
	assert.True(t, current.CurrentAvatarID().Equal(newAvatar.ID()))
}
