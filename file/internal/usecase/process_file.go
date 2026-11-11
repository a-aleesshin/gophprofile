package usecase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

type ProcessParams struct {
	Source    io.Reader
	Namespace domain.Namespace
	MimeType  domain.MimeType
	Variants  []domain.VariantName
}

type RenderedVariant struct {
	Name       domain.VariantName
	Ext        domain.Extension
	MimeType   domain.MimeType
	Dimensions domain.Dimensions
	Size       domain.Size
	Checksum   domain.Checksum
	Body       []byte
}

type ProcessedContent struct {
	Reason      domain.RejectReason
	Checksum    domain.Checksum
	Dimensions  domain.Dimensions
	OriginalExt domain.Extension
	Variants    []RenderedVariant
}

type PutObjectParams struct {
	Location domain.Location
	Body     io.Reader
	MimeType domain.MimeType
	Size     domain.Size
}

type FileProcessedFact struct {
	FileID     domain.FileID
	OwnerRef   domain.OwnerRef
	MimeType   domain.MimeType
	Size       domain.Size
	Dimensions domain.Dimensions
	Variants   []domain.Variant
}

type FileRejectedFact struct {
	FileID   domain.FileID
	OwnerRef domain.OwnerRef
	Reason   domain.RejectReason
}

type fileLoaderSaver interface {
	ByID(ctx context.Context, id domain.FileID) (domain.File, error)
	Save(ctx context.Context, file *domain.File) error
}

type variantCreator interface {
	CreateAll(ctx context.Context, variants []domain.Variant) error
}

type objectReadWriter interface {
	Get(ctx context.Context, location domain.Location) (io.ReadCloser, error)
	Put(ctx context.Context, params PutObjectParams) error
	Copy(ctx context.Context, from, to domain.Location) error
	Delete(ctx context.Context, locations ...domain.Location) error
}

type contentProcessor interface {
	Process(ctx context.Context, params ProcessParams) (ProcessedContent, error)
}

type factPublisher interface {
	PublishFileProcessed(ctx context.Context, fact FileProcessedFact) error
	PublishFileRejected(ctx context.Context, fact FileRejectedFact) error
}

type ProcessFileResult struct {
	Status domain.FileStatus
	Reason domain.RejectReason
}

type ProcessFileHandler struct {
	files     fileLoaderSaver
	variants  variantCreator
	objects   objectReadWriter
	processor contentProcessor
	publisher factPublisher
	tx        transactor
	clock     clock
}

func NewProcessFileHandler(
	files fileLoaderSaver,
	variants variantCreator,
	objects objectReadWriter,
	processor contentProcessor,
	publisher factPublisher,
	tx transactor,
	clock clock,
) *ProcessFileHandler {
	return &ProcessFileHandler{
		files:     files,
		variants:  variants,
		objects:   objects,
		processor: processor,
		publisher: publisher,
		tx:        tx,
		clock:     clock,
	}
}

func (h *ProcessFileHandler) Handle(
	ctx context.Context,
	cmd ProcessFileCommand,
) (ProcessFileResult, error) {
	switch {
	case cmd.FileID.IsZero():
		return ProcessFileResult{}, fmt.Errorf("%w: file id is required", ErrInvalidCommand)
	case cmd.StagingKey.IsZero():
		return ProcessFileResult{}, fmt.Errorf("%w: staging key is required", ErrInvalidCommand)
	}

	file, err := h.files.ByID(ctx, cmd.FileID)
	if err != nil {
		return ProcessFileResult{}, err
	}

	if !file.Status().Equal(domain.FileStatusReceived) &&
		!file.Status().Equal(domain.FileStatusProcessing) {
		return ProcessFileResult{Status: file.Status(), Reason: file.RejectReason()}, nil
	}

	if file.Status().Equal(domain.FileStatusReceived) {
		if err := file.StartProcessing(h.clock.Now()); err != nil {
			return ProcessFileResult{}, err
		}

		if err := h.files.Save(ctx, &file); err != nil {
			if errors.Is(err, ErrStaleVersion) {
				return ProcessFileResult{Status: file.Status()}, nil
			}

			return ProcessFileResult{}, err
		}
	}

	var content ProcessedContent

	body, err := h.objects.Get(ctx, cmd.StagingKey)

	switch {
	case errors.Is(err, ErrObjectNotFound):
		content.Reason = domain.RejectReasonInternalError
	case err != nil:
		return ProcessFileResult{}, err
	default:
		defer func() { _ = body.Close() }()

		content, err = h.processor.Process(ctx, ProcessParams{
			Source:    body,
			Namespace: file.Namespace(),
			MimeType:  file.MimeType(),
			Variants:  cmd.Variants,
		})
		if err != nil {
			return ProcessFileResult{}, err
		}
	}

	if !content.Reason.IsZero() {
		if err := file.Reject(content.Reason, h.clock.Now()); err != nil {
			return ProcessFileResult{}, err
		}

		err = h.tx.InTx(ctx, func(ctx context.Context) error {
			if err := h.files.Save(ctx, &file); err != nil {
				return err
			}

			return h.publisher.PublishFileRejected(ctx, FileRejectedFact{
				FileID:   file.ID(),
				OwnerRef: file.OwnerRef(),
				Reason:   content.Reason,
			})
		})

		if errors.Is(err, ErrStaleVersion) {
			return ProcessFileResult{Status: domain.FileStatusDeleting}, nil
		}

		if err != nil {
			return ProcessFileResult{}, err
		}

		_ = h.objects.Delete(ctx, cmd.StagingKey)

		return ProcessFileResult{Status: file.Status(), Reason: content.Reason}, nil
	}

	variants := make([]domain.Variant, 0, len(content.Variants)+1)
	written := make([]domain.Location, 0, len(content.Variants)+1)

	for _, rendered := range content.Variants {
		variant, buildErr := domain.NewVariant(domain.NewVariantParams{
			FileID:     file.ID(),
			Name:       rendered.Name,
			Ext:        rendered.Ext,
			MimeType:   rendered.MimeType,
			Dimensions: rendered.Dimensions,
			Size:       rendered.Size,
			Checksum:   rendered.Checksum,
			Now:        h.clock.Now(),
		})
		if buildErr != nil {
			return ProcessFileResult{}, buildErr
		}

		if putErr := h.objects.Put(ctx, PutObjectParams{
			Location: variant.Location(),
			Body:     bytes.NewReader(rendered.Body),
			MimeType: rendered.MimeType,
			Size:     rendered.Size,
		}); putErr != nil {
			return ProcessFileResult{}, putErr
		}

		variants = append(variants, variant)
		written = append(written, variant.Location())
	}

	if err := h.objects.Copy(ctx, cmd.StagingKey, file.Location()); err != nil {
		return ProcessFileResult{}, err
	}

	written = append(written, file.Location())

	original, err := domain.NewVariant(domain.NewVariantParams{
		FileID:     file.ID(),
		Name:       domain.VariantNameOriginal,
		Ext:        content.OriginalExt,
		MimeType:   file.MimeType(),
		Dimensions: content.Dimensions,
		Size:       file.Size(),
		Checksum:   content.Checksum,
		Now:        h.clock.Now(),
	})
	if err != nil {
		return ProcessFileResult{}, err
	}

	variants = append(variants, original)

	if err := file.MarkReady(content.Checksum, content.Dimensions, h.clock.Now()); err != nil {
		return ProcessFileResult{}, err
	}

	err = h.tx.InTx(ctx, func(ctx context.Context) error {
		if err := h.files.Save(ctx, &file); err != nil {
			return err
		}

		if err := h.variants.CreateAll(ctx, variants); err != nil {
			return err
		}

		return h.publisher.PublishFileProcessed(ctx, FileProcessedFact{
			FileID:     file.ID(),
			OwnerRef:   file.OwnerRef(),
			MimeType:   file.MimeType(),
			Size:       file.Size(),
			Dimensions: content.Dimensions,
			Variants:   variants,
		})
	})

	if errors.Is(err, ErrStaleVersion) {
		_ = h.objects.Delete(ctx, written...)

		return ProcessFileResult{Status: domain.FileStatusDeleting}, nil
	}

	if err != nil {
		return ProcessFileResult{}, err
	}

	_ = h.objects.Delete(ctx, cmd.StagingKey)

	return ProcessFileResult{Status: file.Status()}, nil
}
