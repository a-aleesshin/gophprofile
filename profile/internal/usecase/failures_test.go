package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

var errInfrastructure = errors.New("postgres is unreachable")

func TestInfrastructureFailuresReachTheCaller(t *testing.T) {
	t.Parallel()

	user := testUserID(t, "user-1")

	tests := []struct {
		name string
		call func(*testing.T) error
	}{
		{
			name: "start upload cannot read the profile",
			call: func(t *testing.T) error {
				h := newStartUploadHarness(t, testUploadPolicy())
				h.profiles.byIDErr = errInfrastructure

				_, err := h.handler.Handle(context.Background(), testStartUploadCommand(t))

				return err
			},
		},
		{
			name: "start upload cannot count the avatars",
			call: func(t *testing.T) error {
				h := newStartUploadHarness(t, testUploadPolicy())
				h.avatars.countErr = errInfrastructure

				_, err := h.handler.Handle(context.Background(), testStartUploadCommand(t))

				return err
			},
		},
		{
			name: "start upload cannot write the rows",
			call: func(t *testing.T) error {
				h := newStartUploadHarness(t, testUploadPolicy())
				h.avatars.saveErr = errInfrastructure

				_, err := h.handler.Handle(context.Background(), testStartUploadCommand(t))

				return err
			},
		},
		{
			name: "complete upload cannot read the avatar",
			call: func(t *testing.T) error {
				h := newCompleteUploadHarness(t, testPendingAvatar(t, user))
				h.avatars.byIDErr = errInfrastructure

				_, err := h.handler.Handle(context.Background(), CompleteUploadCommand{
					AvatarID: h.avatar.ID(), UserID: user,
				})

				return err
			},
		},
		{
			name: "complete upload cannot reach the file service",
			call: func(t *testing.T) error {
				h := newCompleteUploadHarness(t, testPendingAvatar(t, user))
				h.confirmer.err = errInfrastructure

				_, err := h.handler.Handle(context.Background(), CompleteUploadCommand{
					AvatarID: h.avatar.ID(), UserID: user,
				})

				return err
			},
		},
		{
			name: "complete upload cannot save",
			call: func(t *testing.T) error {
				h := newCompleteUploadHarness(t, testPendingAvatar(t, user))
				h.avatars.saveErr = errInfrastructure

				_, err := h.handler.Handle(context.Background(), CompleteUploadCommand{
					AvatarID: h.avatar.ID(), UserID: user,
				})

				return err
			},
		},
		{
			name: "delete avatar cannot read the profile",
			call: func(t *testing.T) error {
				h := newDeleteAvatarHarness(t, testProcessingAvatar(t, user), false)
				h.profiles.byIDErr = errInfrastructure

				_, err := h.handler.Handle(context.Background(), DeleteAvatarCommand{
					AvatarID: h.avatar.ID(), UserID: user,
				})

				return err
			},
		},
		{
			name: "delete avatar cannot publish the fact",
			call: func(t *testing.T) error {
				h := newDeleteAvatarHarness(t, testProcessingAvatar(t, user), false)
				h.publisher.err = errInfrastructure

				_, err := h.handler.Handle(context.Background(), DeleteAvatarCommand{
					AvatarID: h.avatar.ID(), UserID: user,
				})

				return err
			},
		},
		{
			name: "processed cannot read the avatar",
			call: func(t *testing.T) error {
				h := newFactsHarness(t, testProcessingAvatar(t, user), false)
				h.avatars.byIDErr = errInfrastructure

				_, err := h.processed.Handle(context.Background(), FileProcessedFact{
					FileID: h.avatar.FileID(), Snapshot: testSnapshot(t),
					Variants: testVariants(t),
				})

				return err
			},
		},
		{
			name: "processed cannot read the profile",
			call: func(t *testing.T) error {
				h := newFactsHarness(t, testProcessingAvatar(t, user), false)
				h.profiles.byIDErr = errInfrastructure

				_, err := h.processed.Handle(context.Background(), FileProcessedFact{
					FileID: h.avatar.FileID(), Snapshot: testSnapshot(t),
					Variants: testVariants(t),
				})

				return err
			},
		},
		{
			name: "rejected cannot save",
			call: func(t *testing.T) error {
				h := newFactsHarness(t, testProcessingAvatar(t, user), false)
				h.avatars.saveErr = errInfrastructure

				_, err := h.rejected.Handle(context.Background(), FileRejectedFact{
					FileID: h.avatar.FileID(), Reason: "decode_failed",
				})

				return err
			},
		},
		{
			name: "deleted cannot save",
			call: func(t *testing.T) error {
				h := newFactsHarness(t, testProcessingAvatar(t, user), true)
				h.avatars.saveErr = errInfrastructure

				_, err := h.deleted.Handle(context.Background(), FileDeletedFact{
					FileID: h.avatar.FileID(),
				})

				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Act
			err := test.call(t)

			// Assert
			require.ErrorIs(t, err, errInfrastructure,
				"an infrastructure failure must be retried, so it must not be swallowed")
		})
	}
}

func TestMissingAvatarIsReportedByEveryWriter(t *testing.T) {
	t.Parallel()

	user := testUserID(t, "user-1")

	absent, err := domain.NewAvatarID()
	require.NoError(t, err)

	// Act
	deleteHarness := newDeleteAvatarHarness(t, testProcessingAvatar(t, user), false)

	_, deleteErr := deleteHarness.handler.Handle(context.Background(), DeleteAvatarCommand{
		AvatarID: absent, UserID: user,
	})

	// Assert
	require.ErrorIs(t, deleteErr, domain.ErrAvatarNotFound)
	assert.Empty(t, deleteHarness.publisher.facts)
}
