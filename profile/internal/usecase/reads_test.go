package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

func newViews(t *testing.T, views ...AvatarView) *stubViews {
	t.Helper()

	byID := make(map[string]AvatarView, len(views))
	for _, view := range views {
		byID[view.ID.String()] = view
	}

	return &stubViews{
		views:    byID,
		byUser:   views,
		byHash:   map[string]domain.Profile{},
		profiles: map[string]domain.Profile{},
	}
}

func TestGetAvatar_BuildsEveryVariantURL(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")
	view := testReadyView(t, user, testNow)
	source := newViews(t, view)

	// Act
	got, err := NewGetAvatarHandler(source, testURLs()).
		Handle(context.Background(), GetAvatarCommand{AvatarID: view.ID, Viewer: user})

	// Assert
	require.NoError(t, err)
	assert.Len(t, got.URLs, 3)
	assert.Equal(t,
		"https://domain.com/upload/"+view.FileID.String()+"/300x300.jpg",
		got.URLs["300x300"],
		"the gallery gets ready urls, so the front end never asks for a link per element")
	assert.NotContains(t, got.URLs, "original",
		"the original keeps its exif and is served by signed link only")
}

func TestGetAvatar_DoesNotCallAStrangerTheOwner(t *testing.T) {
	t.Parallel()

	// Arrange
	view := testReadyView(t, testUserID(t, "user-1"), testNow)
	source := newViews(t, view)

	// Act
	stranger, strangerErr := NewGetAvatarHandler(source, testURLs()).
		Handle(context.Background(), GetAvatarCommand{
			AvatarID: view.ID, Viewer: testUserID(t, "user-2"),
		})

	anonymous, anonymousErr := NewGetAvatarHandler(source, testURLs()).
		Handle(context.Background(), GetAvatarCommand{AvatarID: view.ID})

	owner, ownerErr := NewGetAvatarHandler(source, testURLs()).
		Handle(context.Background(), GetAvatarCommand{
			AvatarID: view.ID, Viewer: testUserID(t, "user-1"),
		})

	// Assert
	require.NoError(t, strangerErr, "metadata stays readable, only its private fields do not")
	require.NoError(t, anonymousErr)
	require.NoError(t, ownerErr)

	assert.False(t, stranger.Owner)
	assert.False(t, anonymous.Owner,
		"an absent viewer used to mean no check at all, which let anyone read the file name")
	assert.True(t, owner.Owner)
}

func TestGetAvatar_ServesAPendingAvatarAsMetadata(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")

	view := testReadyView(t, user, testNow)
	view.Status = domain.AvatarStatusProcessing
	view.Variants = nil

	source := newViews(t, view)

	// Act
	got, err := NewGetAvatarHandler(source, testURLs()).
		Handle(context.Background(), GetAvatarCommand{AvatarID: view.ID, Viewer: user})

	// Assert
	require.NoError(t, err,
		"metadata answers for any status, the client watches it to learn when it is ready")
	assert.True(t, got.Avatar.Status.Equal(domain.AvatarStatusProcessing))
	assert.Empty(t, got.URLs)
}

func TestListAvatars_PagesWithAnOpaqueCursor(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")

	views := []AvatarView{
		testReadyView(t, user, testNow),
		testReadyView(t, user, testNow.Add(-time.Hour)),
		testReadyView(t, user, testNow.Add(-2*time.Hour)),
	}

	source := newViews(t, views...)
	handler := NewListAvatarsHandler(source, testURLs())

	// Act
	first, err := handler.Handle(context.Background(), ListAvatarsCommand{
		UserID: user, Limit: 2,
	})
	require.NoError(t, err)

	second, err := handler.Handle(context.Background(), ListAvatarsCommand{
		UserID: user, Limit: 2, Cursor: first.NextCursor,
	})
	require.NoError(t, err)

	// Assert
	assert.Len(t, first.Items, 2)
	assert.NotEmpty(t, first.NextCursor)
	assert.Equal(t, 3, source.page.Limit,
		"one row beyond the page tells us whether a next page exists")

	assert.Equal(t, views[1].CreatedAt.UTC(), source.page.AfterCreatedAt,
		"the cursor carries the created_at of the last row of the page")
	assert.True(t, source.page.AfterID.Equal(views[1].ID),
		"and its id, so rows sharing a timestamp are not skipped or repeated")
	assert.Len(t, first.URLs, 2)
	assert.NotEmpty(t, second.Items)
}

func TestListAvatars_ClampsTheLimit(t *testing.T) {
	t.Parallel()

	user := testUserID(t, "user-1")
	source := newViews(t, testReadyView(t, user, testNow))
	handler := NewListAvatarsHandler(source, testURLs())

	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{name: "zero falls back to the default", limit: 0, want: DefaultPageLimit + 1},
		{name: "negative falls back to the default", limit: -5, want: DefaultPageLimit + 1},
		{name: "above the ceiling is clamped", limit: 500, want: MaxPageLimit + 1},
		{name: "a sane limit is kept", limit: 7, want: 8},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act
			_, err := handler.Handle(context.Background(), ListAvatarsCommand{
				UserID: user, Limit: test.limit,
			})

			// Assert
			require.NoError(t, err)
			assert.Equal(t, test.want, source.page.Limit)
		})
	}
}

func TestListAvatars_RefusesABrokenCursor(t *testing.T) {
	t.Parallel()

	user := testUserID(t, "user-1")
	source := newViews(t, testReadyView(t, user, testNow))
	handler := NewListAvatarsHandler(source, testURLs())

	for _, cursor := range []string{"not base64!", "bm90LWEtY3Vyc29y", "MTIzfG5vdC1hLXV1aWQ"} {
		t.Run(cursor, func(t *testing.T) {
			// Act
			_, err := handler.Handle(context.Background(), ListAvatarsCommand{
				UserID: user, Cursor: cursor,
			})

			// Assert
			require.ErrorIs(t, err, ErrInvalidCursor)
		})
	}
}

func TestResolveAvatar_PicksTheVariantAndDefaults(t *testing.T) {
	t.Parallel()

	// Arrange
	view := testReadyView(t, testUserID(t, "user-1"), testNow)
	handler := NewResolveAvatarHandler(newViews(t, view), testURLs())

	// Act
	explicit, err := handler.Handle(context.Background(), ResolveAvatarCommand{
		AvatarID: view.ID, Size: "100x100",
	})
	require.NoError(t, err)

	implicit, err := handler.Handle(context.Background(), ResolveAvatarCommand{
		AvatarID: view.ID,
	})
	require.NoError(t, err)

	// Assert
	assert.Contains(t, explicit.URL, "/100x100.jpg")
	assert.Contains(t, implicit.URL, "/"+DefaultVariant+".jpg")
	assert.False(t, implicit.Placeholder)
}

func TestResolveAvatar_SaysWhyItCannot(t *testing.T) {
	t.Parallel()

	user := testUserID(t, "user-1")

	t.Run("not ready yet", func(t *testing.T) {
		t.Parallel()

		view := testReadyView(t, user, testNow)
		view.Status = domain.AvatarStatusProcessing

		_, err := NewResolveAvatarHandler(newViews(t, view), testURLs()).
			Handle(context.Background(), ResolveAvatarCommand{AvatarID: view.ID})

		require.ErrorIs(t, err, ErrAvatarNotReady,
			"the client distinguishes wait from gone, so this is not a 404")
	})

	t.Run("no such size", func(t *testing.T) {
		t.Parallel()

		view := testReadyView(t, user, testNow)

		_, err := NewResolveAvatarHandler(newViews(t, view), testURLs()).
			Handle(context.Background(), ResolveAvatarCommand{
				AvatarID: view.ID, Size: "4096x4096",
			})

		require.ErrorIs(t, err, ErrVariantAbsent)
	})

	t.Run("no such avatar", func(t *testing.T) {
		t.Parallel()

		absent, err := domain.NewAvatarID()
		require.NoError(t, err)

		_, err = NewResolveAvatarHandler(newViews(t), testURLs()).
			Handle(context.Background(), ResolveAvatarCommand{AvatarID: absent})

		require.ErrorIs(t, err, domain.ErrAvatarNotFound)
	})
}

func TestResolveCurrentAvatar_AlwaysGivesAPicture(t *testing.T) {
	t.Parallel()

	user := testUserID(t, "user-1")
	urls := testURLs()

	t.Run("the current avatar when there is one", func(t *testing.T) {
		t.Parallel()

		view := testReadyView(t, user, testNow)
		source := newViews(t, view)

		profile, err := domain.NewProfile(user, testNow)
		require.NoError(t, err)
		require.NoError(t, profile.SetCurrentAvatar(view.ID, testNow))

		source.profiles[user.String()] = profile

		got, err := NewResolveCurrentAvatarHandler(source, urls).
			Handle(context.Background(), ResolveCurrentAvatarCommand{UserID: user})

		require.NoError(t, err)
		assert.False(t, got.Placeholder)
		assert.Contains(t, got.URL, "/300x300.jpg")
	})

	tests := []struct {
		name  string
		setup func(*testing.T, *stubViews)
	}{
		{
			name:  "no profile at all",
			setup: func(*testing.T, *stubViews) {},
		},
		{
			name: "a profile without a current avatar",
			setup: func(t *testing.T, s *stubViews) {
				profile, err := domain.NewProfile(user, testNow)
				require.NoError(t, err)

				s.profiles[user.String()] = profile
			},
		},
		{
			name: "a pointer to an avatar that is gone",
			setup: func(t *testing.T, s *stubViews) {
				id, err := domain.NewAvatarID()
				require.NoError(t, err)

				profile, err := domain.NewProfile(user, testNow)
				require.NoError(t, err)
				require.NoError(t, profile.SetCurrentAvatar(id, testNow))

				s.profiles[user.String()] = profile
			},
		},
		{
			name: "a current avatar that is still processing",
			setup: func(t *testing.T, s *stubViews) {
				view := testReadyView(t, user, testNow)
				view.Status = domain.AvatarStatusProcessing
				s.views[view.ID.String()] = view

				profile, err := domain.NewProfile(user, testNow)
				require.NoError(t, err)
				require.NoError(t, profile.SetCurrentAvatar(view.ID, testNow))

				s.profiles[user.String()] = profile
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			source := newViews(t)
			test.setup(t, source)

			// Act
			got, err := NewResolveCurrentAvatarHandler(source, urls).
				Handle(context.Background(), ResolveCurrentAvatarCommand{UserID: user})

			// Assert
			require.NoError(t, err)
			assert.True(t, got.Placeholder,
				"an embedding page must always get an image, never a 404")
			assert.Equal(t, urls.placeholder, got.URL)
		})
	}
}

func TestResolveByEmail_FindsTheProfileByHash(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")
	view := testReadyView(t, user, testNow)

	email, err := domain.NewEmail("user@example.com")
	require.NoError(t, err)

	profile, err := domain.NewProfile(user, testNow)
	require.NoError(t, err)
	require.NoError(t, profile.SetEmail(email, testNow))
	require.NoError(t, profile.SetCurrentAvatar(view.ID, testNow))

	source := newViews(t, view)
	source.byHash[email.Hash().String()] = profile

	// Act
	got, err := NewResolveByEmailHandler(source, testURLs()).
		Handle(context.Background(), ResolveByEmailCommand{EmailHash: email.Hash()})

	// Assert
	require.NoError(t, err)
	assert.False(t, got.Placeholder)
	assert.Contains(t, got.URL, "/300x300.jpg")
}

func TestResolveByEmail_FallsBackToThePlaceholder(t *testing.T) {
	t.Parallel()

	// Arrange
	email, err := domain.NewEmail("nobody@example.com")
	require.NoError(t, err)

	urls := testURLs()

	// Act
	got, err := NewResolveByEmailHandler(newViews(t), urls).
		Handle(context.Background(), ResolveByEmailCommand{EmailHash: email.Hash()})

	// Assert
	require.NoError(t, err)
	assert.True(t, got.Placeholder)
	assert.Equal(t, urls.placeholder, got.URL)
}

func TestBindEmailStoresTheHashAlongsideTheEmail(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")

	profiles := &stubProfiles{profiles: map[string]domain.Profile{}}
	tx := &stubTransactor{}

	email, err := domain.NewEmail("User@Example.com")
	require.NoError(t, err)

	// Act
	got, err := NewBindEmailHandler(profiles, tx, fixedClock{now: testNow}).
		Handle(context.Background(), BindEmailCommand{UserID: user, Email: email})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, email.Hash().String(), got.EmailHash.String())

	require.Len(t, profiles.created, 1, "binding an email may be the first thing a user does")
	assert.Equal(t, "user@example.com", profiles.created[0].Email().String())
}

func TestListAvatars_HidesWhatIsNotServableFromAStranger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		viewer   domain.UserID
		owner    bool
		servable bool
	}{
		{name: "anonymous", servable: true},
		{name: "stranger", viewer: testUserID(t, "user-2"), servable: true},
		{name: "owner", viewer: testUserID(t, "user-1"), owner: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			user := testUserID(t, "user-1")
			source := newViews(t, testReadyView(t, user, testNow))

			// Act
			got, err := NewListAvatarsHandler(source, testURLs()).
				Handle(context.Background(), ListAvatarsCommand{
					UserID: user, Viewer: test.viewer,
				})

			// Assert
			require.NoError(t, err)
			assert.Equal(t, test.owner, got.Owner)
			assert.Equal(t, test.servable, source.page.OnlyServable,
				"a gallery open to everyone would otherwise enumerate failed uploads")
		})
	}
}
