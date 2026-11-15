package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
	"github.com/a-aleesshin/gophprofile/internal/inbox"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
)

type avatarDeletedData struct {
	AvatarID string `json:"avatar_id"`
	UserID   string `json:"user_id"`
	FileID   string `json:"file_id"`
}

type fileDeleter interface {
	Handle(ctx context.Context, cmd usecase.DeleteFileCommand) (usecase.DeleteFileResult, error)
}

func FactHandlers(deleter fileDeleter) map[string]inbox.Handler {
	return map[string]inbox.Handler{
		TypeAvatarDeleted: inbox.Transactional(
			func(ctx context.Context, message outbox.Envelope) error {
				var data avatarDeletedData

				if err := json.Unmarshal(message.Data, &data); err != nil {
					return fmt.Errorf("%w: decode %s: %w",
						inbox.ErrUnprocessable, message.Type, err)
				}

				fileID, err := domain.ParseFileID(data.FileID)
				if err != nil {
					return fmt.Errorf("%w: file id: %w", inbox.ErrUnprocessable, err)
				}

				owner, err := domain.NewOwnerRef(data.UserID)
				if err != nil {
					return fmt.Errorf("%w: user id: %w", inbox.ErrUnprocessable, err)
				}

				_, err = deleter.Handle(ctx, usecase.DeleteFileCommand{
					FileID:   fileID,
					OwnerRef: owner,
				})

				if errors.Is(err, domain.ErrFileNotFound) {
					// profile deleted an avatar whose file we never had, or it is
					// already gone. There is nothing left to delete.
					return nil
				}

				return unprocessableIfPermanent(err)
			}),
	}
}

func unprocessableIfPermanent(err error) error {
	switch {
	case errors.Is(err, usecase.ErrInvalidCommand),
		errors.Is(err, usecase.ErrOwnerMismatch),
		errors.Is(err, domain.ErrForbiddenFileTransition):
		return fmt.Errorf("%w: %w", inbox.ErrUnprocessable, err)
	default:
		return err
	}
}
