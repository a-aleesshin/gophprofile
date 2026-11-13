package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type AvatarDeletedFact struct {
	AvatarID domain.AvatarID
	UserID   domain.UserID
	FileID   domain.FileRef
}

type profileLoaderSaver interface {
	ByUserID(ctx context.Context, userID domain.UserID) (domain.Profile, error)
	Save(ctx context.Context, profile *domain.Profile) error
}

type deletedFactPublisher interface {
	PublishAvatarDeleted(ctx context.Context, fact AvatarDeletedFact) error
}

type DeleteAvatarCommand struct {
	AvatarID domain.AvatarID
	UserID   domain.UserID
}

type DeleteAvatarResult struct {
	Status domain.AvatarStatus
}

type DeleteAvatarHandler struct {
	avatars   avatarLoaderSaver
	profiles  profileLoaderSaver
	publisher deletedFactPublisher
	tx        transactor
	clock     clock
}

func NewDeleteAvatarHandler(
	avatars avatarLoaderSaver,
	profiles profileLoaderSaver,
	publisher deletedFactPublisher,
	tx transactor,
	clock clock,
) *DeleteAvatarHandler {
	return &DeleteAvatarHandler{
		avatars:   avatars,
		profiles:  profiles,
		publisher: publisher,
		tx:        tx,
		clock:     clock,
	}
}

func (h *DeleteAvatarHandler) Handle(
	ctx context.Context,
	cmd DeleteAvatarCommand,
) (DeleteAvatarResult, error) {
	switch {
	case cmd.AvatarID.IsZero():
		return DeleteAvatarResult{}, fmt.Errorf("%w: avatar id is required", ErrInvalidCommand)
	case cmd.UserID.IsZero():
		return DeleteAvatarResult{}, fmt.Errorf("%w: user id is required", ErrInvalidCommand)
	}

	avatar, err := h.avatars.ByID(ctx, cmd.AvatarID)
	if err != nil {
		return DeleteAvatarResult{}, err
	}

	if !avatar.OwnedBy(cmd.UserID) {
		return DeleteAvatarResult{}, fmt.Errorf("%w: %s", ErrForbidden, cmd.AvatarID.String())
	}

	if avatar.Status().Equal(domain.AvatarStatusDeleted) {
		return DeleteAvatarResult{Status: avatar.Status()}, nil
	}

	profile, err := h.profiles.ByUserID(ctx, cmd.UserID)
	if err != nil {
		return DeleteAvatarResult{}, err
	}

	now := h.clock.Now()

	if err := avatar.MarkDeleted(now); err != nil {
		return DeleteAvatarResult{}, err
	}

	wasCurrent := profile.CurrentAvatarID().Equal(avatar.ID())

	if wasCurrent {
		if err := profile.ClearCurrentAvatar(now); err != nil {
			return DeleteAvatarResult{}, err
		}
	}

	err = h.tx.InTx(ctx, func(ctx context.Context) error {
		if wasCurrent {
			if err := h.profiles.Save(ctx, &profile); err != nil {
				return err
			}
		}

		if err := h.avatars.Save(ctx, &avatar); err != nil {
			return err
		}

		if avatar.FileID().IsZero() {
			return nil
		}

		return h.publisher.PublishAvatarDeleted(ctx, AvatarDeletedFact{
			AvatarID: avatar.ID(),
			UserID:   avatar.UserID(),
			FileID:   avatar.FileID(),
		})
	})

	if errors.Is(err, ErrStaleVersion) {
		current, reloadErr := h.avatars.ByID(ctx, cmd.AvatarID)
		if reloadErr != nil {
			return DeleteAvatarResult{}, reloadErr
		}

		return DeleteAvatarResult{Status: current.Status()}, nil
	}

	if err != nil {
		return DeleteAvatarResult{}, err
	}

	return DeleteAvatarResult{Status: avatar.Status()}, nil
}
