package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewVariantName(t *testing.T) {
	t.Parallel()

	t.Run("accepted", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			raw  string
			want string
		}{
			{raw: "100x100", want: "100x100"},
			{raw: "300x300", want: "300x300"},
			{raw: "web", want: "web"},
			{raw: "original", want: "original"},
			{raw: "  WEB  ", want: "web"},
			{raw: "thumb-small", want: "thumb-small"},
			{raw: strings.Repeat("x", maxVariantNameLength), want: strings.Repeat("x", maxVariantNameLength)},
		}

		for _, tt := range tests {
			t.Run(tt.raw, func(t *testing.T) {
				t.Parallel()

				got, err := NewVariantName(tt.raw)
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
			{name: "too long", raw: strings.Repeat("x", maxVariantNameLength+1)},
			{name: "dot", raw: "300.300"},
			{name: "slash", raw: "300/300"},
			{name: "backslash", raw: `300\300`},
			{name: "underscore", raw: "thumb_small"},
			{name: "space", raw: "thumb small"},
			{name: "leading hyphen", raw: "-web"},
			{name: "trailing hyphen", raw: "web-"},
			{name: "traversal", raw: ".."},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := NewVariantName(tt.raw)
				require.ErrorIs(t, err, ErrInvalidVariantName)
				assert.True(t, got.IsZero())
			})
		}
	})
}

func TestVariantName_IsOriginal(t *testing.T) {
	t.Parallel()

	assert.True(t, VariantNameOriginal.IsOriginal())
	assert.Equal(t, "original", VariantNameOriginal.String())

	derived, err := NewVariantName("300x300")
	require.NoError(t, err)
	assert.False(t, derived.IsOriginal())

	parsed, err := NewVariantName("ORIGINAL")
	require.NoError(t, err)
	assert.True(t, parsed.IsOriginal())
	assert.True(t, parsed.Equal(VariantNameOriginal))

	assert.False(t, VariantName{}.IsOriginal())
}

func TestRestoreVariantName(t *testing.T) {
	t.Parallel()

	got, err := RestoreVariantName("100x100")
	require.NoError(t, err)
	assert.Equal(t, "100x100", got.String())

	for _, raw := range []string{"WEB", " web", "web ", ""} {
		got, err := RestoreVariantName(raw)
		require.ErrorIs(t, err, ErrInvalidVariantName)
		assert.True(t, got.IsZero())
	}
}

func TestVariantName_MarshalJSON(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(struct {
		Name VariantName `json:"name"`
	}{Name: VariantNameOriginal})
	require.Error(t, err)
	assert.Empty(t, data)
}
