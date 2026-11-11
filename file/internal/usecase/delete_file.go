package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

type DeleteObjectsCommand struct {
	FileID    domain.FileID
	Locations []domain.Location
}

type variantLocationLister interface {
	LocationsByFileID(ctx context.Context, id domain.FileID) ([]domain.Location, error)
}

type deleteObjectsEnqueuer interface {
	EnqueueDeleteObjects(ctx context.Context, cmd DeleteObjectsCommand) error
}

type DeleteFileCommand struct {
	FileID domain.FileID
}

type DeleteFileResult struct {
	Status domain.FileStatus
}

type DeleteFileHandler struct {
	files    fileLoaderSaver
	variants variantLocationLister
	enqueuer deleteObjectsEnqueuer
	tx       transactor
	clock    clock
}

func NewDeleteFileHandler(
	files fileLoaderSaver,
	variants variantLocationLister,
	enqueuer deleteObjectsEnqueuer,
	tx transactor,
	clock clock,
) *DeleteFileHandler {
	return &DeleteFileHandler{
		files:    files,
		variants: variants,
		enqueuer: enqueuer,
		tx:       tx,
		clock:    clock,
	}
}

func (h *DeleteFileHandler) Handle(
	ctx context.Context,
	cmd DeleteFileCommand,
) (DeleteFileResult, error) {
	if cmd.FileID.IsZero() {
		return DeleteFileResult{}, fmt.Errorf("%w: file id is required", ErrInvalidCommand)
	}

	file, err := h.files.ByID(ctx, cmd.FileID)
	if err != nil {
		return DeleteFileResult{}, err
	}

	if file.IsDeleted() {
		return DeleteFileResult{Status: file.Status()}, nil
	}

	locations, err := h.variants.LocationsByFileID(ctx, file.ID())
	if err != nil {
		return DeleteFileResult{}, err
	}

	locations = append(locations, file.Location())

	if err := file.MarkForDeletion(h.clock.Now()); err != nil {
		return DeleteFileResult{}, err
	}

	err = h.tx.InTx(ctx, func(ctx context.Context) error {
		if err := h.files.Save(ctx, &file); err != nil {
			return err
		}

		return h.enqueuer.EnqueueDeleteObjects(ctx, DeleteObjectsCommand{
			FileID:    file.ID(),
			Locations: locations,
		})
	})

	if errors.Is(err, ErrStaleVersion) {
		current, reloadErr := h.files.ByID(ctx, cmd.FileID)
		if reloadErr != nil {
			return DeleteFileResult{}, reloadErr
		}

		return DeleteFileResult{Status: current.Status()}, nil
	}

	if err != nil {
		return DeleteFileResult{}, err
	}

	return DeleteFileResult{Status: file.Status()}, nil
}
