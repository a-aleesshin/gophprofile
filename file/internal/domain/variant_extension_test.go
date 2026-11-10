package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewExtension(t *testing.T) {
	t.Parallel()

	t.Run("accepted", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			raw  string
			want string
		}{
			{raw: "webp", want: "webp"},
			{raw: "jpg", want: "jpg"},
			{raw: "png", want: "png"},
			{raw: "pdf", want: "pdf"},
			{raw: "WEBP", want: "webp"},
			{raw: "  webp  ", want: "webp"},
			{raw: ".webp", want: "webp"},
			{raw: "jp2", want: "jp2"},
		}

		for _, tt := range tests {
			t.Run(tt.raw, func(t *testing.T) {
				t.Parallel()

				got, err := NewExtension(tt.raw)
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
			{name: "only dot", raw: "."},
			{name: "whitespace only", raw: "   "},
			{name: "too long", raw: strings.Repeat("x", maxExtensionLength+1)},
			{name: "inner dot", raw: "tar.gz"},
			{name: "slash", raw: "we/bp"},
			{name: "hyphen", raw: "we-bp"},
			{name: "space", raw: "we bp"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := NewExtension(tt.raw)
				require.ErrorIs(t, err, ErrInvalidExtension)
				assert.True(t, got.IsZero())
			})
		}
	})
}

func TestRestoreExtension(t *testing.T) {
	t.Parallel()

	got, err := RestoreExtension("webp")
	require.NoError(t, err)
	assert.Equal(t, "webp", got.String())

	for _, raw := range []string{"WEBP", " webp", ".webp", ""} {
		got, err := RestoreExtension(raw)
		require.ErrorIs(t, err, ErrInvalidExtension)
		assert.True(t, got.IsZero())
	}
}

func TestExtension_EqualAndMarshal(t *testing.T) {
	t.Parallel()

	first, err := NewExtension("webp")
	require.NoError(t, err)

	second, err := NewExtension(".WEBP")
	require.NoError(t, err)

	other, err := NewExtension("jpg")
	require.NoError(t, err)

	assert.True(t, first.Equal(second))
	assert.False(t, first.Equal(other))
	assert.Empty(t, Extension{}.String())

	data, err := json.Marshal(struct {
		Ext Extension `json:"ext"`
	}{Ext: first})
	require.Error(t, err)
	assert.Empty(t, data)
}
