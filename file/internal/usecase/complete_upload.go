package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

var (
	ErrStaleVersion   = errors.New("aggregate was modified concurrently")
	ErrObjectNotFound = errors.New("object not found in staging")
	ErrObjectUnusable = errors.New("object in staging is unusable")
	ErrUploadExpired  = errors.New("upload expired")
	ErrSizeExceeded   = errors.New("object exceeds the ticket limit")
)

type CompletePolicy struct {
	AllowedMime domain.MimeSet
	Variants    []domain.VariantName
}

type ObjectInfo struct {
	SizeBytes   int64
	ContentType string
}

type ProcessFileCommand struct {
	FileID     domain.FileID
	StagingKey domain.Location
	Variants   []domain.VariantName
}

type completePolicyProvider interface {
	CompletePolicy(namespace domain.Namespace) (CompletePolicy, error)
}

type uploadLoaderSaver interface {
	ByID(ctx context.Context, id domain.UploadID) (domain.Upload, error)
	Save(ctx context.Context, upload *domain.Upload) error
}

type fileLoaderCreator interface {
	ByID(ctx context.Context, id domain.FileID) (domain.File, error)
	Create(ctx context.Context, file *domain.File) error
}

type objectHeader interface {
	Head(ctx context.Context, location domain.Location) (ObjectInfo, error)
}

type processFileEnqueuer interface {
	EnqueueProcessFile(ctx context.Context, cmd ProcessFileCommand) error
}

type transactor interface {
	InTx(ctx context.Context, fn func(context.Context) error) error
}

type CompleteUploadCommand struct {
	UploadID domain.UploadID
}

type CompleteUploadResult struct {
	FileID domain.FileID
	Status domain.FileStatus
}

type CompleteUploadHandler struct {
	policies completePolicyProvider
	uploads  uploadLoaderSaver
	files    fileLoaderCreator
	objects  objectHeader
	enqueuer processFileEnqueuer
	tx       transactor
	clock    clock
}

func NewCompleteUploadHandler(
	policies completePolicyProvider,
	uploads uploadLoaderSaver,
	files fileLoaderCreator,
	objects objectHeader,
	enqueuer processFileEnqueuer,
	tx transactor,
	clock clock,
) *CompleteUploadHandler {
	return &CompleteUploadHandler{
		policies: policies,
		uploads:  uploads,
		files:    files,
		objects:  objects,
		enqueuer: enqueuer,
		tx:       tx,
		clock:    clock,
	}
}

func (h *CompleteUploadHandler) Handle(
	ctx context.Context,
	cmd CompleteUploadCommand,
) (CompleteUploadResult, error) {
	if cmd.UploadID.IsZero() {
		return CompleteUploadResult{}, fmt.Errorf("%w: upload id is required", ErrInvalidCommand)
	}

	upload, err := h.uploads.ByID(ctx, cmd.UploadID)
	if err != nil {
		return CompleteUploadResult{}, err
	}

	if !upload.IsPending() {
		if !upload.Status().Equal(domain.UploadStatusUploaded) {
			return CompleteUploadResult{}, fmt.Errorf(
				"%w: status %q", ErrUploadNotReusable, upload.Status().String(),
			)
		}

		file, fileErr := h.files.ByID(ctx, upload.FileID())
		if fileErr != nil {
			return CompleteUploadResult{}, fileErr
		}

		return CompleteUploadResult{FileID: file.ID(), Status: file.Status()}, nil
	}

	policy, err := h.policies.CompletePolicy(upload.Namespace())
	if err != nil {
		return CompleteUploadResult{}, fmt.Errorf(
			"%w: %q", ErrUnknownNamespace, upload.Namespace().String(),
		)
	}

	info, err := h.objects.Head(ctx, upload.Location())

	if errors.Is(err, ErrObjectNotFound) {
		if upload.IsExpired(h.clock.Now()) {
			return CompleteUploadResult{}, fmt.Errorf(
				"%w: expired at %s", ErrUploadExpired, upload.ExpiresAt().Format(time.RFC3339),
			)
		}

		return CompleteUploadResult{}, err
	}

	if err != nil {
		return CompleteUploadResult{}, err
	}

	if info.SizeBytes > upload.MaxSize().Bytes() {
		return CompleteUploadResult{}, fmt.Errorf(
			"%w: %d bytes over a limit of %d",
			ErrSizeExceeded, info.SizeBytes, upload.MaxSize().Bytes(),
		)
	}

	size, err := domain.NewSize(info.SizeBytes)
	if err != nil {
		return CompleteUploadResult{}, fmt.Errorf("%w: %w", ErrObjectUnusable, err)
	}

	mimeType, err := domain.NewMimeType(info.ContentType)
	if err != nil {
		return CompleteUploadResult{}, fmt.Errorf("%w: %w", ErrObjectUnusable, err)
	}

	if !policy.AllowedMime.Contains(mimeType) {
		return CompleteUploadResult{}, fmt.Errorf(
			"%w: %q is not in %s",
			ErrUnsupportedMimeType, mimeType.String(), policy.AllowedMime.String(),
		)
	}

	fileID, err := domain.NewFileID()
	if err != nil {
		return CompleteUploadResult{}, err
	}

	file, err := domain.NewFile(domain.NewFileParams{
		ID:           fileID,
		Namespace:    upload.Namespace(),
		OwnerRef:     upload.OwnerRef(),
		OriginalName: upload.OriginalName(),
		MimeType:     mimeType,
		Size:         size,
		Now:          h.clock.Now(),
	})
	if err != nil {
		return CompleteUploadResult{}, err
	}

	if err := upload.Complete(file.ID(), h.clock.Now()); err != nil {
		return CompleteUploadResult{}, err
	}

	err = h.tx.InTx(ctx, func(ctx context.Context) error {
		if err := h.files.Create(ctx, &file); err != nil {
			return err
		}

		if err := h.uploads.Save(ctx, &upload); err != nil {
			return err
		}

		return h.enqueuer.EnqueueProcessFile(ctx, ProcessFileCommand{
			FileID:     file.ID(),
			StagingKey: upload.Location(),
			Variants:   policy.Variants,
		})
	})

	if errors.Is(err, ErrStaleVersion) {
		winner, reloadErr := h.uploads.ByID(ctx, cmd.UploadID)
		if reloadErr != nil {
			return CompleteUploadResult{}, reloadErr
		}

		if !winner.Status().Equal(domain.UploadStatusUploaded) {
			return CompleteUploadResult{}, err
		}

		existing, fileErr := h.files.ByID(ctx, winner.FileID())
		if fileErr != nil {
			return CompleteUploadResult{}, fileErr
		}

		return CompleteUploadResult{FileID: existing.ID(), Status: existing.Status()}, nil
	}

	if err != nil {
		return CompleteUploadResult{}, err
	}

	return CompleteUploadResult{FileID: file.ID(), Status: file.Status()}, nil
}
