package domain

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDimensions(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		d, err := NewDimensions(1920, 1080)
		require.NoError(t, err)
		assert.Equal(t, int32(1920), d.Width())
		assert.Equal(t, int32(1080), d.Height())
		assert.False(t, d.IsZero())
	})

	t.Run("square", func(t *testing.T) {
		t.Parallel()

		d, err := NewDimensions(100, 100)
		require.NoError(t, err)
		assert.Equal(t, "100x100", d.String())
	})

	t.Run("rejected", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name          string
			width, height int
		}{
			{name: "both zero", width: 0, height: 0},
			{name: "zero width", width: 0, height: 100},
			{name: "zero height", width: 100, height: 0},
			{name: "negative width", width: -1, height: 100},
			{name: "negative height", width: 100, height: -1},
			{name: "width overflows int32", width: math.MaxInt32 + 1, height: 100},
			{name: "height overflows int32", width: 100, height: math.MaxInt32 + 1},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				d, err := NewDimensions(tt.width, tt.height)
				require.ErrorIs(t, err, ErrInvalidDimensions)
				assert.True(t, d.IsZero())
			})
		}
	})
}

func TestDimensions_Pixels(t *testing.T) {
	t.Parallel()

	t.Run("product", func(t *testing.T) {
		t.Parallel()

		d, err := NewDimensions(1920, 1080)
		require.NoError(t, err)
		assert.Equal(t, int64(2073600), d.Pixels())
	})

	t.Run("largest allowed does not overflow", func(t *testing.T) {
		t.Parallel()

		d, err := NewDimensions(math.MaxInt32, math.MaxInt32)
		require.NoError(t, err)

		got := d.Pixels()
		assert.Positive(t, got)
		assert.Equal(t, int64(math.MaxInt32)*int64(math.MaxInt32), got)
		assert.Less(t, got, int64(math.MaxInt64))
	})

	t.Run("zero", func(t *testing.T) {
		t.Parallel()

		var d Dimensions
		assert.Equal(t, int64(0), d.Pixels())
	})
}

func TestRestoreDimensions(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		d, err := RestoreDimensions(300, 300)
		require.NoError(t, err)
		assert.Equal(t, "300x300", d.String())
	})

	t.Run("round trip with NewDimensions", func(t *testing.T) {
		t.Parallel()

		original, err := NewDimensions(800, 600)
		require.NoError(t, err)

		restored, err := RestoreDimensions(original.Width(), original.Height())
		require.NoError(t, err)
		assert.True(t, original.Equal(restored))
	})

	t.Run("zero is rejected", func(t *testing.T) {
		t.Parallel()

		d, err := RestoreDimensions(0, 0)
		require.ErrorIs(t, err, ErrInvalidDimensions)
		assert.True(t, d.IsZero())
	})
}

func TestDimensions_String(t *testing.T) {
	t.Parallel()

	t.Run("non-zero", func(t *testing.T) {
		t.Parallel()

		d, err := NewDimensions(100, 300)
		require.NoError(t, err)
		assert.Equal(t, "100x300", d.String())
	})

	t.Run("zero", func(t *testing.T) {
		t.Parallel()

		var d Dimensions
		assert.Empty(t, d.String())
	})
}

func TestDimensions_IsZero(t *testing.T) {
	t.Parallel()

	t.Run("zero value", func(t *testing.T) {
		t.Parallel()

		var d Dimensions
		assert.True(t, d.IsZero())
	})

	t.Run("constructed", func(t *testing.T) {
		t.Parallel()

		d, err := NewDimensions(1, 1)
		require.NoError(t, err)
		assert.False(t, d.IsZero())
	})
}

func TestDimensions_Equal(t *testing.T) {
	t.Parallel()

	t.Run("same", func(t *testing.T) {
		t.Parallel()

		first, err := NewDimensions(300, 300)
		require.NoError(t, err)

		second, err := NewDimensions(300, 300)
		require.NoError(t, err)

		assert.True(t, first.Equal(second))
	})

	t.Run("transposed sides are not equal", func(t *testing.T) {
		t.Parallel()

		first, err := NewDimensions(1920, 1080)
		require.NoError(t, err)

		second, err := NewDimensions(1080, 1920)
		require.NoError(t, err)

		assert.False(t, first.Equal(second))
	})

	t.Run("zero vs zero", func(t *testing.T) {
		t.Parallel()

		assert.True(t, Dimensions{}.Equal(Dimensions{}))
	})
}

func TestDimensions_MarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("direct call returns error", func(t *testing.T) {
		t.Parallel()

		d, err := NewDimensions(100, 100)
		require.NoError(t, err)

		data, err := d.MarshalJSON()
		require.Error(t, err)
		assert.Nil(t, data)
	})

	t.Run("nested in dto returns error", func(t *testing.T) {
		t.Parallel()

		d, err := NewDimensions(100, 100)
		require.NoError(t, err)

		data, err := json.Marshal(struct {
			Dimensions Dimensions `json:"dimensions"`
		}{Dimensions: d})
		require.Error(t, err)
		assert.Empty(t, data)
	})
}
