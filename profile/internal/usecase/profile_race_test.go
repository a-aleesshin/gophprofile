package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

func testTicket(t *testing.T) CreateUploadTicket {
	t.Helper()

	return CreateUploadTicket{
		UploadID:  uuid.Must(uuid.NewV7()).String(),
		URL:       "https://domain.com/s3/gophprofile",
		Fields:    map[string]string{"key": "staging/x", "Content-Type": "image/"},
		ExpiresAt: testNow.Add(15 * time.Minute),
	}
}

func racedProfile(t *testing.T, user domain.UserID) *domain.Profile {
	t.Helper()

	winner, err := domain.NewProfile(user, testNow)
	require.NoError(t, err)

	return &winner
}

func TestStartUpload_SurvivesLosingTheProfileRace(t *testing.T) {
	t.Parallel()

	// Arrange: another request created the profile between our read and our insert.
	user := testUserID(t, "user-1")

	profiles := &stubProfiles{
		profiles:   map[string]domain.Profile{},
		createErr:  domain.ErrProfileExists,
		raceWinner: racedProfile(t, user),
	}

	avatars := &stubAvatars{avatars: map[string]domain.Avatar{}}
	tickets := &stubTickets{ticket: testTicket(t)}

	handler := NewStartUploadHandler(
		stubPolicies{policy: testUploadPolicy()}, profiles, avatars, tickets,
		&stubTransactor{}, fixedClock{now: testNow})

	// Act
	got, err := handler.Handle(context.Background(), StartUploadCommand{
		UserID: user, FileName: "avatar.jpg", MimeType: "image/jpeg", SizeBytes: 2048,
	})

	// Assert
	require.NoError(t, err,
		"the profile exists either way, which is all this step needed from it")
	assert.False(t, got.AvatarID.IsZero())
	require.Len(t, avatars.created, 1, "the avatar must still be created")
}

func TestStartUpload_StillReportsARealCreateFailure(t *testing.T) {
	t.Parallel()

	// Arrange
	broken := errors.New("connection reset by peer")

	profiles := &stubProfiles{profiles: map[string]domain.Profile{}, createErr: broken}
	avatars := &stubAvatars{avatars: map[string]domain.Avatar{}}

	handler := NewStartUploadHandler(
		stubPolicies{policy: testUploadPolicy()}, profiles, avatars,
		&stubTickets{ticket: testTicket(t)}, &stubTransactor{}, fixedClock{now: testNow})

	// Act
	_, err := handler.Handle(context.Background(), StartUploadCommand{
		UserID: testUserID(t, "user-1"), FileName: "a.jpg",
		MimeType: "image/jpeg", SizeBytes: 2048,
	})

	// Assert
	require.ErrorIs(t, err, broken,
		"only a lost race is tolerated, not every failure to insert")
	assert.Empty(t, avatars.created)
}

func TestBindEmail_AppliesTheEmailToTheProfileThatWon(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")

	email, err := domain.NewEmail("user@example.com")
	require.NoError(t, err)

	profiles := &stubProfiles{
		profiles:   map[string]domain.Profile{},
		createErr:  domain.ErrProfileExists,
		raceWinner: racedProfile(t, user),
	}

	handler := NewBindEmailHandler(profiles, &stubTransactor{}, fixedClock{now: testNow})

	// Act
	got, err := handler.Handle(context.Background(), BindEmailCommand{
		UserID: user, Email: email,
	})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, email.Hash().String(), got.EmailHash.String())

	require.Len(t, profiles.saved, 1,
		"losing the insert must not drop the email: the row is reread and updated")
	assert.Equal(t, "user@example.com", profiles.saved[0].Email().String())
}

func TestBindEmail_ReportsAFailedRereadAfterTheRace(t *testing.T) {
	t.Parallel()

	// Arrange: the insert lost the race and the row cannot be read back.
	email, err := domain.NewEmail("user@example.com")
	require.NoError(t, err)

	profiles := &stubProfiles{
		profiles:  map[string]domain.Profile{},
		createErr: domain.ErrProfileExists,
	}

	handler := NewBindEmailHandler(profiles, &stubTransactor{}, fixedClock{now: testNow})

	// Act
	_, err = handler.Handle(context.Background(), BindEmailCommand{
		UserID: testUserID(t, "user-1"), Email: email,
	})

	// Assert
	require.ErrorIs(t, err, domain.ErrProfileNotFound,
		"a row that conflicted and then vanished is not something to paper over")
	assert.Empty(t, profiles.saved)
}
