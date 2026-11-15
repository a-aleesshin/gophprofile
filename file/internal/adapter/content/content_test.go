package content

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/adapter/policy"
	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
)

type stubPolicies struct {
	policy usecase.ProcessPolicy
	err    error
}

func (s stubPolicies) Namespaces() []domain.Namespace {
	return []domain.Namespace{mustNamespace("avatar")}
}

func (s stubPolicies) ProcessPolicy(domain.Namespace) (usecase.ProcessPolicy, error) {
	return s.policy, s.err
}

func mustNamespace(raw string) domain.Namespace {
	value, err := domain.NewNamespace(raw)
	if err != nil {
		panic(err)
	}

	return value
}

func mustVariantName(t *testing.T, raw string) domain.VariantName {
	t.Helper()

	value, err := domain.NewVariantName(raw)
	require.NoError(t, err)

	return value
}

func mustMime(t *testing.T, raw string) domain.MimeType {
	t.Helper()

	value, err := domain.NewMimeType(raw)
	require.NoError(t, err)

	return value
}

func gradient(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	for y := range height {
		for x := range width {
			img.Set(x, y, color.RGBA{
				R: uint8(x % 256),
				G: uint8(y % 256),
				B: uint8((x + y) % 256),
				A: 255,
			})
		}
	}

	return img
}

func jpegBytes(t *testing.T, width, height int) []byte {
	t.Helper()

	var buffer bytes.Buffer
	require.NoError(t, jpeg.Encode(&buffer, gradient(width, height), nil))

	return buffer.Bytes()
}

func pngBytes(t *testing.T, width, height int) []byte {
	t.Helper()

	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, gradient(width, height)))

	return buffer.Bytes()
}

func withOrientation(t *testing.T, body []byte, orientation byte) []byte {
	t.Helper()

	require.Greater(t, len(body), 2)

	tiff := []byte{
		'I', 'I', 42, 0,
		8, 0, 0, 0,
		1, 0,
		0x12, 0x01,
		3, 0,
		1, 0, 0, 0,
		orientation, 0, 0, 0,
		0, 0, 0, 0,
	}

	payload := append([]byte("Exif\x00\x00"), tiff...)
	length := len(payload) + 2

	segment := []byte{0xFF, 0xE1, byte(length >> 8), byte(length)}
	segment = append(segment, payload...)

	out := make([]byte, 0, len(body)+len(segment))
	out = append(out, body[:2]...)
	out = append(out, segment...)
	out = append(out, body[2:]...)

	return out
}

func imagePolicy(t *testing.T) usecase.ProcessPolicy {
	t.Helper()

	return usecase.ProcessPolicy{
		Validator:    validatorImage,
		MaxSizeBytes: 10 << 20,
		MaxPixels:    50_000_000,
		MaxDimension: 10000,
		MinDimension: 100,
		Variants: []usecase.VariantSpec{
			{
				Name: mustVariantName(t, "100x100"), Width: 100, Height: 100,
				Fit: fitCover, Format: "jpeg", Quality: 82,
			},
			{
				Name: mustVariantName(t, "web"), Width: 800, Height: 800,
				Fit: fitInside, Format: "jpeg", Quality: 85,
			},
		},
	}
}

func newTestProcessor(t *testing.T, policy usecase.ProcessPolicy) *Processor {
	t.Helper()

	processor, err := NewProcessor(stubPolicies{policy: policy})
	require.NoError(t, err)

	return processor
}

func process(
	t *testing.T,
	policy usecase.ProcessPolicy,
	body []byte,
	mime string,
	variants ...string,
) usecase.ProcessedContent {
	t.Helper()

	names := make([]domain.VariantName, 0, len(variants))
	for _, raw := range variants {
		names = append(names, mustVariantName(t, raw))
	}

	content, err := newTestProcessor(t, policy).Process(t.Context(), usecase.ProcessParams{
		Source:    bytes.NewReader(body),
		Namespace: mustNamespace("avatar"),
		MimeType:  mustMime(t, mime),
		Variants:  names,
	})
	require.NoError(t, err)

	return content
}

func TestNewProcessorRefusesAFormatItCannotEncode(t *testing.T) {
	t.Parallel()

	// Arrange
	policy := imagePolicy(t)
	policy.Variants[0].Format = "webp"

	// Act
	processor, err := NewProcessor(stubPolicies{policy: policy})

	// Assert
	require.ErrorIs(t, err, ErrUnsupportedVariant)
	assert.ErrorContains(t, err, "webp")
	assert.Nil(t, processor)
}

func TestNewProcessorRefusesAnUnknownFit(t *testing.T) {
	t.Parallel()

	// Arrange
	policy := imagePolicy(t)
	policy.Variants[0].Fit = "squeeze"

	// Act
	_, err := NewProcessor(stubPolicies{policy: policy})

	// Assert
	require.ErrorIs(t, err, ErrUnsupportedVariant)
}

func TestProcessRendersTheRequestedVariants(t *testing.T) {
	t.Parallel()

	// Arrange
	body := jpegBytes(t, 400, 200)

	// Act
	content := process(t, imagePolicy(t), body, "image/jpeg", "100x100", "web")

	// Assert
	require.True(t, content.Reason.IsZero())
	assert.Equal(t, "jpg", content.OriginalExt.String())

	digest := sha256.Sum256(body)
	assert.Equal(t, hex.EncodeToString(digest[:]), content.Checksum.String())

	assert.Equal(t, int32(400), content.Dimensions.Width())
	assert.Equal(t, int32(200), content.Dimensions.Height())

	require.Len(t, content.Variants, 2)

	cover := content.Variants[0]
	assert.Equal(t, "100x100", cover.Name.String())
	assert.Equal(t, "jpg", cover.Ext.String())
	assert.Equal(t, "image/jpeg", cover.MimeType.String())
	assert.Equal(t, int32(100), cover.Dimensions.Width())
	assert.Equal(t, int32(100), cover.Dimensions.Height())
	assert.Positive(t, cover.Size.Bytes())

	decoded, format, err := image.Decode(bytes.NewReader(cover.Body))
	require.NoError(t, err)
	assert.Equal(t, "jpeg", format)
	assert.Equal(t, 100, decoded.Bounds().Dx())

	inside := content.Variants[1]
	assert.Equal(t, int32(400), inside.Dimensions.Width(),
		"inside must keep the aspect ratio and never upscale")
	assert.Equal(t, int32(200), inside.Dimensions.Height())
}

func TestProcessSkipsAVariantThePolicyNoLongerHas(t *testing.T) {
	t.Parallel()

	// Act
	content := process(t, imagePolicy(t), jpegBytes(t, 400, 400), "image/jpeg", "100x100", "gone")

	// Assert
	require.Len(t, content.Variants, 1,
		"a policy change must not reject a file that is otherwise valid")
	assert.Equal(t, "100x100", content.Variants[0].Name.String())
}

func TestProcessAppliesExifOrientation(t *testing.T) {
	t.Parallel()

	// Arrange
	body := withOrientation(t, jpegBytes(t, 400, 200), 6)

	// Act
	content := process(t, imagePolicy(t), body, "image/jpeg", "web")

	// Assert
	require.True(t, content.Reason.IsZero())
	assert.Equal(t, int32(200), content.Dimensions.Width(),
		"orientation 6 turns the image on its side")
	assert.Equal(t, int32(400), content.Dimensions.Height())
}

func TestProcessRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*usecase.ProcessPolicy)
		body   func(*testing.T) []byte
		mime   string
		want   domain.RejectReason
	}{
		{
			name: "a png declared as jpeg",
			body: func(t *testing.T) []byte { return pngBytes(t, 400, 400) },
			mime: "image/jpeg",
			want: domain.RejectReasonMagicMismatch,
		},
		{
			name: "bytes that start like a jpeg but do not decode",
			body: func(*testing.T) []byte {
				return append([]byte{0xFF, 0xD8, 0xFF}, bytes.Repeat([]byte{0x7F}, 600)...)
			},
			mime: "image/jpeg",
			want: domain.RejectReasonDecodeFailed,
		},
		{
			name:   "more pixels than the policy allows",
			mutate: func(p *usecase.ProcessPolicy) { p.MaxPixels = 1000 },
			body:   func(t *testing.T) []byte { return jpegBytes(t, 400, 400) },
			mime:   "image/jpeg",
			want:   domain.RejectReasonTooManyPixels,
		},
		{
			name:   "a side longer than the policy allows",
			mutate: func(p *usecase.ProcessPolicy) { p.MaxDimension = 200 },
			body:   func(t *testing.T) []byte { return jpegBytes(t, 400, 100) },
			mime:   "image/jpeg",
			want:   domain.RejectReasonTooManyPixels,
		},
		{
			name: "smaller than the policy allows",
			body: func(t *testing.T) []byte { return jpegBytes(t, 50, 50) },
			mime: "image/jpeg",
			want: domain.RejectReasonTooSmall,
		},
		{
			name:   "larger than the policy allows",
			mutate: func(p *usecase.ProcessPolicy) { p.MaxSizeBytes = 64 },
			body:   func(t *testing.T) []byte { return jpegBytes(t, 400, 400) },
			mime:   "image/jpeg",
			want:   domain.RejectReasonSizeMismatch,
		},
		{
			name: "an empty object",
			body: func(*testing.T) []byte { return nil },
			mime: "image/jpeg",
			want: domain.RejectReasonDecodeFailed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			policy := imagePolicy(t)
			if test.mutate != nil {
				test.mutate(&policy)
			}

			// Act
			content := process(t, policy, test.body(t), test.mime, "100x100")

			// Assert
			assert.Equal(t, test.want.String(), content.Reason.String())
			assert.Empty(t, content.Variants)
		})
	}
}

func TestProcessCoverCropsInsteadOfSquashing(t *testing.T) {
	t.Parallel()

	// Arrange
	source := image.NewRGBA(image.Rect(0, 0, 400, 200))

	for y := range 200 {
		for x := range 400 {
			shade := color.RGBA{R: 255, G: 255, B: 255, A: 255}
			if x < 100 {
				shade = color.RGBA{R: 255, A: 255}
			}

			source.Set(x, y, shade)
		}
	}

	var buffer bytes.Buffer
	require.NoError(t, jpeg.Encode(&buffer, source, &jpeg.Options{Quality: 100}))

	// Act
	content := process(t, imagePolicy(t), buffer.Bytes(), "image/jpeg", "100x100")

	// Assert
	require.Len(t, content.Variants, 1)

	decoded, err := jpeg.Decode(bytes.NewReader(content.Variants[0].Body))
	require.NoError(t, err)

	red, green, blue, _ := decoded.At(4, 50).RGBA()

	assert.Greater(t, green>>8, uint32(200),
		"cover must crop the centre, so the red strip at the left edge is cut away")
	assert.Greater(t, blue>>8, uint32(200))
	assert.Greater(t, red>>8, uint32(200))
}

func TestNewProcessorAcceptsTheShippedPolicies(t *testing.T) {
	t.Parallel()

	// Arrange
	registry, err := policy.Load("../../../configs/policies.yaml")
	require.NoError(t, err)

	// Act
	processor, err := NewProcessor(registry)

	// Assert
	require.NoError(t, err,
		"every variant the shipped policies ask for must have an encoder in this build")
	require.NotNil(t, processor)
}

func TestProcessRunsTheShippedAvatarPolicy(t *testing.T) {
	t.Parallel()

	// Arrange
	registry, err := policy.Load("../../../configs/policies.yaml")
	require.NoError(t, err)

	processor, err := NewProcessor(registry)
	require.NoError(t, err)

	body := jpegBytes(t, 1200, 900)

	names := make([]domain.VariantName, 0, 3)
	for _, raw := range []string{"100x100", "300x300", "web"} {
		names = append(names, mustVariantName(t, raw))
	}

	// Act
	content, err := processor.Process(t.Context(), usecase.ProcessParams{
		Source:    bytes.NewReader(body),
		Namespace: mustNamespace("avatar"),
		MimeType:  mustMime(t, "image/jpeg"),
		Variants:  names,
	})

	// Assert
	require.NoError(t, err)
	require.True(t, content.Reason.IsZero())
	require.Len(t, content.Variants, 3)

	assert.Equal(t, int32(100), content.Variants[0].Dimensions.Width())
	assert.Equal(t, int32(300), content.Variants[1].Dimensions.Width())
	assert.Equal(t, int32(800), content.Variants[2].Dimensions.Width(),
		"inside fits the long side into the box")
	assert.Equal(t, int32(600), content.Variants[2].Dimensions.Height())

	for _, variant := range content.Variants {
		assert.Less(t, variant.Size.Bytes(), int64(len(body)),
			"a variant of %s must be smaller than the original", variant.Name.String())
	}
}
