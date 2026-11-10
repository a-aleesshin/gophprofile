package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const docxMimeType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

func TestNewMimeType(t *testing.T) {
	t.Parallel()

	t.Run("normalized", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			raw  string
			want string
		}{
			{name: "jpeg", raw: "image/jpeg", want: "image/jpeg"},
			{name: "uppercase", raw: "IMAGE/JPEG", want: "image/jpeg"},
			{name: "surrounding whitespace", raw: "  image/png  ", want: "image/png"},
			{name: "parameters dropped", raw: "image/jpeg; charset=utf-8", want: "image/jpeg"},
			{name: "pdf", raw: "application/pdf", want: "application/pdf"},
			{name: "docx", raw: docxMimeType, want: docxMimeType},
			{name: "plus suffix", raw: "image/svg+xml", want: "image/svg+xml"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := NewMimeType(tt.raw)
				require.NoError(t, err)
				assert.Equal(t, tt.want, got.String())
			})
		}
	})

	t.Run("rejected", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			raw  string
		}{
			{name: "empty", raw: ""},
			{name: "whitespace only", raw: "   "},
			{name: "no subtype", raw: "image"},
			{name: "empty subtype", raw: "image/"},
			{name: "empty type", raw: "/jpeg"},
			{name: "inner space", raw: "image/jp eg"},
			{name: "too long", raw: "image/" + strings.Repeat("x", maxMimeTypeLength)},
			{name: "two slashes", raw: "image/jpeg/extra"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := NewMimeType(tt.raw)
				require.ErrorIs(t, err, ErrInvalidMimeType)
				assert.True(t, got.IsZero())
			})
		}
	})
}

func TestRestoreMimeType(t *testing.T) {
	t.Parallel()

	t.Run("canonical", func(t *testing.T) {
		t.Parallel()

		for _, raw := range []string{"image/jpeg", "application/pdf", docxMimeType} {
			t.Run(raw, func(t *testing.T) {
				t.Parallel()

				got, err := RestoreMimeType(raw)
				require.NoError(t, err)
				assert.Equal(t, raw, got.String())
			})
		}
	})

	t.Run("rejects non canonical", func(t *testing.T) {
		t.Parallel()

		for _, raw := range []string{"IMAGE/JPEG", "image/jpeg; charset=utf-8", " image/jpeg"} {
			t.Run(raw, func(t *testing.T) {
				t.Parallel()

				got, err := RestoreMimeType(raw)
				require.ErrorIs(t, err, ErrInvalidMimeType)
				assert.True(t, got.IsZero())
			})
		}
	})
}

func TestMimeType_FitsSchemaColumn(t *testing.T) {
	t.Parallel()

	assert.Len(t, docxMimeType, 71)
	assert.LessOrEqual(t, len(docxMimeType), maxMimeTypeLength)
}

func TestMimeType_EqualAndMarshal(t *testing.T) {
	t.Parallel()

	first, err := NewMimeType("image/jpeg")
	require.NoError(t, err)

	second, err := NewMimeType("IMAGE/JPEG; charset=utf-8")
	require.NoError(t, err)

	assert.True(t, first.Equal(second))
	assert.Empty(t, MimeType{}.String())

	data, err := json.Marshal(struct {
		MimeType MimeType `json:"mime_type"`
	}{MimeType: first})
	require.Error(t, err)
	assert.Empty(t, data)
}
