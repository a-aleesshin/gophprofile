package usecase

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

type ServingPolicy struct {
	CacheControl string
	Disposition  string
	AcceptRanges bool
}

type ServableVariantView struct {
	Namespace domain.Namespace
	Location  domain.Location
	Ext       string
	MimeType  string
	SizeBytes int64
	ETag      string
	CreatedAt time.Time
}

type servingPolicyProvider interface {
	ServingPolicy(namespace domain.Namespace) (ServingPolicy, error)
}

type servableVariantLoader interface {
	ServableByName(
		ctx context.Context,
		fileID domain.FileID,
		name domain.VariantName,
	) (ServableVariantView, error)
}

type objectGetter interface {
	Get(ctx context.Context, location domain.Location) (io.ReadCloser, error)
}

type ServeVariantCommand struct {
	FileID      domain.FileID
	Variant     domain.VariantName
	Ext         domain.Extension
	IfNoneMatch string
}

type ServeVariantResult struct {
	NotModified  bool
	Body         io.ReadCloser
	MimeType     string
	SizeBytes    int64
	ETag         string
	LastModified time.Time
	CacheControl string
	Disposition  string
	AcceptRanges bool
}

type ServeVariantHandler struct {
	policies servingPolicyProvider
	variants servableVariantLoader
	objects  objectGetter
}

func NewServeVariantHandler(
	policies servingPolicyProvider,
	variants servableVariantLoader,
	objects objectGetter,
) *ServeVariantHandler {
	return &ServeVariantHandler{policies: policies, variants: variants, objects: objects}
}

func (h *ServeVariantHandler) Handle(
	ctx context.Context,
	cmd ServeVariantCommand,
) (ServeVariantResult, error) {
	switch {
	case cmd.FileID.IsZero():
		return ServeVariantResult{}, fmt.Errorf("%w: file id is required", ErrInvalidCommand)
	case cmd.Variant.IsZero():
		return ServeVariantResult{}, fmt.Errorf("%w: variant is required", ErrInvalidCommand)
	case cmd.Ext.IsZero():
		return ServeVariantResult{}, fmt.Errorf("%w: extension is required", ErrInvalidCommand)
	}

	view, err := h.variants.ServableByName(ctx, cmd.FileID, cmd.Variant)
	if err != nil {
		return ServeVariantResult{}, err
	}

	if view.Ext != cmd.Ext.String() {
		return ServeVariantResult{}, fmt.Errorf(
			"%w: %s/%s", domain.ErrVariantAbsent, cmd.FileID.String(), cmd.Variant.String(),
		)
	}

	policy, err := h.policies.ServingPolicy(view.Namespace)
	if err != nil {
		return ServeVariantResult{}, fmt.Errorf(
			"%w: %q", ErrUnknownNamespace, view.Namespace.String(),
		)
	}

	result := ServeVariantResult{
		MimeType:     view.MimeType,
		SizeBytes:    view.SizeBytes,
		ETag:         view.ETag,
		LastModified: view.CreatedAt,
		CacheControl: policy.CacheControl,
		Disposition:  policy.Disposition,
		AcceptRanges: policy.AcceptRanges,
	}

	if cmd.IfNoneMatch != "" && cmd.IfNoneMatch == result.ETag {
		result.NotModified = true

		return result, nil
	}

	body, err := h.objects.Get(ctx, view.Location)
	if err != nil {
		return ServeVariantResult{}, err
	}

	result.Body = body

	return result, nil
}
