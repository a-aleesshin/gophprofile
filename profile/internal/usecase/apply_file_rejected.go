package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type FileRejectedFact struct {
	FileID domain.FileRef
	Reason string
}

type ApplyFileRejectedHandler struct {
	avatars avatarByFileLoaderSaver
	tx      transactor
	clock   clock
}

func NewApplyFileRejectedHandler(
	avatars avatarByFileLoaderSaver,
	tx transactor,
	clock clock,
) *ApplyFileRejectedHandler {
	return &ApplyFileRejectedHandler{avatars: avatars, tx: tx, clock: clock}
}

func (h *ApplyFileRejectedHandler) Handle(
	ctx context.Context,
	fact FileRejectedFact,
) (ApplyFactResult, error) {
	switch {
	case fact.FileID.IsZero():
		return ApplyFactResult{}, fmt.Errorf("%w: file id is required", ErrInvalidCommand)
	case fact.Reason == "":
		return ApplyFactResult{}, fmt.Errorf("%w: reason is required", ErrInvalidCommand)
	}

	avatar, err := h.avatars.ByFileID(ctx, fact.FileID)

	if errors.Is(err, domain.ErrAvatarNotFound) {
		return ApplyFactResult{Skipped: true}, nil
	}

	if err != nil {
		return ApplyFactResult{}, err
	}

	if err := avatar.MarkFailed(fact.Reason, h.clock.Now()); err != nil {
		if errors.Is(err, domain.ErrForbiddenAvatarTransition) {
			return ApplyFactResult{Status: avatar.Status(), Skipped: true}, nil
		}

		return ApplyFactResult{}, err
	}

	if err := h.tx.InTx(ctx, func(ctx context.Context) error {
		return h.avatars.Save(ctx, &avatar)
	}); err != nil {
		return ApplyFactResult{}, err
	}

	return ApplyFactResult{Status: avatar.Status()}, nil
}
