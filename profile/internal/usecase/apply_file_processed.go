package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type avatarByFileLoaderSaver interface {
	ByFileID(ctx context.Context, fileID domain.FileRef) (domain.Avatar, error)
	Save(ctx context.Context, avatar *domain.Avatar) error
}

type FileProcessedFact struct {
	FileID   domain.FileRef
	Snapshot domain.FileSnapshot
	Variants []domain.Variant
}

type ApplyFactResult struct {
	Status  domain.AvatarStatus
	Skipped bool
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

	if err := profile.SetCurrentAvatar(avatar.ID(), now); err != nil {
		return ApplyFactResult{}, err
	}

	err = h.tx.InTx(ctx, func(ctx context.Context) error {
		if err := h.profiles.Save(ctx, &profile); err != nil {
			return err
		}

		return h.avatars.Save(ctx, &avatar)
	})

	if errors.Is(err, ErrStaleVersion) {
		return ApplyFactResult{Skipped: true}, nil
	}

	if err != nil {
		return ApplyFactResult{}, err
	}

	return ApplyFactResult{Status: avatar.Status()}, nil
}
