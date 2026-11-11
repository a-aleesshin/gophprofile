package content

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
)

func containerPolicy() usecase.ProcessPolicy {
	return usecase.ProcessPolicy{
		Validator:        validatorContainer,
		MaxSizeBytes:     50 << 20,
		ZipMaxEntries:    1000,
		ZipMaxEntryBytes: 100 << 20,
		ZipMaxTotalBytes: 500 << 20,
		ZipMaxRatio:      20,
	}
}

func pdfBytes(padding int) []byte {
	body := []byte("%PDF-1.7\n")
	body = append(body, bytes.Repeat([]byte("% filler\n"), padding)...)
	body = append(body, []byte("trailer\n%%EOF\n")...)

	return body
}

func docxBytes(t *testing.T, entries map[string]string) []byte {
	t.Helper()

	var buffer bytes.Buffer

	archive := zip.NewWriter(&buffer)

	for name, body := range entries {
		writer, err := archive.Create(name)
		require.NoError(t, err)

		_, err = writer.Write([]byte(body))
		require.NoError(t, err)
	}

	require.NoError(t, archive.Close())

	return buffer.Bytes()
}

func validDocx(t *testing.T) []byte {
	t.Helper()

	return docxBytes(t, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types/>`,
		"word/document.xml":   `<?xml version="1.0"?><document/>`,
	})
}

func TestProcessAcceptsADocument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body []byte
		mime string
		ext  string
	}{
		{name: "pdf", body: pdfBytes(4), mime: mimePDF, ext: "pdf"},
		{name: "docx", body: validDocx(t), mime: mimeDOCX, ext: "docx"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Act
			content := process(t, containerPolicy(), test.body, test.mime)

			// Assert
			require.True(t, content.Reason.IsZero())
			assert.Equal(t, test.ext, content.OriginalExt.String())
			assert.Empty(t, content.Variants, "a document has no variants")
			assert.True(t, content.Dimensions.IsZero(), "a document has no dimensions")
			assert.False(t, content.Checksum.IsZero())
		})
	}
}

func TestProcessRejectsABrokenContainer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*usecase.ProcessPolicy)
		body   func(*testing.T) []byte
		mime   string
		want   domain.RejectReason
	}{
		{
			name: "a pdf without a trailer",
			body: func(*testing.T) []byte { return []byte("%PDF-1.7\nnothing else\n") },
			mime: mimePDF,
			want: domain.RejectReasonContainerInvalid,
		},
		{
			name: "a pdf whose trailer is too far from the end",
			body: func(*testing.T) []byte {
				body := []byte("%PDF-1.7\n%%EOF\n")

				return append(body, bytes.Repeat([]byte("x"), pdfTrailerWindow+1)...)
			},
			mime: mimePDF,
			want: domain.RejectReasonContainerInvalid,
		},
		{
			name: "a zip that is not a docx",
			body: func(t *testing.T) []byte {
				return docxBytes(t, map[string]string{"notes.txt": "hello"})
			},
			mime: mimeDOCX,
			want: domain.RejectReasonContainerInvalid,
		},
		{
			name: "a docx without the document part",
			body: func(t *testing.T) []byte {
				return docxBytes(t, map[string]string{
					"[Content_Types].xml": `<?xml version="1.0"?><Types/>`,
				})
			},
			mime: mimeDOCX,
			want: domain.RejectReasonContainerInvalid,
		},
		{
			name:   "a docx with too many entries",
			mutate: func(p *usecase.ProcessPolicy) { p.ZipMaxEntries = 1 },
			body:   validDocx,
			mime:   mimeDOCX,
			want:   domain.RejectReasonContainerTooLarge,
		},
		{
			name:   "a docx whose entry is too large",
			mutate: func(p *usecase.ProcessPolicy) { p.ZipMaxEntryBytes = 4 },
			body:   validDocx,
			mime:   mimeDOCX,
			want:   domain.RejectReasonContainerTooLarge,
		},
		{
			name:   "a docx that unpacks past the total limit",
			mutate: func(p *usecase.ProcessPolicy) { p.ZipMaxTotalBytes = 8 },
			body:   validDocx,
			mime:   mimeDOCX,
			want:   domain.RejectReasonContainerTooLarge,
		},
		{
			name:   "a docx that expands beyond the allowed ratio",
			mutate: func(p *usecase.ProcessPolicy) { p.ZipMaxRatio = 1 },
			body: func(t *testing.T) []byte {
				return docxBytes(t, map[string]string{
					"[Content_Types].xml": `<?xml version="1.0"?><Types/>`,
					"word/document.xml":   string(bytes.Repeat([]byte("A"), 400_000)),
				})
			},
			mime: mimeDOCX,
			want: domain.RejectReasonContainerTooLarge,
		},
		{
			name: "a pdf declared as a docx",
			body: func(*testing.T) []byte { return pdfBytes(4) },
			mime: mimeDOCX,
			want: domain.RejectReasonMagicMismatch,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			policy := containerPolicy()
			if test.mutate != nil {
				test.mutate(&policy)
			}

			// Act
			content := process(t, policy, test.body(t), test.mime)

			// Assert
			assert.Equal(t, test.want.String(), content.Reason.String())
		})
	}
}

func TestExifOrientation(t *testing.T) {
	t.Parallel()

	t.Run("reads the tag a camera wrote", func(t *testing.T) {
		t.Parallel()

		for _, want := range []byte{1, 3, 6, 8} {
			// Act
			got := exifOrientation(withOrientation(t, jpegBytes(t, 20, 10), want))

			// Assert
			assert.Equal(t, int(want), got)
		}
	})

	tests := []struct {
		name string
		body func(*testing.T) []byte
	}{
		{
			name: "a jpeg without exif",
			body: func(t *testing.T) []byte { return jpegBytes(t, 20, 10) },
		},
		{
			name: "a png",
			body: func(t *testing.T) []byte { return pngBytes(t, 20, 10) },
		},
		{
			name: "an out of range value",
			body: func(t *testing.T) []byte {
				return withOrientation(t, jpegBytes(t, 20, 10), 9)
			},
		},
		{
			name: "a truncated segment",
			body: func(t *testing.T) []byte {
				return withOrientation(t, jpegBytes(t, 20, 10), 6)[:12]
			},
		},
		{
			name: "not a jpeg at all",
			body: func(*testing.T) []byte { return []byte("hello") },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Act
			got := exifOrientation(test.body(t))

			// Assert
			assert.Equal(t, orientationUnset, got)
		})
	}
}

func TestApplyOrientation(t *testing.T) {
	t.Parallel()

	red := color.RGBA{R: 255, A: 255}

	source := image.NewRGBA(image.Rect(0, 0, 3, 2))

	for y := range 2 {
		for x := range 3 {
			source.Set(x, y, color.RGBA{G: uint8(40 * (x + 1)), B: uint8(90 * (y + 1)), A: 255})
		}
	}

	source.Set(0, 0, red)

	tests := []struct {
		orientation int
		width       int
		height      int
		redAt       image.Point
	}{
		{orientation: orientationTopLeft, width: 3, height: 2, redAt: image.Pt(0, 0)},
		{orientation: orientationTopRight, width: 3, height: 2, redAt: image.Pt(2, 0)},
		{orientation: orientationBottomRight, width: 3, height: 2, redAt: image.Pt(2, 1)},
		{orientation: orientationBottomLeft, width: 3, height: 2, redAt: image.Pt(0, 1)},
		{orientation: orientationLeftTop, width: 2, height: 3, redAt: image.Pt(0, 0)},
		{orientation: orientationRightTop, width: 2, height: 3, redAt: image.Pt(1, 0)},
		{orientation: orientationRightBottom, width: 2, height: 3, redAt: image.Pt(1, 2)},
		{orientation: orientationLeftBottom, width: 2, height: 3, redAt: image.Pt(0, 2)},
	}

	for _, test := range tests {
		t.Run(strconv.Itoa(test.orientation), func(t *testing.T) {
			t.Parallel()

			// Act
			got := applyOrientation(source, test.orientation)

			// Assert
			assert.Equal(t, test.width, got.Bounds().Dx())
			assert.Equal(t, test.height, got.Bounds().Dy())
			assert.Equal(t, test.width != 3, orientationSwapsSides(test.orientation))

			found := image.Pt(-1, -1)

			for y := range got.Bounds().Dy() {
				for x := range got.Bounds().Dx() {
					if got.At(x, y) == color.RGBA(red) {
						found = image.Pt(x, y)
					}
				}
			}

			assert.Equal(t, test.redAt, found,
				"the top-left corner of the source must land where exif says")
		})
	}
}
