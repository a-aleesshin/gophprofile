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

type stagingObjects interface {
	Head(ctx context.Context, location domain.Location) (ObjectInfo, error)
	Delete(ctx context.Context, locations ...domain.Location) error
}

type ReconcileUploadsCommand struct {
	Limit int
}

type ReconcileFailure struct {
	UploadID domain.UploadID
	Attempts int
	Err      error
}

type ReconcileUploadsResult struct {
	Completed int
	Expired   int
	Aborted   int
	Failed    int
	Failures  []ReconcileFailure
}

type ReconcileUploadsHandler struct {
	uploads     expiredUploadLister
	saver       uploadSaver
	objects     stagingObjects
	completer   uploadCompleter
	clock       clock
	maxAttempts int
}

func NewReconcileUploadsHandler(
	uploads expiredUploadLister,
	saver uploadSaver,
	objects stagingObjects,
	completer uploadCompleter,
	clock clock,
	maxAttempts int,
) (*ReconcileUploadsHandler, error) {
	if maxAttempts <= 0 {
		return nil, fmt.Errorf(
			"%w: max attempts must be positive, or a poisoned ticket is retried forever",
			ErrInvalidCommand,
		)
	}

	return &ReconcileUploadsHandler{
		uploads:     uploads,
		saver:       saver,
		objects:     objects,
		completer:   completer,
		clock:       clock,
		maxAttempts: maxAttempts,
	}, nil
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
				h.settleFailure(ctx, &upload, now, completeErr, &result)

				continue
			}

			result.Completed++
		case errors.Is(headErr, ErrObjectNotFound):
			if expireErr := h.expire(ctx, &upload, now); expireErr != nil {
				h.settleFailure(ctx, &upload, now, expireErr, &result)

				continue
			}

			result.Expired++
		default:
			h.settleFailure(ctx, &upload, now, headErr, &result)
		}
	}

	return result, nil
}

func (h *ReconcileUploadsHandler) expire(
	ctx context.Context,
	upload *domain.Upload,
	now time.Time,
) error {
	if err := upload.Expire(now); err != nil {
		return err
	}

	return h.saver.Save(ctx, upload)
}

func (h *ReconcileUploadsHandler) settleFailure(
	ctx context.Context,
	upload *domain.Upload,
	now time.Time,
	cause error,
	result *ReconcileUploadsResult,
) {
	outcome, err := h.recordFailure(ctx, upload, now)
	if err != nil {
		cause = errors.Join(cause, err)
	}

	if outcome == uploadAborted {
		result.Aborted++
	} else {
		result.Failed++
	}

	result.Failures = append(result.Failures, ReconcileFailure{
		UploadID: upload.ID(),
		Attempts: upload.Attempts(),
		Err:      cause,
	})
}

type failureOutcome int

const (
	uploadRetained failureOutcome = iota
	uploadAborted
)

func (h *ReconcileUploadsHandler) recordFailure(
	ctx context.Context,
	upload *domain.Upload,
	now time.Time,
) (failureOutcome, error) {
	if err := upload.FailAttempt(now); err != nil {
		return uploadRetained, err
	}

	if upload.Attempts() < h.maxAttempts {
		return uploadRetained, h.saver.Save(ctx, upload)
	}

	if err := h.objects.Delete(ctx, upload.Location()); err != nil {
		return uploadRetained, err
	}

	if err := upload.Abort(now); err != nil {
		return uploadRetained, err
	}

	if err := h.saver.Save(ctx, upload); err != nil {
		return uploadRetained, err
	}

	return uploadAborted, nil
}
