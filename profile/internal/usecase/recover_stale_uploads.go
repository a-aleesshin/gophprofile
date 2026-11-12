package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

const (
	ReasonUploadAbandoned = "the upload was never finished"
	ReasonProcessingStuck = "the file service never reported a result"
	ReasonUploadUnknown   = "the upload ticket is gone"
)

type staleAvatarFinder interface {
	StaleByStatus(
		ctx context.Context,
		statuses []domain.AvatarStatus,
		updatedBefore time.Time,
		limit int,
	) ([]domain.Avatar, error)
	Save(ctx context.Context, avatar *domain.Avatar) error
}

type RecoverStaleUploadsCommand struct {
	StaleAfter time.Duration
	Limit      int
}

type RecoverStaleUploadsResult struct {
	Recovered int
	Failed    int
	Skipped   int
}

type RecoverStaleUploadsHandler struct {
	avatars staleAvatarFinder
	uploads uploadConfirmer
	tx      transactor
	clock   clock
}

func NewRecoverStaleUploadsHandler(
	avatars staleAvatarFinder,
	uploads uploadConfirmer,
	tx transactor,
	clock clock,
) *RecoverStaleUploadsHandler {
	return &RecoverStaleUploadsHandler{
		avatars: avatars,
		uploads: uploads,
		tx:      tx,
		clock:   clock,
	}
}

func (h *RecoverStaleUploadsHandler) Handle(
	ctx context.Context,
	cmd RecoverStaleUploadsCommand,
) (RecoverStaleUploadsResult, error) {
	switch {
	case cmd.StaleAfter <= 0:
		return RecoverStaleUploadsResult{}, fmt.Errorf(
			"%w: stale after must be positive", ErrInvalidCommand)
	case cmd.Limit <= 0:
		return RecoverStaleUploadsResult{}, fmt.Errorf(
			"%w: limit must be positive", ErrInvalidCommand)
	}

	now := h.clock.Now()

	avatars, err := h.avatars.StaleByStatus(ctx,
		[]domain.AvatarStatus{domain.AvatarStatusPending},
		now.Add(-cmd.StaleAfter), cmd.Limit)
	if err != nil {
		return RecoverStaleUploadsResult{}, err
	}

	var result RecoverStaleUploadsResult

	for _, avatar := range avatars {
		reason := ""

		if avatar.UploadID().IsZero() {
			reason = ReasonUploadUnknown
		}

		var confirmation CompleteUploadConfirmation

		if reason == "" {
			confirmation, err = h.uploads.CompleteUpload(ctx, avatar.UploadID().String())

			switch {
			case errors.Is(err, ErrUploadNotFound):
				reason = ReasonUploadUnknown
			case errors.Is(err, ErrUploadNotUsable):
				reason = ReasonUploadAbandoned
			case err != nil:
				result.Skipped++

				continue
			}
		}

		if reason == "" {
			fileRef, parseErr := domain.ParseFileRef(confirmation.FileID)
			if parseErr != nil {
				return RecoverStaleUploadsResult{}, parseErr
			}

			if startErr := avatar.StartProcessing(fileRef, now); startErr != nil {
				return RecoverStaleUploadsResult{}, startErr
			}
		} else if failErr := avatar.MarkFailed(reason, now); failErr != nil {
			return RecoverStaleUploadsResult{}, failErr
		}

		saveErr := h.tx.InTx(ctx, func(ctx context.Context) error {
			return h.avatars.Save(ctx, &avatar)
		})

		switch {
		case errors.Is(saveErr, ErrStaleVersion):
			result.Skipped++
		case saveErr != nil:
			return RecoverStaleUploadsResult{}, saveErr
		case reason == "":
			result.Recovered++
		default:
			result.Failed++
		}
	}

	return result, nil
}
