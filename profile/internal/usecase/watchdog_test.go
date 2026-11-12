package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

func newRecoverHandler(
	avatars *stubAvatars, uploads *stubConfirmer,
) *RecoverStaleUploadsHandler {
	return NewRecoverStaleUploadsHandler(avatars, uploads, &stubTransactor{},
		fixedClock{now: testNow})
}

func TestRecoverStaleUploads_FinishesWhatTheClientAbandoned(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")
	avatar := testPendingAvatar(t, user)
	fileID := testFileRef(t)

	avatars := &stubAvatars{stale: []domain.Avatar{avatar}}
	uploads := &stubConfirmer{
		confirmation: CompleteUploadConfirmation{FileID: fileID.String()},
	}

	// Act
	got, err := newRecoverHandler(avatars, uploads).
		Handle(context.Background(), RecoverStaleUploadsCommand{
			StaleAfter: 10 * time.Minute, Limit: 50,
		})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RecoverStaleUploadsResult{Recovered: 1}, got,
		"the bytes are in storage, so the right move is to finish, not to give up")

	assert.Equal(t, avatar.UploadID().String(), uploads.uploadID)

	require.Len(t, avatars.saved, 1)
	assert.True(t, avatars.saved[0].Status().Equal(domain.AvatarStatusProcessing))
	assert.Equal(t, fileID.String(), avatars.saved[0].FileID().String())
}

func TestRecoverStaleUploads_AsksOnlyAboutPendingOnes(t *testing.T) {
	t.Parallel()

	// Arrange
	avatars := &stubAvatars{}

	// Act
	_, err := newRecoverHandler(avatars, &stubConfirmer{}).
		Handle(context.Background(), RecoverStaleUploadsCommand{
			StaleAfter: 10 * time.Minute, Limit: 50,
		})

	// Assert
	require.NoError(t, err)
	require.Len(t, avatars.staleStatuses, 1)
	assert.True(t, avatars.staleStatuses[0].Equal(domain.AvatarStatusPending))
	assert.Equal(t, testNow.Add(-10*time.Minute), avatars.staleBefore)
	assert.Equal(t, 50, avatars.staleLimit)
}

func TestRecoverStaleUploads_GivesUpOnlyWhenFileSaysSo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		reason string
	}{
		{
			name:   "the ticket is gone",
			err:    ErrUploadNotFound,
			reason: ReasonUploadUnknown,
		},
		{
			name:   "nothing was ever uploaded",
			err:    ErrUploadNotUsable,
			reason: ReasonUploadAbandoned,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			avatars := &stubAvatars{
				stale: []domain.Avatar{testPendingAvatar(t, testUserID(t, "user-1"))},
			}

			// Act
			got, err := newRecoverHandler(avatars, &stubConfirmer{err: test.err}).
				Handle(context.Background(), RecoverStaleUploadsCommand{
					StaleAfter: 10 * time.Minute, Limit: 50,
				})

			// Assert
			require.NoError(t, err)
			assert.Equal(t, RecoverStaleUploadsResult{Failed: 1}, got)

			require.Len(t, avatars.saved, 1)
			assert.True(t, avatars.saved[0].Status().Equal(domain.AvatarStatusFailed))
			assert.Equal(t, test.reason, avatars.saved[0].FailedReason())
		})
	}
}

func TestRecoverStaleUploads_LeavesTheAvatarAloneWhenFileIsDown(t *testing.T) {
	t.Parallel()

	// Arrange
	avatars := &stubAvatars{
		stale: []domain.Avatar{testPendingAvatar(t, testUserID(t, "user-1"))},
	}
	uploads := &stubConfirmer{err: ErrFileServiceUnavailable}

	// Act
	got, err := newRecoverHandler(avatars, uploads).
		Handle(context.Background(), RecoverStaleUploadsCommand{
			StaleAfter: 10 * time.Minute, Limit: 50,
		})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RecoverStaleUploadsResult{Skipped: 1}, got)
	assert.Empty(t, avatars.saved,
		"an outage of file is not evidence that the upload failed")
}

type sequenceConfirmer struct {
	errs  []error
	calls int
}

func (s *sequenceConfirmer) CompleteUpload(
	context.Context, string,
) (CompleteUploadConfirmation, error) {
	err := s.errs[s.calls]
	s.calls++

	if err != nil {
		return CompleteUploadConfirmation{}, err
	}

	return CompleteUploadConfirmation{}, nil
}

func TestRecoverStaleUploads_KeepsGoingPastASkippedRow(t *testing.T) {
	t.Parallel()

	// Arrange: the first row cannot be decided, the second one can.
	user := testUserID(t, "user-1")
	avatars := &stubAvatars{stale: []domain.Avatar{
		testPendingAvatar(t, user),
		testPendingAvatar(t, user),
	}}
	uploads := &sequenceConfirmer{errs: []error{ErrFileServiceUnavailable, ErrUploadNotFound}}

	// Act
	got, err := NewRecoverStaleUploadsHandler(avatars, uploads, &stubTransactor{},
		fixedClock{now: testNow}).
		Handle(context.Background(), RecoverStaleUploadsCommand{
			StaleAfter: 10 * time.Minute, Limit: 50,
		})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RecoverStaleUploadsResult{Skipped: 1, Failed: 1}, got,
		"one undecidable row must not end the tick for the rest of the batch")
	assert.Equal(t, 2, uploads.calls)
}

func TestRecoverStaleUploads_CountsALostRaceAsSkipped(t *testing.T) {
	t.Parallel()

	// Arrange
	avatars := &stubAvatars{
		stale:   []domain.Avatar{testPendingAvatar(t, testUserID(t, "user-1"))},
		saveErr: ErrStaleVersion,
	}
	uploads := &stubConfirmer{err: ErrUploadNotFound}

	// Act
	got, err := newRecoverHandler(avatars, uploads).
		Handle(context.Background(), RecoverStaleUploadsCommand{
			StaleAfter: 10 * time.Minute, Limit: 50,
		})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RecoverStaleUploadsResult{Skipped: 1}, got,
		"the fact got there first, and it knows more than the watchdog does")
}

func TestRecoverStaleUploads_FailsAnAvatarWithNoTicketWithoutCallingFile(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")

	id, err := domain.NewAvatarID()
	require.NoError(t, err)

	avatar, err := domain.RestoreAvatar(domain.RestoreAvatarParams{
		ID:        id,
		Version:   1,
		UserID:    user,
		Status:    domain.AvatarStatusPending,
		CreatedAt: testNow,
		UpdatedAt: testNow,
	})
	require.NoError(t, err)

	avatars := &stubAvatars{stale: []domain.Avatar{avatar}}
	uploads := &stubConfirmer{}

	// Act
	got, err := newRecoverHandler(avatars, uploads).
		Handle(context.Background(), RecoverStaleUploadsCommand{
			StaleAfter: 10 * time.Minute, Limit: 50,
		})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RecoverStaleUploadsResult{Failed: 1}, got)
	assert.Zero(t, uploads.calls, "there is no ticket to ask about")
	assert.Equal(t, ReasonUploadUnknown, avatars.saved[0].FailedReason())
}

func TestFailStuckAvatars_FailsWhatNoFactEverReachedUs(t *testing.T) {
	t.Parallel()

	// Arrange
	user := testUserID(t, "user-1")
	avatars := &stubAvatars{stale: []domain.Avatar{
		testProcessingAvatar(t, user),
		testProcessingAvatar(t, user),
	}}

	// Act
	got, err := NewFailStuckAvatarsHandler(avatars, &stubTransactor{}, fixedClock{now: testNow}).
		Handle(context.Background(), FailStuckAvatarsCommand{
			StaleAfter: 30 * time.Minute, Limit: 50,
		})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, FailStuckAvatarsResult{Failed: 2}, got)

	require.Len(t, avatars.staleStatuses, 1)
	assert.True(t, avatars.staleStatuses[0].Equal(domain.AvatarStatusProcessing))
	assert.Equal(t, testNow.Add(-30*time.Minute), avatars.staleBefore)

	require.Len(t, avatars.saved, 2)
	assert.True(t, avatars.saved[0].Status().Equal(domain.AvatarStatusFailed))
	assert.Equal(t, ReasonProcessingStuck, avatars.saved[0].FailedReason())
}

func TestFailStuckAvatars_CountsALostRaceAsSkipped(t *testing.T) {
	t.Parallel()

	// Arrange
	avatars := &stubAvatars{
		stale:   []domain.Avatar{testProcessingAvatar(t, testUserID(t, "user-1"))},
		saveErr: ErrStaleVersion,
	}

	// Act
	got, err := NewFailStuckAvatarsHandler(avatars, &stubTransactor{}, fixedClock{now: testNow}).
		Handle(context.Background(), FailStuckAvatarsCommand{
			StaleAfter: 30 * time.Minute, Limit: 50,
		})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, FailStuckAvatarsResult{Skipped: 1}, got,
		"FileProcessed arrived while the tick was running, and it wins")
}

func TestWatchdogReportsAStorageFailure(t *testing.T) {
	t.Parallel()

	broken := errors.New("connection reset by peer")
	avatars := &stubAvatars{staleErr: broken}

	_, err := newRecoverHandler(avatars, &stubConfirmer{}).
		Handle(context.Background(), RecoverStaleUploadsCommand{
			StaleAfter: time.Minute, Limit: 10,
		})
	require.ErrorIs(t, err, broken)

	_, err = NewFailStuckAvatarsHandler(avatars, &stubTransactor{}, fixedClock{now: testNow}).
		Handle(context.Background(), FailStuckAvatarsCommand{
			StaleAfter: time.Minute, Limit: 10,
		})
	require.ErrorIs(t, err, broken)
}

func TestWatchdogRejectsAnUnboundedCommand(t *testing.T) {
	t.Parallel()

	recoverer := newRecoverHandler(&stubAvatars{}, &stubConfirmer{})
	fail := NewFailStuckAvatarsHandler(&stubAvatars{}, &stubTransactor{},
		fixedClock{now: testNow})

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "recover without a threshold",
			call: func() error {
				_, err := recoverer.Handle(context.Background(),
					RecoverStaleUploadsCommand{Limit: 10})

				return err
			},
		},
		{
			name: "recover without a limit",
			call: func() error {
				_, err := recoverer.Handle(context.Background(),
					RecoverStaleUploadsCommand{StaleAfter: time.Minute})

				return err
			},
		},
		{
			name: "fail without a threshold",
			call: func() error {
				_, err := fail.Handle(context.Background(),
					FailStuckAvatarsCommand{Limit: 10})

				return err
			},
		},
		{
			name: "fail without a limit",
			call: func() error {
				_, err := fail.Handle(context.Background(),
					FailStuckAvatarsCommand{StaleAfter: time.Minute})

				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			require.ErrorIs(t, test.call(), ErrInvalidCommand,
				"a job without a threshold would rewrite the whole table every tick")
		})
	}
}
