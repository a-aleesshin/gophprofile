package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testFileID(t *testing.T) FileID {
	t.Helper()

	id, err := ParseFileID("01a18818-c65b-732b-9e0a-e8603949ed3d")
	require.NoError(t, err)

	return id
}

func TestNewOriginalLocation(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		id := testFileID(t)

		got, err := NewOriginalLocation(id)
		require.NoError(t, err)
		assert.Equal(t, "originals/01a18818-c65b-732b-9e0a-e8603949ed3d", got.String())
		assert.False(t, got.IsZero())
	})

	t.Run("zero file id is rejected", func(t *testing.T) {
		t.Parallel()

		got, err := NewOriginalLocation(FileID{})
		require.ErrorIs(t, err, ErrInvalidLocation)
		assert.True(t, got.IsZero())
	})
}

func TestNewVariantLocation(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		got, err := NewVariantLocation(testFileID(t), testVariantName(t, "300x300"), testExtension(t, "webp"))
		require.NoError(t, err)
		assert.Equal(t, "variants/01a18818-c65b-732b-9e0a-e8603949ed3d/300x300.webp", got.String())
	})

	t.Run("rejected", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			id   FileID
			vn   VariantName
			ext  Extension
		}{
			{name: "zero file id", id: FileID{}, vn: testVariantName(t, "300x300"), ext: testExtension(t, "webp")},
			{name: "zero name", id: testFileID(t), vn: VariantName{}, ext: testExtension(t, "webp")},
			{name: "zero extension", id: testFileID(t), vn: testVariantName(t, "300x300"), ext: Extension{}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := NewVariantLocation(tt.id, tt.vn, tt.ext)
				require.ErrorIs(t, err, ErrInvalidLocation)
				assert.True(t, got.IsZero())
			})
		}
	})
}

func testVariantName(t *testing.T, raw string) VariantName {
	t.Helper()

	name, err := NewVariantName(raw)
	require.NoError(t, err)

	return name
}

func testExtension(t *testing.T, raw string) Extension {
	t.Helper()

	ext, err := NewExtension(raw)
	require.NoError(t, err)

	return ext
}

func TestRestoreLocation(t *testing.T) {
	t.Parallel()

	t.Run("accepted", func(t *testing.T) {
		t.Parallel()

		tests := []string{
			"originals/01a18818-c65b-732b-9e0a-e8603949ed3d",
			"variants/01a18818-c65b-732b-9e0a-e8603949ed3d/100x100.webp",
			"staging/01a18818-c65b-732b-9e0a-e8603949ed3d",
			"flat-key",
			strings.Repeat("x", maxLocationLength),
		}

		for _, raw := range tests {
			t.Run(raw, func(t *testing.T) {
				t.Parallel()

				got, err := RestoreLocation(raw)
				require.NoError(t, err)
				assert.Equal(t, raw, got.String())
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
			{name: "too long", raw: strings.Repeat("x", maxLocationLength+1)},
			{name: "invalid utf8", raw: "originals/a\xffb"},
			{name: "leading slash", raw: "/originals/a"},
			{name: "trailing slash", raw: "originals/a/"},
			{name: "empty segment", raw: "originals//a"},
			{name: "backslash", raw: `originals\a`},
			{name: "parent segment", raw: "originals/../secrets/a"},
			{name: "current segment", raw: "originals/./a"},
			{name: "bare parent", raw: ".."},
			{name: "control character", raw: "originals/a\x00b"},
			{name: "newline", raw: "originals/a\nb"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := RestoreLocation(tt.raw)
				require.ErrorIs(t, err, ErrInvalidLocation)
				assert.True(t, got.IsZero())
			})
		}
	})

	t.Run("round trip with NewVariantLocation", func(t *testing.T) {
		t.Parallel()

		id := testFileID(t)

		original, err := NewVariantLocation(id, testVariantName(t, "100x100"), testExtension(t, "webp"))
		require.NoError(t, err)

		restored, err := RestoreLocation(original.String())
		require.NoError(t, err)
		assert.True(t, original.Equal(restored))
	})
}

func TestLocation_String(t *testing.T) {
	t.Parallel()

	t.Run("zero", func(t *testing.T) {
		t.Parallel()

		var l Location
		assert.Empty(t, l.String())
	})
}

func TestLocation_Equal(t *testing.T) {
	t.Parallel()

	t.Run("different keys", func(t *testing.T) {
		t.Parallel()

		id := testFileID(t)

		first, err := NewVariantLocation(id, testVariantName(t, "100x100"), testExtension(t, "webp"))
		require.NoError(t, err)

		second, err := NewVariantLocation(id, testVariantName(t, "300x300"), testExtension(t, "webp"))
		require.NoError(t, err)

		assert.False(t, first.Equal(second))
	})

	t.Run("zero vs zero", func(t *testing.T) {
		t.Parallel()

		assert.True(t, Location{}.Equal(Location{}))
	})
}

func TestLocation_MarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("nested in dto returns error", func(t *testing.T) {
		t.Parallel()

		l, err := NewOriginalLocation(testFileID(t))
		require.NoError(t, err)

		data, err := json.Marshal(struct {
			Location Location `json:"location"`
		}{Location: l})
		require.Error(t, err)
		assert.Empty(t, data)
	})
}

func TestNewStagingLocation(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		id, err := ParseUploadID("01a19119-c7c0-748d-b97c-ebfd9d4263d7")
		require.NoError(t, err)

		got, err := NewStagingLocation(id)
		require.NoError(t, err)
		assert.Equal(t, "staging/01a19119-c7c0-748d-b97c-ebfd9d4263d7", got.String())
	})

	t.Run("zero upload id is rejected", func(t *testing.T) {
		t.Parallel()

		got, err := NewStagingLocation(UploadID{})
		require.ErrorIs(t, err, ErrInvalidLocation)
		assert.True(t, got.IsZero())
	})
}
