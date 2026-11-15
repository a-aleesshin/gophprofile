package domain

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewChecksum(t *testing.T) {
	t.Parallel()

	t.Run("from sha256 sum", func(t *testing.T) {
		t.Parallel()

		sum := sha256.Sum256([]byte("gophprofile"))

		got, err := NewChecksum(sum[:])
		require.NoError(t, err)
		assert.False(t, got.IsZero())
		assert.Len(t, got.String(), 64)
		assert.Equal(t, strings.ToLower(got.String()), got.String())
		assert.Equal(t, sum, got.Bytes())
	})

	t.Run("rejected", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			sum  []byte
		}{
			{name: "nil", sum: nil},
			{name: "empty", sum: []byte{}},
			{name: "too short", sum: make([]byte, sha256.Size-1)},
			{name: "too long", sum: make([]byte, sha256.Size+1)},
			{name: "all zeroes", sum: make([]byte, sha256.Size)},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := NewChecksum(tt.sum)
				require.ErrorIs(t, err, ErrInvalidChecksum)
				assert.True(t, got.IsZero())
			})
		}
	})
}

func TestRestoreChecksum(t *testing.T) {
	t.Parallel()

	t.Run("round trip", func(t *testing.T) {
		t.Parallel()

		sum := sha256.Sum256([]byte("gophprofile"))

		original, err := NewChecksum(sum[:])
		require.NoError(t, err)

		restored, err := RestoreChecksum(original.String())
		require.NoError(t, err)
		assert.True(t, original.Equal(restored))
		assert.Equal(t, original.String(), restored.String())
	})

	t.Run("rejected", func(t *testing.T) {
		t.Parallel()

		valid := strings.Repeat("ab", 32)

		tests := []struct {
			name string
			raw  string
		}{
			{name: "empty", raw: ""},
			{name: "too short", raw: valid[:63]},
			{name: "too long", raw: valid + "c"},
			{name: "uppercase", raw: strings.ToUpper(valid)},
			{name: "not hex", raw: strings.Repeat("zz", 32)},
			{name: "all zeroes", raw: strings.Repeat("0", 64)},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := RestoreChecksum(tt.raw)
				require.ErrorIs(t, err, ErrInvalidChecksum)
				assert.True(t, got.IsZero())
			})
		}
	})
}

func TestChecksum_ZeroValue(t *testing.T) {
	t.Parallel()

	var c Checksum
	assert.True(t, c.IsZero())
	assert.Empty(t, c.String())
}

func TestChecksum_MarshalJSON(t *testing.T) {
	t.Parallel()

	sum := sha256.Sum256([]byte("gophprofile"))

	c, err := NewChecksum(sum[:])
	require.NoError(t, err)

	data, err := json.Marshal(struct {
		Checksum Checksum `json:"checksum"`
	}{Checksum: c})
	require.Error(t, err)
	assert.Empty(t, data)
}
