package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

type staleFileLister interface {
	StaleByStatus(
		ctx context.Context,
		statuses []domain.FileStatus,
		updatedBefore time.Time,
		limit int,
	) ([]domain.File, error)
}

type fileSaver interface {
	Save(ctx context.Context, file *domain.File) error
}

type rejectedFactPublisher interface {
	PublishFileRejected(ctx context.Context, fact FileRejectedFact) error
}

type ReapStaleFilesCommand struct {
	Limit      int
	StaleAfter time.Duration
}

type ReapStaleFilesResult struct {
	Rejected int
	Requeued int
	Failed   int
}

type ReapStaleFilesHandler struct {
	files     staleFileLister
	saver     fileSaver
	variants  variantLocationLister
	publisher rejectedFactPublisher
	enqueuer  deleteObjectsEnqueuer
	tx        transactor
	clock     clock
}

func NewReapStaleFilesHandler(
	files staleFileLister,
	saver fileSaver,
	variants variantLocationLister,
	publisher rejectedFactPublisher,
	enqueuer deleteObjectsEnqueuer,
	tx transactor,
	clock clock,
) *ReapStaleFilesHandler {
	return &ReapStaleFilesHandler{
		files:     files,
		saver:     saver,
		variants:  variants,
		publisher: publisher,
		enqueuer:  enqueuer,
		tx:        tx,
		clock:     clock,
	}
}

func (h *ReapStaleFilesHandler) Handle(
	ctx context.Context,
	cmd ReapStaleFilesCommand,
) (ReapStaleFilesResult, error) {
	switch {
	case cmd.Limit <= 0:
		return ReapStaleFilesResult{}, fmt.Errorf("%w: limit must be positive", ErrInvalidCommand)
	case cmd.StaleAfter <= 0:
		return ReapStaleFilesResult{}, fmt.Errorf(
			"%w: stale after must be positive", ErrInvalidCommand,
		)
	}

	now := h.clock.Now()

	files, err := h.files.StaleByStatus(
		ctx,
		[]domain.FileStatus{domain.FileStatusProcessing, domain.FileStatusDeleting},
		now.Add(-cmd.StaleAfter),
		cmd.Limit,
	)
	if err != nil {
		return ReapStaleFilesResult{}, err
	}

	var result ReapStaleFilesResult

	for _, file := range files {
		locations, listErr := h.variants.LocationsByFileID(ctx, file.ID())
		if listErr != nil {
			result.Failed++

			continue
		}

		locations = append(locations, file.Location())

		if file.Status().Equal(domain.FileStatusDeleting) {
			if enqueueErr := h.enqueuer.EnqueueDeleteObjects(ctx, DeleteObjectsCommand{
				FileID:    file.ID(),
				Locations: locations,
			}); enqueueErr != nil {
				result.Failed++

				continue
			}

			result.Requeued++

			continue
		}

		if rejectErr := file.Reject(domain.RejectReasonInternalError, now); rejectErr != nil {
			result.Failed++

			continue
		}

		if deleteErr := file.MarkForDeletion(now); deleteErr != nil {
			result.Failed++

			continue
		}

		txErr := h.tx.InTx(ctx, func(ctx context.Context) error {
			if saveErr := h.saver.Save(ctx, &file); saveErr != nil {
				return saveErr
			}

			if publishErr := h.publisher.PublishFileRejected(ctx, FileRejectedFact{
				FileID:   file.ID(),
				OwnerRef: file.OwnerRef(),
				Reason:   domain.RejectReasonInternalError,
			}); publishErr != nil {
				return publishErr
			}

			return h.enqueuer.EnqueueDeleteObjects(ctx, DeleteObjectsCommand{
				FileID:    file.ID(),
				Locations: locations,
			})
		})
		if txErr != nil {
			result.Failed++

			continue
		}

		result.Rejected++
	}

	return result, nil
}
