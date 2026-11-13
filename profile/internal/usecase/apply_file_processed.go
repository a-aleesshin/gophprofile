package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type avatarByFileLoaderSaver interface {
	ByID(ctx context.Context, id domain.AvatarID) (domain.Avatar, error)
	ByFileID(ctx context.Context, fileID domain.FileRef) (domain.Avatar, error)
	Save(ctx context.Context, avatar *domain.Avatar) error
}

type FileProcessedFact struct {
	FileID   domain.FileRef
	Snapshot domain.FileSnapshot
	Variants []domain.Variant
}

type ApplyFactResult struct {
	Status   domain.AvatarStatus
	Skipped  bool
	Promoted bool
}

type ApplyFileProcessedHandler struct {
	avatars  avatarByFileLoaderSaver
	profiles profileLoaderSaver
	tx       transactor
	clock    clock
}

func NewApplyFileProcessedHandler(
	avatars avatarByFileLoaderSaver,
	profiles profileLoaderSaver,
	tx transactor,
	clock clock,
) *ApplyFileProcessedHandler {
	return &ApplyFileProcessedHandler{
		avatars:  avatars,
		profiles: profiles,
		tx:       tx,
		clock:    clock,
	}
}

func (h *ApplyFileProcessedHandler) Handle(
	ctx context.Context,
	fact FileProcessedFact,
) (ApplyFactResult, error) {
	switch {
	case fact.FileID.IsZero():
		return ApplyFactResult{}, fmt.Errorf("%w: file id is required", ErrInvalidCommand)
	case fact.Snapshot.IsZero():
		return ApplyFactResult{}, fmt.Errorf("%w: snapshot is required", ErrInvalidCommand)
	case len(fact.Variants) == 0:
		return ApplyFactResult{}, fmt.Errorf("%w: variants are required", ErrInvalidCommand)
	}

	avatar, err := h.avatars.ByFileID(ctx, fact.FileID)

	if errors.Is(err, domain.ErrAvatarNotFound) {
		return ApplyFactResult{Skipped: true}, nil
	}

	if err != nil {
		return ApplyFactResult{}, err
	}

	now := h.clock.Now()

	if err := avatar.MarkReady(fact.Snapshot, fact.Variants, now); err != nil {
		if errors.Is(err, domain.ErrForbiddenAvatarTransition) {
			return ApplyFactResult{Status: avatar.Status(), Skipped: true}, nil
		}

		return ApplyFactResult{}, err
	}

	profile, err := h.profiles.ByUserID(ctx, avatar.UserID())
	if err != nil {
		return ApplyFactResult{}, err
	}

	promote, err := h.promotes(ctx, profile, avatar)
	if err != nil {
		return ApplyFactResult{}, err
	}

	if promote {
		if err := profile.SetCurrentAvatar(avatar.ID(), now); err != nil {
			return ApplyFactResult{}, err
		}
	}

	if err := h.tx.InTx(ctx, func(ctx context.Context) error {
		if promote {
			if err := h.profiles.Save(ctx, &profile); err != nil {
				return err
			}
		}

		return h.avatars.Save(ctx, &avatar)
	}); err != nil {
		return ApplyFactResult{}, err
	}

	return ApplyFactResult{Status: avatar.Status(), Promoted: promote}, nil
}

func (h *ApplyFileProcessedHandler) promotes(
	ctx context.Context,
	profile domain.Profile,
	avatar domain.Avatar,
) (bool, error) {
	if profile.CurrentAvatarID().IsZero() {
		return true, nil
	}

	if profile.CurrentAvatarID().Equal(avatar.ID()) {
		return false, nil
	}

	current, err := h.avatars.ByID(ctx, profile.CurrentAvatarID())

	if errors.Is(err, domain.ErrAvatarNotFound) {
		return true, nil
	}

	if err != nil {
		return false, err
	}

	if current.IsDeleted() {
		return true, nil
	}

	return avatar.IsNewerThan(current), nil
}
