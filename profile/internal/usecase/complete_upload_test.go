package usecase

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type completeUploadHarness struct {
	handler   *CompleteUploadHandler
	avatars   *stubAvatars
	confirmer *stubConfirmer
	tx        *stubTransactor
	avatar    domain.Avatar
}

func newCompleteUploadHarness(t *testing.T, avatar domain.Avatar) completeUploadHarness {
	t.Helper()

	avatars := &stubAvatars{avatars: map[string]domain.Avatar{avatar.ID().String(): avatar}}
	confirmer := &stubConfirmer{confirmation: CompleteUploadConfirmation{
		FileID: uuid.Must(uuid.NewV7()).String(),
	}}
	tx := &stubTransactor{}

	return completeUploadHarness{
		handler:   NewCompleteUploadHandler(avatars, confirmer, tx, fixedClock{now: testNow}),
		avatars:   avatars,
		confirmer: confirmer,
		tx:        tx,
		avatar:    avatar,
	}
}

func TestCompleteUpload_MovesThePendingAvatarToProcessing(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")
	h := newCompleteUploadHarness(t, testPendingAvatar(t, user))

	// Act
	got, err := h.handler.Handle(context.Background(), CompleteUploadCommand{
		AvatarID: h.avatar.ID(),
		UserID:   user,
	})

	// Assert
	require.NoError(t, err)
	assert.True(t, got.Status.Equal(domain.AvatarStatusProcessing))
	assert.Equal(t, h.avatar.UploadID().String(), h.confirmer.uploadID,
		"the ticket is identified by the upload id we stored, not by the avatar id")

	require.Len(t, h.avatars.saved, 1)
	assert.Equal(t, h.confirmer.confirmation.FileID, h.avatars.saved[0].FileID().String())
}

func TestCompleteUpload_IsIdempotent(t *testing.T) {
	t.Parallel()

	user := testUserID(t, "user-1")

	for _, status := range []struct {
		name  string
		build func(*testing.T) domain.Avatar
		want  domain.AvatarStatus
	}{
		{
			name: "already processing",
			build: func(t *testing.T) domain.Avatar {
				avatar := testPendingAvatar(t, user)
				require.NoError(t, avatar.StartProcessing(testFileRef(t), testNow))

				return avatar
			},
			want: domain.AvatarStatusProcessing,
		},
		{
			name: "already ready",
			build: func(t *testing.T) domain.Avatar {
				avatar := testPendingAvatar(t, user)
				require.NoError(t, avatar.StartProcessing(testFileRef(t), testNow))
				require.NoError(t, avatar.MarkReady(testSnapshot(t), testVariants(t), testNow))

				return avatar
			},
			want: domain.AvatarStatusReady,
		},
	} {
		t.Run(status.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			h := newCompleteUploadHarness(t, status.build(t))

			// Act
			got, err := h.handler.Handle(context.Background(), CompleteUploadCommand{
				AvatarID: h.avatar.ID(),
				UserID:   user,
			})

			// Assert
			require.NoError(t, err)
			assert.True(t, got.Status.Equal(status.want))
			assert.Zero(t, h.confirmer.calls,
				"a repeated complete must not ask the file service again")
			assert.Empty(t, h.avatars.saved)
		})
	}
}

func TestCompleteUpload_RefusesSomebodyElsesAvatar(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newCompleteUploadHarness(t, testPendingAvatar(t, testUserID(t, "user-1")))

	// Act
	_, err := h.handler.Handle(context.Background(), CompleteUploadCommand{
		AvatarID: h.avatar.ID(),
		UserID:   testUserID(t, "user-2"),
	})

	// Assert
	require.ErrorIs(t, err, ErrForbidden)
	assert.Zero(t, h.confirmer.calls)
}

func TestCompleteUpload_ReportsAMissingAvatar(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newCompleteUploadHarness(t, testPendingAvatar(t, testUserID(t, "user-1")))

	other, err := domain.NewAvatarID()
	require.NoError(t, err)

	// Act
	_, err = h.handler.Handle(context.Background(), CompleteUploadCommand{
		AvatarID: other,
		UserID:   testUserID(t, "user-1"),
	})

	// Assert
	require.ErrorIs(t, err, domain.ErrAvatarNotFound)
}

func TestCompleteUpload_SurvivesALostRace(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")
	h := newCompleteUploadHarness(t, testPendingAvatar(t, user))
	h.avatars.saveErr = ErrStaleVersion

	winner := h.avatar
	require.NoError(t, winner.MarkDeleted(testNow))

	h.avatars.avatars[winner.ID().String()] = winner

	// Act
	got, err := h.handler.Handle(context.Background(), CompleteUploadCommand{
		AvatarID: h.avatar.ID(),
		UserID:   user,
	})

	// Assert
	require.NoError(t, err,
		"the user deleted the avatar while we were completing it, that is not an error")
	assert.True(t, got.Status.Equal(domain.AvatarStatusDeleted))
}

func TestCompleteUpload_RejectsAnEmptyCommand(t *testing.T) {
	t.Parallel()

	h := newCompleteUploadHarness(t, testPendingAvatar(t, testUserID(t, "user-1")))

	// Act
	_, noAvatar := h.handler.Handle(context.Background(), CompleteUploadCommand{
		UserID: testUserID(t, "user-1"),
	})

	_, noUser := h.handler.Handle(context.Background(), CompleteUploadCommand{
		AvatarID: h.avatar.ID(),
	})

	// Assert
	require.ErrorIs(t, noAvatar, ErrInvalidCommand)
	require.ErrorIs(t, noUser, ErrInvalidCommand)
}
