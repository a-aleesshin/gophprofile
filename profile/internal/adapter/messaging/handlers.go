package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/a-aleesshin/gophprofile/internal/inbox"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

const DestinationFileEvents = "gophprofile.file.v1"

const (
	TypeFileProcessed = "FileProcessed"
	TypeFileRejected  = "FileRejected"
	TypeFileDeleted   = "FileDeleted"
)

const variantNameOriginal = "original"

type variantData struct {
	Name     string `json:"name"`
	Ext      string `json:"ext"`
	MimeType string `json:"mime_type"`
	Width    int32  `json:"width"`
	Height   int32  `json:"height"`
}

type fileProcessedData struct {
	FileID       string        `json:"file_id"`
	OriginalName string        `json:"original_name"`
	MimeType     string        `json:"mime_type"`
	SizeBytes    int64         `json:"size_bytes"`
	Width        int32         `json:"width"`
	Height       int32         `json:"height"`
	Variants     []variantData `json:"variants"`
}

type fileRejectedData struct {
	FileID string `json:"file_id"`
	Reason string `json:"reason"`
}

type fileDeletedData struct {
	FileID string `json:"file_id"`
}

type processedApplier interface {
	Handle(
		ctx context.Context,
		fact usecase.FileProcessedFact,
	) (usecase.ApplyFactResult, error)
}

type rejectedApplier interface {
	Handle(
		ctx context.Context,
		fact usecase.FileRejectedFact,
	) (usecase.ApplyFactResult, error)
}

type deletedApplier interface {
	Handle(
		ctx context.Context,
		fact usecase.FileDeletedFact,
	) (usecase.ApplyFactResult, error)
}

func FactHandlers(
	processed processedApplier,
	rejected rejectedApplier,
	deleted deletedApplier,
) map[string]inbox.Handler {
	return map[string]inbox.Handler{
		TypeFileProcessed: inbox.Transactional(
			func(ctx context.Context, message outbox.Envelope) error {
				var data fileProcessedData

				if err := json.Unmarshal(message.Data, &data); err != nil {
					return fmt.Errorf("%w: decode %s: %w",
						inbox.ErrUnprocessable, message.Type, err)
				}

				fileID, err := domain.ParseFileRef(data.FileID)
				if err != nil {
					return fmt.Errorf("%w: file id: %w", inbox.ErrUnprocessable, err)
				}

				snapshot, err := domain.NewFileSnapshot(domain.NewFileSnapshotParams{
					FileName:  data.OriginalName,
					MimeType:  data.MimeType,
					SizeBytes: data.SizeBytes,
					Width:     data.Width,
					Height:    data.Height,
				})
				if err != nil {
					return fmt.Errorf("%w: snapshot: %w", inbox.ErrUnprocessable, err)
				}

				variants := make([]domain.Variant, 0, len(data.Variants))

				for _, stored := range data.Variants {
					if stored.Name == variantNameOriginal {
						continue
					}

					variant, variantErr := domain.NewVariant(domain.NewVariantParams{
						Name:     stored.Name,
						Ext:      stored.Ext,
						MimeType: stored.MimeType,
						Width:    stored.Width,
						Height:   stored.Height,
					})
					if variantErr != nil {
						return fmt.Errorf("%w: variant %q: %w",
							inbox.ErrUnprocessable, stored.Name, variantErr)
					}

					variants = append(variants, variant)
				}

				if len(variants) == 0 {
					return fmt.Errorf("%w: %s carries no servable variant",
						inbox.ErrUnprocessable, message.Type)
				}

				_, err = processed.Handle(ctx, usecase.FileProcessedFact{
					FileID:   fileID,
					Snapshot: snapshot,
					Variants: variants,
				})

				return unprocessableIfPermanent(err)
			}),

		TypeFileRejected: inbox.Transactional(
			func(ctx context.Context, message outbox.Envelope) error {
				var data fileRejectedData

				if err := json.Unmarshal(message.Data, &data); err != nil {
					return fmt.Errorf("%w: decode %s: %w",
						inbox.ErrUnprocessable, message.Type, err)
				}

				fileID, err := domain.ParseFileRef(data.FileID)
				if err != nil {
					return fmt.Errorf("%w: file id: %w", inbox.ErrUnprocessable, err)
				}

				if data.Reason == "" {
					return fmt.Errorf("%w: %s carries no reason",
						inbox.ErrUnprocessable, message.Type)
				}

				_, err = rejected.Handle(ctx, usecase.FileRejectedFact{
					FileID: fileID,
					Reason: data.Reason,
				})

				return unprocessableIfPermanent(err)
			}),

		TypeFileDeleted: inbox.Transactional(
			func(ctx context.Context, message outbox.Envelope) error {
				var data fileDeletedData

				if err := json.Unmarshal(message.Data, &data); err != nil {
					return fmt.Errorf("%w: decode %s: %w",
						inbox.ErrUnprocessable, message.Type, err)
				}

				fileID, err := domain.ParseFileRef(data.FileID)
				if err != nil {
					return fmt.Errorf("%w: file id: %w", inbox.ErrUnprocessable, err)
				}

				_, err = deleted.Handle(ctx, usecase.FileDeletedFact{FileID: fileID})

				return unprocessableIfPermanent(err)
			}),
	}
}

func unprocessableIfPermanent(err error) error {
	switch {
	case errors.Is(err, usecase.ErrInvalidCommand),
		errors.Is(err, domain.ErrInvalidAvatar),
		errors.Is(err, domain.ErrInvalidSnapshot),
		errors.Is(err, domain.ErrInvalidVariant),
		errors.Is(err, domain.ErrInvalidProfile):
		return fmt.Errorf("%w: %w", inbox.ErrUnprocessable, err)
	default:
		return err
	}
}
