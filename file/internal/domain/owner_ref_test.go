package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOwnerRef(t *testing.T) {
	t.Parallel()

	t.Run("accepted", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			raw  string
			want string
		}{
			{name: "plain id", raw: "user-123", want: "user-123"},
			{name: "uuid", raw: "01a18818-c65b-732b-9e0a-e8603949ed3d", want: "01a18818-c65b-732b-9e0a-e8603949ed3d"},
			{name: "trimmed", raw: "  user-123  ", want: "user-123"},
			{name: "case preserved", raw: "User-123", want: "User-123"},
			{name: "opaque to us", raw: "tenant/42:user@example.com", want: "tenant/42:user@example.com"},
			{name: "non ascii", raw: "пользователь-1", want: "пользователь-1"},
			{name: "max length", raw: strings.Repeat("x", maxOwnerRefLength), want: strings.Repeat("x", maxOwnerRefLength)},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := NewOwnerRef(tt.raw)
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
			{name: "too long", raw: strings.Repeat("x", maxOwnerRefLength+1)},
			{name: "newline", raw: "user\n123"},
			{name: "null byte", raw: "user\x00123"},
			{name: "invalid utf8", raw: "user\xff"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := NewOwnerRef(tt.raw)
				require.ErrorIs(t, err, ErrInvalidOwnerRef)
				assert.True(t, got.IsZero())
			})
		}
	})
}

func TestRestoreOwnerRef(t *testing.T) {
	t.Parallel()

	got, err := RestoreOwnerRef("user-123")
	require.NoError(t, err)
	assert.Equal(t, "user-123", got.String())

	for _, raw := range []string{" user-123", "user-123 ", ""} {
		got, err := RestoreOwnerRef(raw)
		require.ErrorIs(t, err, ErrInvalidOwnerRef)
		assert.True(t, got.IsZero())
	}
}

func TestOwnerRef_EqualAndMarshal(t *testing.T) {
	t.Parallel()

	first, err := NewOwnerRef("user-123")
	require.NoError(t, err)

	second, err := NewOwnerRef("  user-123 ")
	require.NoError(t, err)

	other, err := NewOwnerRef("user-456")
	require.NoError(t, err)

	assert.True(t, first.Equal(second))
	assert.False(t, first.Equal(other))
	assert.Empty(t, OwnerRef{}.String())

	data, err := json.Marshal(struct {
		OwnerRef OwnerRef `json:"owner_ref"`
	}{OwnerRef: first})
	require.Error(t, err)
	assert.Empty(t, data)
}
