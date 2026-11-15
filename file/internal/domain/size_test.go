package domain

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSize(t *testing.T) {
	t.Parallel()

	t.Run("accepted", func(t *testing.T) {
		t.Parallel()

		for _, bytes := range []int64{1, 1024, 10 << 20, 50 << 20, math.MaxInt64} {
			got, err := NewSize(bytes)
			require.NoError(t, err)
			assert.Equal(t, bytes, got.Bytes())
			assert.False(t, got.IsZero())
		}
	})

	t.Run("rejected", func(t *testing.T) {
		t.Parallel()

		for _, bytes := range []int64{0, -1, math.MinInt64} {
			got, err := NewSize(bytes)
			require.ErrorIs(t, err, ErrInvalidSize)
			assert.True(t, got.IsZero())
		}
	})
}

func TestSize_ZeroValue(t *testing.T) {
	t.Parallel()

	var s Size
	assert.True(t, s.IsZero())
	assert.Equal(t, int64(0), s.Bytes())
}

func TestSize_EqualAndMarshal(t *testing.T) {
	t.Parallel()

	first, err := NewSize(1024)
	require.NoError(t, err)

	second, err := NewSize(1024)
	require.NoError(t, err)

	other, err := NewSize(2048)
	require.NoError(t, err)

	assert.True(t, first.Equal(second))
	assert.False(t, first.Equal(other))

	data, err := json.Marshal(struct {
		Size Size `json:"size"`
	}{Size: first})
	require.Error(t, err)
	assert.Empty(t, data)
}
