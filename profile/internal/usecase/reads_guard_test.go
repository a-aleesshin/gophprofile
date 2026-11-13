package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

func TestReadsRejectAnEmptyCommand(t *testing.T) {
	t.Parallel()

	source := newViews(t)
	urls := testURLs()
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "get avatar without an id",
			call: func() error {
				_, err := NewGetAvatarHandler(source, urls).Handle(ctx, GetAvatarCommand{})

				return err
			},
		},
		{
			name: "list avatars without a user",
			call: func() error {
				_, err := NewListAvatarsHandler(source, urls).Handle(ctx, ListAvatarsCommand{})

				return err
			},
		},
		{
			name: "resolve avatar without an id",
			call: func() error {
				_, err := NewResolveAvatarHandler(source, urls).
					Handle(ctx, ResolveAvatarCommand{})

				return err
			},
		},
		{
			name: "resolve the current avatar without a user",
			call: func() error {
				_, err := NewResolveCurrentAvatarHandler(source, urls).
					Handle(ctx, ResolveCurrentAvatarCommand{})

				return err
			},
		},
		{
			name: "resolve by email without a hash",
			call: func() error {
				_, err := NewResolveByEmailHandler(source, urls).
					Handle(ctx, ResolveByEmailCommand{})

				return err
			},
		},
		{
			name: "bind an email without a user",
			call: func() error {
				_, err := NewBindEmailHandler(
					&stubProfiles{profiles: map[string]domain.Profile{}},
					&stubTransactor{}, fixedClock{now: testNow},
				).Handle(ctx, BindEmailCommand{})

				return err
			},
		},
		{
			name: "bind an empty email",
			call: func() error {
				_, err := NewBindEmailHandler(
					&stubProfiles{profiles: map[string]domain.Profile{}},
					&stubTransactor{}, fixedClock{now: testNow},
				).Handle(ctx, BindEmailCommand{UserID: testUserID(t, "user-1")})

				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			require.ErrorIs(t, test.call(), ErrInvalidCommand,
				"a zero value must never reach the database as a query")
		})
	}
}

func TestReadsPropagateAStorageFailure(t *testing.T) {
	t.Parallel()

	broken := errors.New("connection reset by peer")

	user := testUserID(t, "user-1")
	view := testReadyView(t, user, testNow)
	urls := testURLs()
	ctx := context.Background()

	tests := []struct {
		name string
		call func(*stubViews) error
	}{
		{
			name: "get avatar",
			call: func(s *stubViews) error {
				_, err := NewGetAvatarHandler(s, urls).
					Handle(ctx, GetAvatarCommand{AvatarID: view.ID})

				return err
			},
		},
		{
			name: "list avatars",
			call: func(s *stubViews) error {
				_, err := NewListAvatarsHandler(s, urls).
					Handle(ctx, ListAvatarsCommand{UserID: user})

				return err
			},
		},
		{
			name: "resolve avatar",
			call: func(s *stubViews) error {
				_, err := NewResolveAvatarHandler(s, urls).
					Handle(ctx, ResolveAvatarCommand{AvatarID: view.ID})

				return err
			},
		},
		{
			name: "resolve the current avatar",
			call: func(s *stubViews) error {
				_, err := NewResolveCurrentAvatarHandler(s, urls).
					Handle(ctx, ResolveCurrentAvatarCommand{UserID: user})

				return err
			},
		},
		{
			name: "resolve by email",
			call: func(s *stubViews) error {
				_, err := NewResolveByEmailHandler(s, urls).
					Handle(ctx, ResolveByEmailCommand{EmailHash: testEmailHash(t)})

				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			source := newViews(t, view)
			source.byIDErr, source.listErr = broken, broken
			source.hashErr, source.profileErr = broken, broken

			// Act
			err := test.call(source)

			// Assert
			require.ErrorIs(t, err, broken,
				"an unreachable database is not a missing avatar: it must not become a placeholder")
		})
	}
}

func TestBindEmailReportsAFailedWrite(t *testing.T) {
	t.Parallel()

	broken := errors.New("unique violation on profiles_email_key")

	profiles := &stubProfiles{profiles: map[string]domain.Profile{}, saveErr: broken}

	email, err := domain.NewEmail("taken@example.com")
	require.NoError(t, err)

	_, err = NewBindEmailHandler(profiles, &stubTransactor{}, fixedClock{now: testNow}).
		Handle(context.Background(), BindEmailCommand{
			UserID: testUserID(t, "user-1"), Email: email,
		})

	require.ErrorIs(t, err, broken,
		"two users cannot share an email, and the caller has to learn that")
}

func TestBindEmailUpdatesAnExistingProfile(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")

	existing, err := domain.NewProfile(user, testNow)
	require.NoError(t, err)

	profiles := &stubProfiles{profiles: map[string]domain.Profile{user.String(): existing}}

	email, err := domain.NewEmail("user@example.com")
	require.NoError(t, err)

	// Act
	got, err := NewBindEmailHandler(profiles, &stubTransactor{}, fixedClock{now: testNow}).
		Handle(context.Background(), BindEmailCommand{UserID: user, Email: email})

	// Assert
	require.NoError(t, err)
	assert.Empty(t, profiles.created)
	require.Len(t, profiles.saved, 1, "the profile exists, so this is an update under its version")
	assert.Equal(t, got.EmailHash.String(), profiles.saved[0].EmailHash().String())
}
