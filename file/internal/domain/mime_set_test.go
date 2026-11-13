package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testMime(t *testing.T, raw string) MimeType {
	t.Helper()

	value, err := NewMimeType(raw)
	require.NoError(t, err)

	return value
}

func TestNewMimeSet(t *testing.T) {
	t.Parallel()

	t.Run("membership", func(t *testing.T) {
		t.Parallel()

		set, err := NewMimeSet(testMime(t, "image/jpeg"), testMime(t, "image/png"))
		require.NoError(t, err)

		assert.Equal(t, 2, set.Len())
		assert.True(t, set.Contains(testMime(t, "image/jpeg")))
		assert.True(t, set.Contains(testMime(t, "IMAGE/PNG")), "membership follows normalization")
		assert.False(t, set.Contains(testMime(t, "image/gif")))
		assert.False(t, set.Contains(MimeType{}), "the zero type belongs to no set")
	})

	t.Run("rejected", func(t *testing.T) {
		t.Parallel()

		_, err := NewMimeSet()
		require.ErrorIs(t, err, ErrInvalidMimeSet)

		_, err = NewMimeSet(testMime(t, "image/jpeg"), MimeType{})
		require.ErrorIs(t, err, ErrInvalidMimeSet)

		_, err = NewMimeSet(testMime(t, "image/jpeg"), testMime(t, "image/jpeg"))
		require.ErrorIs(t, err, ErrInvalidMimeSet,
			"a duplicate in a policy file is a mistake worth reporting at startup")
	})

	t.Run("zero value", func(t *testing.T) {
		t.Parallel()

		var set MimeSet

		assert.True(t, set.IsZero())
		assert.Zero(t, set.Len())
		assert.False(t, set.Contains(testMime(t, "image/jpeg")),
			"an unset policy permits nothing rather than everything")
		assert.Empty(t, set.String())
	})

	t.Run("string lists every member", func(t *testing.T) {
		t.Parallel()

		set, err := NewMimeSet(testMime(t, "image/jpeg"), testMime(t, "image/png"))
		require.NoError(t, err)
		assert.Equal(t, "image/jpeg, image/png", set.String())
	})

	t.Run("marshal is refused", func(t *testing.T) {
		t.Parallel()

		set, err := NewMimeSet(testMime(t, "image/jpeg"))
		require.NoError(t, err)

		data, err := json.Marshal(struct {
			Allowed MimeSet `json:"allowed"`
		}{Allowed: set})
		require.Error(t, err)
		assert.Empty(t, data)
	})
}
