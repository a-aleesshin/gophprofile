package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

type FileView struct {
	ID           domain.FileID
	Namespace    domain.Namespace
	OwnerRef     string
	OriginalName string
	MimeType     string
	SizeBytes    int64
	Width        int32
	Height       int32
	Status       domain.FileStatus
	RejectReason string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type VariantView struct {
	Name      string
	Ext       string
	MimeType  string
	Width     int32
	Height    int32
	SizeBytes int64
	ETag      string
	CreatedAt time.Time
}

type fileViewLoader interface {
	ViewsByIDs(ctx context.Context, ids []domain.FileID) ([]FileView, error)
}

type variantViewLister interface {
	ViewsByFileIDs(
		ctx context.Context,
		ids []domain.FileID,
	) (map[domain.FileID][]VariantView, error)
}

type GetFileCommand struct {
	FileID domain.FileID
}

type GetFileResult struct {
	File     FileView
	Variants []VariantView
}

type GetFileHandler struct {
	files    fileViewLoader
	variants variantViewLister
}

func NewGetFileHandler(files fileViewLoader, variants variantViewLister) *GetFileHandler {
	return &GetFileHandler{files: files, variants: variants}
}

func (h *GetFileHandler) Handle(
	ctx context.Context,
	cmd GetFileCommand,
) (GetFileResult, error) {
	if cmd.FileID.IsZero() {
		return GetFileResult{}, fmt.Errorf("%w: file id is required", ErrInvalidCommand)
	}

	ids := []domain.FileID{cmd.FileID}

	files, err := h.files.ViewsByIDs(ctx, ids)
	if err != nil {
		return GetFileResult{}, err
	}

	if len(files) == 0 {
		return GetFileResult{}, fmt.Errorf("%w: %s", domain.ErrFileNotFound, cmd.FileID.String())
	}

	variants, err := h.variants.ViewsByFileIDs(ctx, ids)
	if err != nil {
		return GetFileResult{}, err
	}

	return GetFileResult{File: files[0], Variants: variants[cmd.FileID]}, nil
}
