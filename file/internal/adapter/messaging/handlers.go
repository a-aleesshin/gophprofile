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

type fileProcessor interface {
	Handle(
		ctx context.Context,
		cmd usecase.ProcessFileCommand,
	) (usecase.ProcessFileResult, error)
}

type objectDeleter interface {
	Handle(
		ctx context.Context,
		cmd usecase.DeleteObjectsCommand,
	) (usecase.DeleteObjectsResult, error)
}

func CommandHandlers(processor fileProcessor, deleter objectDeleter) map[string]inbox.Handler {
	return map[string]inbox.Handler{
		TypeProcessFile: inbox.Repeatable(func(ctx context.Context, message outbox.Envelope) error {
			var data processFileData

			if err := json.Unmarshal(message.Data, &data); err != nil {
				return fmt.Errorf("%w: decode %s: %w", inbox.ErrUnprocessable, message.Type, err)
			}

			fileID, err := domain.ParseFileID(data.FileID)
			if err != nil {
				return fmt.Errorf("%w: file id: %w", inbox.ErrUnprocessable, err)
			}

			stagingKey, err := domain.RestoreLocation(data.StagingKey)
			if err != nil {
				return fmt.Errorf("%w: staging key: %w", inbox.ErrUnprocessable, err)
			}

			variants := make([]domain.VariantName, 0, len(data.Variants))

			for _, raw := range data.Variants {
				variant, variantErr := domain.NewVariantName(raw)
				if variantErr != nil {
					return fmt.Errorf("%w: variant %q: %w",
						inbox.ErrUnprocessable, raw, variantErr)
				}

				variants = append(variants, variant)
			}

			_, err = processor.Handle(ctx, usecase.ProcessFileCommand{
				FileID:     fileID,
				StagingKey: stagingKey,
				Variants:   variants,
			})

			return unprocessableIfInvalid(err)
		}),

		TypeDeleteObjects: inbox.Repeatable(func(ctx context.Context, message outbox.Envelope) error {
			var data deleteObjectsData

			if err := json.Unmarshal(message.Data, &data); err != nil {
				return fmt.Errorf("%w: decode %s: %w", inbox.ErrUnprocessable, message.Type, err)
			}

			fileID, err := domain.ParseFileID(data.FileID)
			if err != nil {
				return fmt.Errorf("%w: file id: %w", inbox.ErrUnprocessable, err)
			}

			locations := make([]domain.Location, 0, len(data.StorageKeys))

			for _, raw := range data.StorageKeys {
				location, locationErr := domain.RestoreLocation(raw)
				if locationErr != nil {
					return fmt.Errorf("%w: storage key %q: %w",
						inbox.ErrUnprocessable, raw, locationErr)
				}

				locations = append(locations, location)
			}

			_, err = deleter.Handle(ctx, usecase.DeleteObjectsCommand{
				FileID:    fileID,
				Locations: locations,
			})

			return unprocessableIfInvalid(err)
		}),
	}
}

func unprocessableIfInvalid(err error) error {
	if errors.Is(err, usecase.ErrInvalidCommand) {
		return fmt.Errorf("%w: %w", inbox.ErrUnprocessable, err)
	}

	return err
}
