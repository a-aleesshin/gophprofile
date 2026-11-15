package usecase

import (
	"context"
	"fmt"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

const MaxFileBatch = 100

type FileWithVariants struct {
	File     FileView
	Variants []VariantView
}

type GetFilesCommand struct {
	FileIDs []domain.FileID
}

type GetFilesResult struct {
	Files []FileWithVariants
}

type GetFilesHandler struct {
	files    fileViewLoader
	variants variantViewLister
}

func NewGetFilesHandler(files fileViewLoader, variants variantViewLister) *GetFilesHandler {
	return &GetFilesHandler{files: files, variants: variants}
}

func (h *GetFilesHandler) Handle(
	ctx context.Context,
	cmd GetFilesCommand,
) (GetFilesResult, error) {
	switch {
	case len(cmd.FileIDs) == 0:
		return GetFilesResult{}, fmt.Errorf("%w: at least one file id is required", ErrInvalidCommand)
	case len(cmd.FileIDs) > MaxFileBatch:
		return GetFilesResult{}, fmt.Errorf(
			"%w: at most %d file ids per call (got %d)",
			ErrInvalidCommand, MaxFileBatch, len(cmd.FileIDs),
		)
	}

	ids := make([]domain.FileID, 0, len(cmd.FileIDs))
	seen := make(map[domain.FileID]bool, len(cmd.FileIDs))

	for _, id := range cmd.FileIDs {
		if id.IsZero() {
			return GetFilesResult{}, fmt.Errorf("%w: file id is required", ErrInvalidCommand)
		}

		if seen[id] {
			continue
		}

		seen[id] = true
		ids = append(ids, id)
	}

	views, err := h.files.ViewsByIDs(ctx, ids)
	if err != nil {
		return GetFilesResult{}, err
	}

	variants, err := h.variants.ViewsByFileIDs(ctx, ids)
	if err != nil {
		return GetFilesResult{}, err
	}

	byID := make(map[domain.FileID]FileView, len(views))
	for _, view := range views {
		byID[view.ID] = view
	}

	files := make([]FileWithVariants, 0, len(views))

	for _, id := range ids {
		view, found := byID[id]
		if !found {
			continue
		}

		files = append(files, FileWithVariants{File: view, Variants: variants[id]})
	}

	return GetFilesResult{Files: files}, nil
}
