package content

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"strings"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
)

var (
	ErrInvalidProcessor   = errors.New("invalid processor")
	ErrUnsupportedVariant = errors.New("unsupported variant")
)

const (
	validatorImage     = "image"
	validatorContainer = "container"
)

var extensionByMime = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/webp": "webp",
	mimePDF:      "pdf",
	mimeDOCX:     "docx",
}

type policyProvider interface {
	Namespaces() []domain.Namespace
	ProcessPolicy(namespace domain.Namespace) (usecase.ProcessPolicy, error)
}

type Processor struct {
	policies policyProvider
}

func NewProcessor(policies policyProvider) (*Processor, error) {
	if policies == nil {
		return nil, fmt.Errorf("%w: policies are required", ErrInvalidProcessor)
	}

	for _, namespace := range policies.Namespaces() {
		policy, err := policies.ProcessPolicy(namespace)
		if err != nil {
			return nil, err
		}

		if policy.Validator != validatorImage && policy.Validator != validatorContainer {
			return nil, fmt.Errorf("%w: namespace %q has unknown validator %q",
				ErrInvalidProcessor, namespace.String(), policy.Validator)
		}

		for _, spec := range policy.Variants {
			if _, known := encoders[spec.Format]; !known {
				return nil, fmt.Errorf(
					"%w: namespace %q variant %q wants format %q, which this build cannot encode",
					ErrUnsupportedVariant, namespace.String(), spec.Name.String(), spec.Format)
			}

			if spec.Fit != fitCover && spec.Fit != fitInside {
				return nil, fmt.Errorf("%w: namespace %q variant %q has unknown fit %q",
					ErrUnsupportedVariant, namespace.String(), spec.Name.String(), spec.Fit)
			}
		}
	}

	return &Processor{policies: policies}, nil
}

func (p *Processor) Process(
	ctx context.Context,
	params usecase.ProcessParams,
) (usecase.ProcessedContent, error) {
	policy, err := p.policies.ProcessPolicy(params.Namespace)
	if err != nil {
		return usecase.ProcessedContent{}, err
	}

	body, err := io.ReadAll(io.LimitReader(params.Source, policy.MaxSizeBytes+1))
	if err != nil {
		return usecase.ProcessedContent{}, fmt.Errorf("read staged object: %w", err)
	}

	switch {
	case int64(len(body)) > policy.MaxSizeBytes:
		return rejected(domain.RejectReasonSizeMismatch), nil
	case len(body) == 0:
		return rejected(domain.RejectReasonDecodeFailed), nil
	}

	if !sniffMatches(body, params.MimeType) {
		return rejected(domain.RejectReasonMagicMismatch), nil
	}

	extension, known := extensionByMime[params.MimeType.String()]
	if !known {
		return rejected(domain.RejectReasonUnsupportedFormat), nil
	}

	originalExt, err := domain.NewExtension(extension)
	if err != nil {
		return usecase.ProcessedContent{}, err
	}

	digest := sha256.Sum256(body)

	checksum, err := domain.NewChecksum(digest[:])
	if err != nil {
		return usecase.ProcessedContent{}, err
	}

	switch policy.Validator {
	case validatorImage:
		return processImage(ctx, body, params, policy, checksum, originalExt)
	case validatorContainer:
		return processContainer(body, params, policy, checksum, originalExt)
	default:
		return usecase.ProcessedContent{}, fmt.Errorf(
			"%w: unknown validator %q for namespace %q",
			ErrInvalidProcessor, policy.Validator, params.Namespace.String())
	}
}

func processImage(
	ctx context.Context,
	body []byte,
	params usecase.ProcessParams,
	policy usecase.ProcessPolicy,
	checksum domain.Checksum,
	originalExt domain.Extension,
) (usecase.ProcessedContent, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return rejected(domain.RejectReasonDecodeFailed), nil
	}

	pixels := int64(config.Width) * int64(config.Height)

	switch {
	case policy.MaxPixels > 0 && pixels > policy.MaxPixels:
		return rejected(domain.RejectReasonTooManyPixels), nil
	case policy.MaxDimension > 0 &&
		(config.Width > policy.MaxDimension || config.Height > policy.MaxDimension):
		return rejected(domain.RejectReasonTooManyPixels), nil
	case policy.MinDimension > 0 &&
		(config.Width < policy.MinDimension || config.Height < policy.MinDimension):
		return rejected(domain.RejectReasonTooSmall), nil
	}

	source, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return rejected(domain.RejectReasonDecodeFailed), nil
	}

	source = applyOrientation(source, exifOrientation(body))

	dimensions, err := domain.NewDimensions(source.Bounds().Dx(), source.Bounds().Dy())
	if err != nil {
		return usecase.ProcessedContent{}, err
	}

	specs := make(map[string]usecase.VariantSpec, len(policy.Variants))
	for _, spec := range policy.Variants {
		specs[spec.Name.String()] = spec
	}

	rendered := make([]usecase.RenderedVariant, 0, len(params.Variants))

	for _, name := range params.Variants {
		if err := ctx.Err(); err != nil {
			return usecase.ProcessedContent{}, err
		}

		spec, wanted := specs[name.String()]
		if !wanted {
			continue
		}

		variant, err := renderVariant(source, spec)
		if err != nil {
			return usecase.ProcessedContent{}, err
		}

		rendered = append(rendered, variant)
	}

	return usecase.ProcessedContent{
		Checksum:    checksum,
		Dimensions:  dimensions,
		OriginalExt: originalExt,
		Variants:    rendered,
	}, nil
}

func renderVariant(
	source image.Image,
	spec usecase.VariantSpec,
) (usecase.RenderedVariant, error) {
	codec, known := encoders[spec.Format]
	if !known {
		return usecase.RenderedVariant{}, fmt.Errorf(
			"%w: no encoder for %q (variant %q)",
			ErrUnsupportedVariant, spec.Format, spec.Name.String())
	}

	scaled, err := resize(source, spec.Width, spec.Height, spec.Fit)
	if err != nil {
		return usecase.RenderedVariant{}, err
	}

	var buffer bytes.Buffer

	if err := codec.encode(&buffer, scaled, spec.Quality); err != nil {
		return usecase.RenderedVariant{}, fmt.Errorf(
			"encode variant %q: %w", spec.Name.String(), err)
	}

	body := buffer.Bytes()
	digest := sha256.Sum256(body)

	checksum, err := domain.NewChecksum(digest[:])
	if err != nil {
		return usecase.RenderedVariant{}, err
	}

	size, err := domain.NewSize(int64(len(body)))
	if err != nil {
		return usecase.RenderedVariant{}, err
	}

	dimensions, err := domain.NewDimensions(scaled.Bounds().Dx(), scaled.Bounds().Dy())
	if err != nil {
		return usecase.RenderedVariant{}, err
	}

	extension, err := domain.NewExtension(codec.ext)
	if err != nil {
		return usecase.RenderedVariant{}, err
	}

	mimeType, err := domain.NewMimeType(codec.mime)
	if err != nil {
		return usecase.RenderedVariant{}, err
	}

	return usecase.RenderedVariant{
		Name:       spec.Name,
		Ext:        extension,
		MimeType:   mimeType,
		Dimensions: dimensions,
		Size:       size,
		Checksum:   checksum,
		Body:       body,
	}, nil
}

func processContainer(
	body []byte,
	params usecase.ProcessParams,
	policy usecase.ProcessPolicy,
	checksum domain.Checksum,
	originalExt domain.Extension,
) (usecase.ProcessedContent, error) {
	var reason domain.RejectReason

	switch params.MimeType.String() {
	case mimePDF:
		reason = validatePDF(body)
	case mimeDOCX:
		reason = validateDOCX(body, zipLimits{
			MaxEntries:    policy.ZipMaxEntries,
			MaxEntryBytes: policy.ZipMaxEntryBytes,
			MaxTotalBytes: policy.ZipMaxTotalBytes,
			MaxRatio:      policy.ZipMaxRatio,
		})
	default:
		return rejected(domain.RejectReasonUnsupportedFormat), nil
	}

	if !reason.IsZero() {
		return rejected(reason), nil
	}

	return usecase.ProcessedContent{
		Checksum:    checksum,
		OriginalExt: originalExt,
	}, nil
}

func sniffMatches(body []byte, declared domain.MimeType) bool {
	sniffed, _, _ := strings.Cut(http.DetectContentType(body), ";")
	sniffed = strings.TrimSpace(sniffed)

	if sniffed == declared.String() {
		return true
	}

	return sniffed == mimeZIP && declared.String() == mimeDOCX
}

func rejected(reason domain.RejectReason) usecase.ProcessedContent {
	return usecase.ProcessedContent{Reason: reason}
}
