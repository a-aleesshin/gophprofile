package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type FileDeletedFact struct {
	FileID domain.FileRef
}

type ApplyFileDeletedHandler struct {
	avatars  avatarByFileLoaderSaver
	profiles profileLoaderSaver
	tx       transactor
	clock    clock
}

func NewApplyFileDeletedHandler(
	avatars avatarByFileLoaderSaver,
	profiles profileLoaderSaver,
	tx transactor,
	clock clock,
) *ApplyFileDeletedHandler {
	return &ApplyFileDeletedHandler{
		avatars:  avatars,
		profiles: profiles,
		tx:       tx,
		clock:    clock,
	}
}

func (h *ApplyFileDeletedHandler) Handle(
	ctx context.Context,
	fact FileDeletedFact,
) (ApplyFactResult, error) {
	if fact.FileID.IsZero() {
		return ApplyFactResult{}, fmt.Errorf("%w: file id is required", ErrInvalidCommand)
	}

	avatar, err := h.avatars.ByFileID(ctx, fact.FileID)

	if errors.Is(err, domain.ErrAvatarNotFound) {
		return ApplyFactResult{Skipped: true}, nil
	}

	if err != nil {
		return ApplyFactResult{}, err
	}

	if avatar.Status().Equal(domain.AvatarStatusDeleted) {
		return ApplyFactResult{Status: avatar.Status(), Skipped: true}, nil
	}

	profile, err := h.profiles.ByUserID(ctx, avatar.UserID())
	if err != nil {
		return ApplyFactResult{}, err
	}

	now := h.clock.Now()

	if err := avatar.MarkDeleted(now); err != nil {
		return ApplyFactResult{}, err
	}

	wasCurrent := profile.CurrentAvatarID().Equal(avatar.ID())

	if wasCurrent {
		if err := profile.ClearCurrentAvatar(now); err != nil {
			return ApplyFactResult{}, err
		}
	}

	if err := h.tx.InTx(ctx, func(ctx context.Context) error {
		if wasCurrent {
			if err := h.profiles.Save(ctx, &profile); err != nil {
				return err
			}
		}

		return h.avatars.Save(ctx, &avatar)
	}); err != nil {
		return ApplyFactResult{}, err
	}

	return ApplyFactResult{Status: avatar.Status()}, nil
}
