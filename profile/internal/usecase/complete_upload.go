package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type CompleteUploadConfirmation struct {
	FileID string
}

type avatarLoaderSaver interface {
	ByID(ctx context.Context, id domain.AvatarID) (domain.Avatar, error)
	Save(ctx context.Context, avatar *domain.Avatar) error
}

type uploadConfirmer interface {
	CompleteUpload(
		ctx context.Context,
		uploadID string,
	) (CompleteUploadConfirmation, error)
}

type CompleteUploadCommand struct {
	AvatarID domain.AvatarID
	UserID   domain.UserID
}

type CompleteUploadResult struct {
	AvatarID domain.AvatarID
	Status   domain.AvatarStatus
}

type CompleteUploadHandler struct {
	avatars avatarLoaderSaver
	uploads uploadConfirmer
	tx      transactor
	clock   clock
}

func NewCompleteUploadHandler(
	avatars avatarLoaderSaver,
	uploads uploadConfirmer,
	tx transactor,
	clock clock,
) *CompleteUploadHandler {
	return &CompleteUploadHandler{avatars: avatars, uploads: uploads, tx: tx, clock: clock}
}

func (h *CompleteUploadHandler) Handle(
	ctx context.Context,
	cmd CompleteUploadCommand,
) (CompleteUploadResult, error) {
	switch {
	case cmd.AvatarID.IsZero():
		return CompleteUploadResult{}, fmt.Errorf("%w: avatar id is required", ErrInvalidCommand)
	case cmd.UserID.IsZero():
		return CompleteUploadResult{}, fmt.Errorf("%w: user id is required", ErrInvalidCommand)
	}

	avatar, err := h.avatars.ByID(ctx, cmd.AvatarID)
	if err != nil {
		return CompleteUploadResult{}, err
	}

	if !avatar.OwnedBy(cmd.UserID) {
		return CompleteUploadResult{}, fmt.Errorf("%w: %s", ErrForbidden, cmd.AvatarID.String())
	}

	if !avatar.Status().Equal(domain.AvatarStatusPending) {
		return CompleteUploadResult{AvatarID: avatar.ID(), Status: avatar.Status()}, nil
	}

	confirmation, err := h.uploads.CompleteUpload(ctx, avatar.UploadID().String())
	if err != nil {
		return CompleteUploadResult{}, err
	}

	fileRef, err := domain.ParseFileRef(confirmation.FileID)
	if err != nil {
		return CompleteUploadResult{}, err
	}

	if err := avatar.StartProcessing(fileRef, h.clock.Now()); err != nil {
		return CompleteUploadResult{}, err
	}

	err = h.tx.InTx(ctx, func(ctx context.Context) error {
		return h.avatars.Save(ctx, &avatar)
	})

	if errors.Is(err, ErrStaleVersion) {
		current, reloadErr := h.avatars.ByID(ctx, cmd.AvatarID)
		if reloadErr != nil {
			return CompleteUploadResult{}, reloadErr
		}

		return CompleteUploadResult{AvatarID: current.ID(), Status: current.Status()}, nil
	}

	if err != nil {
		return CompleteUploadResult{}, err
	}

	return CompleteUploadResult{AvatarID: avatar.ID(), Status: avatar.Status()}, nil
}
