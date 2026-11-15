package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type startUploadHarness struct {
	handler  *StartUploadHandler
	profiles *stubProfiles
	avatars  *stubAvatars
	tickets  *stubTickets
	tx       *stubTransactor
}

func newStartUploadHarness(t *testing.T, policy UploadPolicy) startUploadHarness {
	t.Helper()

	profiles := &stubProfiles{profiles: map[string]domain.Profile{}}
	avatars := &stubAvatars{avatars: map[string]domain.Avatar{}}
	tickets := &stubTickets{ticket: CreateUploadTicket{
		UploadID:  uuid.Must(uuid.NewV7()).String(),
		URL:       "https://domain.com/s3/gophprofile",
		Fields:    map[string]string{"key": "staging/x", "Content-Type": "image/"},
		ExpiresAt: testNow.Add(15 * time.Minute),
	}}
	tx := &stubTransactor{}

	return startUploadHarness{
		handler: NewStartUploadHandler(
			stubPolicies{policy: policy}, profiles, avatars, tickets, tx,
			fixedClock{now: testNow}),
		profiles: profiles,
		avatars:  avatars,
		tickets:  tickets,
		tx:       tx,
	}
}

func testStartUploadCommand(t *testing.T) StartUploadCommand {
	t.Helper()

	return StartUploadCommand{
		UserID:    testUserID(t, "user-1"),
		FileName:  "avatar.jpg",
		MimeType:  "image/jpeg",
		SizeBytes: 1 << 20,
	}
}

func TestStartUpload_CreatesTheProfileAndThePendingAvatar(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newStartUploadHarness(t, testUploadPolicy())

	// Act
	got, err := h.handler.Handle(context.Background(), testStartUploadCommand(t))

	// Assert
	require.NoError(t, err)
	assert.True(t, got.Status.Equal(domain.AvatarStatusPending))
	assert.Equal(t, "https://domain.com/s3/gophprofile", got.URL)
	assert.Equal(t, testNow.Add(15*time.Minute), got.ExpiresAt)
	assert.Equal(t, "image/", got.Fields["Content-Type"],
		"the prefilled content type travels to the client untouched, it overwrites it itself")

	require.Len(t, h.profiles.created, 1, "the first upload of a user creates the profile")
	require.Len(t, h.avatars.created, 1)
	assert.True(t, h.avatars.created[0].ID().Equal(got.AvatarID))
	assert.Equal(t, 1, h.tx.calls, "the profile and the avatar land in one transaction")
}

func TestStartUpload_UsesTheAvatarIdAsTheIdempotencyKey(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newStartUploadHarness(t, testUploadPolicy())

	// Act
	got, err := h.handler.Handle(context.Background(), testStartUploadCommand(t))

	// Assert
	require.NoError(t, err)
	assert.Equal(t, got.AvatarID.String(), h.tickets.params.IdempotencyKey,
		"a retried request must reuse the ticket, and our own id is the only stable key")
	assert.Equal(t, "avatar", h.tickets.params.Namespace)
	assert.Equal(t, "user-1", h.tickets.params.OwnerRef)
	assert.Equal(t, "avatar.jpg", h.tickets.params.OriginalName)
	assert.Equal(t, int64(1<<20), h.tickets.params.MaxSizeBytes)
}

func TestStartUpload_WritesNothingWhenTheTicketFails(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newStartUploadHarness(t, testUploadPolicy())
	h.tickets.err = assert.AnError

	// Act
	_, err := h.handler.Handle(context.Background(), testStartUploadCommand(t))

	// Assert
	require.ErrorIs(t, err, assert.AnError)
	assert.Empty(t, h.avatars.created,
		"a row without a ticket would be a pending avatar nobody can ever complete")
	assert.Empty(t, h.profiles.created)
	assert.Zero(t, h.tx.calls)
}

func TestStartUpload_KeepsAnExistingProfile(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newStartUploadHarness(t, testUploadPolicy())

	user := testUserID(t, "user-1")

	existing, err := domain.NewProfile(user, testNow.Add(-time.Hour))
	require.NoError(t, err)

	h.profiles.profiles[user.String()] = existing

	// Act
	_, err = h.handler.Handle(context.Background(), testStartUploadCommand(t))

	// Assert
	require.NoError(t, err)
	assert.Empty(t, h.profiles.created, "the profile is already there")
	require.Len(t, h.avatars.created, 1)
}

func TestStartUpload_RejectsEarly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*StartUploadCommand)
		policy func(*UploadPolicy)
		count  int
		want   error
	}{
		{
			name:   "no user",
			mutate: func(c *StartUploadCommand) { c.UserID = domain.UserID{} },
			want:   ErrInvalidCommand,
		},
		{
			name:   "no mime",
			mutate: func(c *StartUploadCommand) { c.MimeType = "" },
			want:   ErrInvalidCommand,
		},
		{
			name:   "a mime the namespace does not take",
			mutate: func(c *StartUploadCommand) { c.MimeType = "image/gif" },
			want:   ErrUnsupportedMimeType,
		},
		{
			name:   "zero size",
			mutate: func(c *StartUploadCommand) { c.SizeBytes = 0 },
			want:   ErrInvalidCommand,
		},
		{
			name:   "larger than the limit",
			mutate: func(c *StartUploadCommand) { c.SizeBytes = 11 << 20 },
			want:   ErrFileTooLarge,
		},
		{
			name:  "the user is at the limit",
			count: 20,
			want:  ErrTooManyAvatars,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			policy := testUploadPolicy()
			if test.policy != nil {
				test.policy(&policy)
			}

			h := newStartUploadHarness(t, policy)
			h.avatars.count = test.count

			cmd := testStartUploadCommand(t)
			if test.mutate != nil {
				test.mutate(&cmd)
			}

			// Act
			_, err := h.handler.Handle(context.Background(), cmd)

			// Assert
			require.ErrorIs(t, err, test.want)
			assert.Zero(t, h.tickets.calls,
				"early rejection exists to keep a doomed request away from the file service")
		})
	}
}

func TestStartUpload_NormalisesTheDeclaredMime(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newStartUploadHarness(t, testUploadPolicy())

	cmd := testStartUploadCommand(t)
	cmd.MimeType = "  IMAGE/JPEG  "

	// Act
	_, err := h.handler.Handle(context.Background(), cmd)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "image/jpeg", h.tickets.params.DeclaredMime)
}

func TestStartUpload_RefusesATicketWithoutAUsableUploadID(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newStartUploadHarness(t, testUploadPolicy())
	h.tickets.ticket.UploadID = "not-a-uuid"

	// Act
	_, err := h.handler.Handle(context.Background(), testStartUploadCommand(t))

	// Assert
	require.ErrorIs(t, err, domain.ErrInvalidReference)
	assert.Empty(t, h.avatars.created)
}
