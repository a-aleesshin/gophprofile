package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewIdempotencyKey(t *testing.T) {
	t.Parallel()

	t.Run("accepted", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			raw  string
			want string
		}{
			{name: "uuid", raw: "01a18818-c65b-732b-9e0a-e8603949ed3d", want: "01a18818-c65b-732b-9e0a-e8603949ed3d"},
			{name: "trimmed", raw: "  key-1  ", want: "key-1"},
			{name: "opaque", raw: "avatar:user-123:1", want: "avatar:user-123:1"},
			{name: "max length", raw: strings.Repeat("x", maxIdempotencyKeyLength), want: strings.Repeat("x", maxIdempotencyKeyLength)},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := NewIdempotencyKey(tt.raw)
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
			{name: "too long", raw: strings.Repeat("x", maxIdempotencyKeyLength+1)},
			{name: "newline", raw: "key\n1"},
			{name: "invalid utf8", raw: "key\xff"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := NewIdempotencyKey(tt.raw)
				require.ErrorIs(t, err, ErrInvalidIdempotencyKey)
				assert.True(t, got.IsZero())
			})
		}
	})
}

func TestRestoreIdempotencyKey(t *testing.T) {
	t.Parallel()

	got, err := RestoreIdempotencyKey("key-1")
	require.NoError(t, err)
	assert.Equal(t, "key-1", got.String())

	for _, raw := range []string{" key-1", "key-1 ", ""} {
		got, err := RestoreIdempotencyKey(raw)
		require.ErrorIs(t, err, ErrInvalidIdempotencyKey)
		assert.True(t, got.IsZero())
	}
}

func TestIdempotencyKey_EqualAndMarshal(t *testing.T) {
	t.Parallel()

	first, err := NewIdempotencyKey("key-1")
	require.NoError(t, err)

	second, err := NewIdempotencyKey(" key-1 ")
	require.NoError(t, err)

	other, err := NewIdempotencyKey("key-2")
	require.NoError(t, err)

	assert.True(t, first.Equal(second))
	assert.False(t, first.Equal(other))
	assert.Empty(t, IdempotencyKey{}.String())

	data, err := json.Marshal(struct {
		Key IdempotencyKey `json:"key"`
	}{Key: first})
	require.Error(t, err)
	assert.Empty(t, data)
}
