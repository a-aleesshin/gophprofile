//go:build integration

package postgres

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/internal/clock"
	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

type testURLs struct{}

func (testURLs) VariantURL(fileID domain.FileRef, name, ext string) (string, error) {
	return "https://domain.com/upload/" + fileID.String() + "/" + name + "." + ext, nil
}

func (testURLs) PlaceholderURL() string { return "https://domain.com/static/placeholder.png" }

func TestBindEmailRunsAgainstPostgres(t *testing.T) {
	t.Parallel()

	// Arrange
	handler := usecase.NewBindEmailHandler(NewProfiles(sharedDB), sharedDB, clock.System{})
	user := testUserID(t)
	email := testEmail(t)

	// Act: the first call has no profile to bind to and must create one.
	created, err := handler.Handle(t.Context(), usecase.BindEmailCommand{
		UserID: user, Email: email,
	})
	require.NoError(t, err)

	replacement := testEmail(t)

	rebound, err := handler.Handle(t.Context(), usecase.BindEmailCommand{
		UserID: user, Email: replacement,
	})
	require.NoError(t, err)

	// Assert
	assert.Equal(t, email.Hash().String(), created.EmailHash.String())
	assert.Equal(t, replacement.Hash().String(), rebound.EmailHash.String())

	stored, err := NewProfiles(sharedDB).ByEmailHash(t.Context(), replacement.Hash())
	require.NoError(t, err)
	assert.Equal(t, user.String(), stored.UserID().String())

	_, err = NewProfiles(sharedDB).ByEmailHash(t.Context(), email.Hash())
	require.ErrorIs(t, err, domain.ErrProfileNotFound, "the old hash must not linger")
}

func TestListAvatarsPagesThroughPostgres(t *testing.T) {
	t.Parallel()

	// Arrange
	avatars := NewAvatars(sharedDB)
	handler := usecase.NewListAvatarsHandler(avatars, testURLs{})

	user := newUser(t)
	shared := time.Now().Truncate(time.Microsecond)

	for range 5 {
		newPendingAvatarAt(t, user, shared)
	}

	ready := newReadyAvatar(t, user)

	// Act
	seen := make(map[string]int, 6)
	cursor := ""

	for page := range 10 {
		result, err := handler.Handle(t.Context(), usecase.ListAvatarsCommand{
			UserID: user, Limit: 2, Cursor: cursor,
		})
		require.NoError(t, err, "page %d", page)

		for _, view := range result.Items {
			seen[view.ID.String()]++
		}

		cursor = result.NextCursor
		if cursor == "" {
			break
		}
	}

	// Assert
	assert.Empty(t, cursor, "the walk must terminate, not loop on its own cursor")
	assert.Len(t, seen, 6)

	for id, times := range seen {
		assert.Equal(t, 1, times, "%s appeared %d times", id, times)
	}

	first, err := handler.Handle(t.Context(), usecase.ListAvatarsCommand{
		UserID: user, Limit: 10,
	})
	require.NoError(t, err)
	assert.Equal(t,
		"https://domain.com/upload/"+ready.FileID().String()+"/300x300.jpg",
		first.URLs[ready.ID().String()]["300x300"])
}

func TestResolveCurrentAvatarRunsAgainstPostgres(t *testing.T) {
	t.Parallel()

	// Arrange
	profiles, avatars := NewProfiles(sharedDB), NewAvatars(sharedDB)

	handler := usecase.NewResolveCurrentAvatarHandler(
		struct {
			*Profiles
			*Avatars
		}{profiles, avatars},
		testURLs{},
	)

	profile := newProfile(t)
	user := profile.UserID()

	// Act: no current avatar yet.
	empty, err := handler.Handle(t.Context(), usecase.ResolveCurrentAvatarCommand{UserID: user})
	require.NoError(t, err)

	ready := newReadyAvatar(t, user)
	require.NoError(t, profile.SetCurrentAvatar(ready.ID(), time.Now()))
	require.NoError(t, profiles.Save(t.Context(), &profile))

	pointed, err := handler.Handle(t.Context(), usecase.ResolveCurrentAvatarCommand{UserID: user})
	require.NoError(t, err)

	// Assert
	assert.True(t, empty.Placeholder)
	assert.Equal(t, testURLs{}.PlaceholderURL(), empty.URL)

	assert.False(t, pointed.Placeholder)
	assert.Equal(t,
		"https://domain.com/upload/"+ready.FileID().String()+"/300x300.jpg",
		pointed.URL)
}

func TestFailStuckAvatarsRunsAgainstPostgres(t *testing.T) {
	t.Parallel()

	// Arrange
	avatars := NewAvatars(sharedDB)
	user := newUser(t)

	stuck := newPendingAvatar(t, user)
	require.NoError(t, stuck.StartProcessing(testFileRef(t), time.Now().Add(-time.Hour)))
	require.NoError(t, avatars.Save(t.Context(), &stuck))

	fresh := newPendingAvatar(t, user)
	require.NoError(t, fresh.StartProcessing(testFileRef(t), time.Now()))
	require.NoError(t, avatars.Save(t.Context(), &fresh))

	// Act
	got, err := usecase.NewFailStuckAvatarsHandler(avatars, sharedDB, clock.System{}).
		Handle(t.Context(), usecase.FailStuckAvatarsCommand{
			StaleAfter: 30 * time.Minute, Limit: 50,
		})

	// Assert
	require.NoError(t, err)
	assert.GreaterOrEqual(t, got.Failed, 1)

	failed, err := avatars.ByID(t.Context(), stuck.ID())
	require.NoError(t, err)
	assert.True(t, failed.Status().Equal(domain.AvatarStatusFailed))
	assert.Equal(t, usecase.ReasonProcessingStuck, failed.FailedReason())
	assert.Equal(t, int64(3), failed.Version(), "the watchdog writes under the version it read")

	untouched, err := avatars.ByID(t.Context(), fresh.ID())
	require.NoError(t, err)
	assert.True(t, untouched.Status().Equal(domain.AvatarStatusProcessing),
		"a file that file may still be working on must be left alone")
}
