package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

var (
	ErrInvalidCommand      = errors.New("invalid command")
	ErrUnknownNamespace    = errors.New("unknown namespace")
	ErrUnsupportedMimeType = errors.New("unsupported mime type")
	ErrUploadNotReusable   = errors.New("upload is no longer reusable")
)

type UploadPolicy struct {
	MaxSizeBytes      int64
	AllowedMime       domain.MimeSet
	ContentTypePrefix string
	TTL               time.Duration
}

type PresignUploadParams struct {
	Location          domain.Location
	MaxSizeBytes      int64
	ContentType       string
	ContentTypePrefix string
	ExpiresAt         time.Time
}

type PresignedUpload struct {
	URL    string
	Fields map[string]string
}

type uploadPolicyProvider interface {
	UploadPolicy(namespace domain.Namespace) (UploadPolicy, error)
}

type uploadByKeyFinder interface {
	ByIdempotencyKey(
		ctx context.Context,
		namespace domain.Namespace,
		key domain.IdempotencyKey,
	) (domain.Upload, error)
}

type uploadCreator interface {
	Create(ctx context.Context, upload *domain.Upload) error
}

type uploadPresigner interface {
	PresignUpload(ctx context.Context, params PresignUploadParams) (PresignedUpload, error)
}

type clock interface {
	Now() time.Time
}

type CreateUploadCommand struct {
	Namespace      domain.Namespace
	OwnerRef       domain.OwnerRef
	OriginalName   domain.OriginalName
	DeclaredMime   domain.MimeType
	MaxSize        domain.Size
	IdempotencyKey domain.IdempotencyKey
}

type CreateUploadResult struct {
	UploadID  domain.UploadID
	URL       string
	Fields    map[string]string
	ExpiresAt time.Time
}

type CreateUploadHandler struct {
	policies  uploadPolicyProvider
	finder    uploadByKeyFinder
	creator   uploadCreator
	presigner uploadPresigner
	clock     clock
}

func NewCreateUploadHandler(
	policies uploadPolicyProvider,
	finder uploadByKeyFinder,
	creator uploadCreator,
	presigner uploadPresigner,
	clock clock,
) *CreateUploadHandler {
	return &CreateUploadHandler{
		policies:  policies,
		finder:    finder,
		creator:   creator,
		presigner: presigner,
		clock:     clock,
	}
}

func (h *CreateUploadHandler) Handle(
	ctx context.Context,
	cmd CreateUploadCommand,
) (CreateUploadResult, error) {
	switch {
	case cmd.Namespace.IsZero():
		return CreateUploadResult{}, fmt.Errorf("%w: namespace is required", ErrInvalidCommand)
	case cmd.IdempotencyKey.IsZero():
		return CreateUploadResult{}, fmt.Errorf("%w: idempotency key is required", ErrInvalidCommand)
	}

	policy, err := h.policies.UploadPolicy(cmd.Namespace)
	if err != nil {
		return CreateUploadResult{}, fmt.Errorf("%w: %q", ErrUnknownNamespace, cmd.Namespace.String())
	}

	var signature PresignedUpload

	upload, err := h.finder.ByIdempotencyKey(ctx, cmd.Namespace, cmd.IdempotencyKey)

	if errors.Is(err, domain.ErrUploadNotFound) {
		if !cmd.DeclaredMime.IsZero() && !policy.AllowedMime.Contains(cmd.DeclaredMime) {
			return CreateUploadResult{}, fmt.Errorf(
				"%w: %q is not in %s",
				ErrUnsupportedMimeType, cmd.DeclaredMime.String(), policy.AllowedMime.String(),
			)
		}

		maxSizeBytes := policy.MaxSizeBytes
		if !cmd.MaxSize.IsZero() && cmd.MaxSize.Bytes() < maxSizeBytes {
			maxSizeBytes = cmd.MaxSize.Bytes()
		}

		maxSize, sizeErr := domain.NewSize(maxSizeBytes)
		if sizeErr != nil {
			return CreateUploadResult{}, sizeErr
		}

		id, idErr := domain.NewUploadID()
		if idErr != nil {
			return CreateUploadResult{}, idErr
		}

		candidate, buildErr := domain.NewUpload(domain.NewUploadParams{
			ID:             id,
			Namespace:      cmd.Namespace,
			OwnerRef:       cmd.OwnerRef,
			IdempotencyKey: cmd.IdempotencyKey,
			OriginalName:   cmd.OriginalName,
			DeclaredMime:   cmd.DeclaredMime,
			MaxSize:        maxSize,
			TTL:            policy.TTL,
			Now:            h.clock.Now(),
		})
		if buildErr != nil {
			return CreateUploadResult{}, buildErr
		}

		presigned, signErr := h.presigner.PresignUpload(ctx, PresignUploadParams{
			Location:          candidate.Location(),
			MaxSizeBytes:      candidate.MaxSize().Bytes(),
			ContentType:       candidate.DeclaredMime().String(),
			ContentTypePrefix: policy.ContentTypePrefix,
			ExpiresAt:         candidate.ExpiresAt(),
		})
		if signErr != nil {
			return CreateUploadResult{}, signErr
		}

		err = h.creator.Create(ctx, &candidate)

		switch {
		case err == nil:
			upload, signature = candidate, presigned
		case errors.Is(err, domain.ErrUploadAlreadyExists):
			upload, err = h.finder.ByIdempotencyKey(ctx, cmd.Namespace, cmd.IdempotencyKey)
		}
	}

	if err != nil {
		return CreateUploadResult{}, err
	}

	if !upload.IsPending() || upload.IsExpired(h.clock.Now()) {
		return CreateUploadResult{}, fmt.Errorf(
			"%w: status %q, expires at %s",
			ErrUploadNotReusable, upload.Status().String(), upload.ExpiresAt().Format(time.RFC3339),
		)
	}

	if signature.URL == "" {
		signature, err = h.presigner.PresignUpload(ctx, PresignUploadParams{
			Location:          upload.Location(),
			MaxSizeBytes:      upload.MaxSize().Bytes(),
			ContentType:       upload.DeclaredMime().String(),
			ContentTypePrefix: policy.ContentTypePrefix,
			ExpiresAt:         upload.ExpiresAt(),
		})
		if err != nil {
			return CreateUploadResult{}, err
		}
	}

	return CreateUploadResult{
		UploadID:  upload.ID(),
		URL:       signature.URL,
		Fields:    signature.Fields,
		ExpiresAt: upload.ExpiresAt(),
	}, nil
}
