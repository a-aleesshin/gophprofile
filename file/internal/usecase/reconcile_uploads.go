package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

type expiredUploadLister interface {
	PendingExpiredBefore(
		ctx context.Context,
		before time.Time,
		limit int,
	) ([]domain.Upload, error)
}

type uploadSaver interface {
	Save(ctx context.Context, upload *domain.Upload) error
}

type uploadCompleter interface {
	Handle(ctx context.Context, cmd CompleteUploadCommand) (CompleteUploadResult, error)
}

type ReconcileUploadsCommand struct {
	Limit int
}

type ReconcileUploadsResult struct {
	Completed int
	Expired   int
	Failed    int
}

type ReconcileUploadsHandler struct {
	uploads   expiredUploadLister
	saver     uploadSaver
	objects   objectHeader
	completer uploadCompleter
	clock     clock
}

func NewReconcileUploadsHandler(
	uploads expiredUploadLister,
	saver uploadSaver,
	objects objectHeader,
	completer uploadCompleter,
	clock clock,
) *ReconcileUploadsHandler {
	return &ReconcileUploadsHandler{
		uploads:   uploads,
		saver:     saver,
		objects:   objects,
		completer: completer,
		clock:     clock,
	}
}

func (h *ReconcileUploadsHandler) Handle(
	ctx context.Context,
	cmd ReconcileUploadsCommand,
) (ReconcileUploadsResult, error) {
	if cmd.Limit <= 0 {
		return ReconcileUploadsResult{}, fmt.Errorf("%w: limit must be positive", ErrInvalidCommand)
	}

	now := h.clock.Now()

	uploads, err := h.uploads.PendingExpiredBefore(ctx, now, cmd.Limit)
	if err != nil {
		return ReconcileUploadsResult{}, err
	}

	var result ReconcileUploadsResult

	for _, upload := range uploads {
		_, headErr := h.objects.Head(ctx, upload.Location())

		switch {
		case headErr == nil:
			if _, completeErr := h.completer.Handle(ctx, CompleteUploadCommand{
				UploadID: upload.ID(),
			}); completeErr != nil {
				result.Failed++

				continue
			}

			result.Completed++
		case errors.Is(headErr, ErrObjectNotFound):
			if expireErr := upload.Expire(now); expireErr != nil {
				result.Failed++

				continue
			}

			if saveErr := h.saver.Save(ctx, &upload); saveErr != nil {
				result.Failed++

				continue
			}

			result.Expired++
		default:
			result.Failed++
		}
	}

	return result, nil
}
