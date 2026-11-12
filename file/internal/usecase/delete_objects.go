package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

type FileDeletedFact struct {
	FileID   domain.FileID
	OwnerRef domain.OwnerRef
}

type objectDeleter interface {
	Delete(ctx context.Context, locations ...domain.Location) error
}

type deletedFactPublisher interface {
	PublishFileDeleted(ctx context.Context, fact FileDeletedFact) error
}

type DeleteObjectsResult struct {
	Status domain.FileStatus
}

var ErrFileNotDeleting = errors.New("file is not marked for deletion")

type DeleteObjectsHandler struct {
	files     fileLoaderSaver
	objects   objectDeleter
	publisher deletedFactPublisher
	tx        transactor
	clock     clock
}

func NewDeleteObjectsHandler(
	files fileLoaderSaver,
	objects objectDeleter,
	publisher deletedFactPublisher,
	tx transactor,
	clock clock,
) *DeleteObjectsHandler {
	return &DeleteObjectsHandler{
		files:     files,
		objects:   objects,
		publisher: publisher,
		tx:        tx,
		clock:     clock,
	}
}

func (h *DeleteObjectsHandler) Handle(
	ctx context.Context,
	cmd DeleteObjectsCommand,
) (DeleteObjectsResult, error) {
	switch {
	case cmd.FileID.IsZero():
		return DeleteObjectsResult{}, fmt.Errorf("%w: file id is required", ErrInvalidCommand)
	case len(cmd.Locations) == 0:
		return DeleteObjectsResult{}, fmt.Errorf("%w: locations are required", ErrInvalidCommand)
	}

	file, err := h.files.ByID(ctx, cmd.FileID)
	if err != nil {
		return DeleteObjectsResult{}, err
	}

	if file.Status().Equal(domain.FileStatusDeleted) {
		return DeleteObjectsResult{Status: file.Status()}, nil
	}

	if !file.Status().Equal(domain.FileStatusDeleting) {
		return DeleteObjectsResult{}, fmt.Errorf(
			"%w: %s is %q, so its objects must stay",
			ErrFileNotDeleting, cmd.FileID.String(), file.Status().String(),
		)
	}

	if err := h.objects.Delete(ctx, cmd.Locations...); err != nil {
		return DeleteObjectsResult{}, err
	}

	if err := file.MarkDeleted(h.clock.Now()); err != nil {
		return DeleteObjectsResult{}, err
	}

	err = h.tx.InTx(ctx, func(ctx context.Context) error {
		if err := h.files.Save(ctx, &file); err != nil {
			return err
		}

		return h.publisher.PublishFileDeleted(ctx, FileDeletedFact{
			FileID:   file.ID(),
			OwnerRef: file.OwnerRef(),
		})
	})

	if errors.Is(err, ErrStaleVersion) {
		current, reloadErr := h.files.ByID(ctx, cmd.FileID)
		if reloadErr != nil {
			return DeleteObjectsResult{}, reloadErr
		}

		return DeleteObjectsResult{Status: current.Status()}, nil
	}

	if err != nil {
		return DeleteObjectsResult{}, err
	}

	return DeleteObjectsResult{Status: file.Status()}, nil
}
