package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testNewVariantParams(t *testing.T) NewVariantParams {
	t.Helper()

	mimeType, err := NewMimeType("image/webp")
	require.NoError(t, err)

	size, err := NewSize(4096)
	require.NoError(t, err)

	dimensions, err := NewDimensions(300, 300)
	require.NoError(t, err)

	return NewVariantParams{
		FileID:     testFileID(t),
		Name:       testVariantName(t, "300x300"),
		Ext:        testExtension(t, "webp"),
		MimeType:   mimeType,
		Dimensions: dimensions,
		Size:       size,
		Checksum:   testChecksum(t),
		Now:        testNow,
	}
}

func TestNewVariant(t *testing.T) {
	t.Parallel()

	t.Run("derived variant", func(t *testing.T) {
		t.Parallel()

		v, err := NewVariant(testNewVariantParams(t))
		require.NoError(t, err)

		assert.Equal(t, "300x300", v.Name().String())
		assert.Equal(t, "webp", v.Ext().String())
		assert.Equal(t, "image/webp", v.MimeType().String())
		assert.Equal(t, int64(4096), v.Size().Bytes())
		assert.Equal(t, "300x300", v.Dimensions().String())
		assert.False(t, v.Checksum().IsZero())
		assert.False(t, v.IsOriginal())
		assert.Equal(t, testNow, v.CreatedAt())
		assert.Equal(t,
			"variants/01a18818-c65b-732b-9e0a-e8603949ed3d/300x300.webp",
			v.Location().String())
	})

	t.Run("original lives under the originals prefix", func(t *testing.T) {
		t.Parallel()

		p := testNewVariantParams(t)
		p.Name = VariantNameOriginal
		p.Ext = testExtension(t, "jpg")

		v, err := NewVariant(p)
		require.NoError(t, err)

		assert.True(t, v.IsOriginal())
		assert.Equal(t, "originals/01a18818-c65b-732b-9e0a-e8603949ed3d", v.Location().String())
	})

	t.Run("document original has no dimensions", func(t *testing.T) {
		t.Parallel()

		mimeType, err := NewMimeType("application/pdf")
		require.NoError(t, err)

		p := testNewVariantParams(t)
		p.Name = VariantNameOriginal
		p.Ext = testExtension(t, "pdf")
		p.MimeType = mimeType
		p.Dimensions = Dimensions{}

		v, err := NewVariant(p)
		require.NoError(t, err)
		assert.True(t, v.Dimensions().IsZero())
		assert.Equal(t, "application/pdf", v.MimeType().String())
	})

	t.Run("timestamp is normalized to utc", func(t *testing.T) {
		t.Parallel()

		p := testNewVariantParams(t)
		p.Now = testNow.In(time.FixedZone("MSK", 3*60*60))

		v, err := NewVariant(p)
		require.NoError(t, err)
		assert.Equal(t, time.UTC, v.CreatedAt().Location())
	})

	t.Run("required fields", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*NewVariantParams)
		}{
			{name: "file id", mutate: func(p *NewVariantParams) { p.FileID = FileID{} }},
			{name: "name", mutate: func(p *NewVariantParams) { p.Name = VariantName{} }},
			{name: "extension", mutate: func(p *NewVariantParams) { p.Ext = Extension{} }},
			{name: "mime type", mutate: func(p *NewVariantParams) { p.MimeType = MimeType{} }},
			{name: "size", mutate: func(p *NewVariantParams) { p.Size = Size{} }},
			{name: "checksum", mutate: func(p *NewVariantParams) { p.Checksum = Checksum{} }},
			{name: "now", mutate: func(p *NewVariantParams) { p.Now = time.Time{} }},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				p := testNewVariantParams(t)
				tt.mutate(&p)

				v, err := NewVariant(p)
				require.ErrorIs(t, err, ErrInvalidVariant)
				assert.Equal(t, Variant{}, v)
			})
		}
	})
}

func TestVariant_MatchesExt(t *testing.T) {
	t.Parallel()

	v, err := NewVariant(testNewVariantParams(t))
	require.NoError(t, err)

	assert.True(t, v.MatchesExt(testExtension(t, "webp")))
	assert.False(t, v.MatchesExt(testExtension(t, "jpg")))
	assert.False(t, v.MatchesExt(Extension{}))
}

func testRestoreVariantParams(t *testing.T) RestoreVariantParams {
	t.Helper()

	p := testNewVariantParams(t)

	location, err := NewVariantLocation(p.FileID, p.Name, p.Ext)
	require.NoError(t, err)

	return RestoreVariantParams{
		FileID:     p.FileID,
		Name:       p.Name,
		Ext:        p.Ext,
		MimeType:   p.MimeType,
		Dimensions: p.Dimensions,
		Size:       p.Size,
		Checksum:   p.Checksum,
		Location:   location,
		CreatedAt:  testNow,
	}
}

func TestRestoreVariant(t *testing.T) {
	t.Parallel()

	t.Run("round trip", func(t *testing.T) {
		t.Parallel()

		v, err := RestoreVariant(testRestoreVariantParams(t))
		require.NoError(t, err)
		assert.Equal(t, "300x300", v.Name().String())
		assert.Equal(t,
			"variants/01a18818-c65b-732b-9e0a-e8603949ed3d/300x300.webp",
			v.Location().String())
	})

	t.Run("keeps a legacy location shape", func(t *testing.T) {
		t.Parallel()

		legacy, err := RestoreLocation("thumbnails/01a18818-c65b-732b-9e0a-e8603949ed3d/300x300.jpg")
		require.NoError(t, err)

		p := testRestoreVariantParams(t)
		p.Location = legacy

		v, err := RestoreVariant(p)
		require.NoError(t, err)
		assert.True(t, v.Location().Equal(legacy))
	})

	t.Run("creation rules are not reapplied", func(t *testing.T) {
		t.Parallel()

		p := testRestoreVariantParams(t)
		p.MimeType = MimeType{}
		p.Size = Size{}
		p.Checksum = Checksum{}

		v, err := RestoreVariant(p)
		require.NoError(t, err)
		assert.True(t, v.Checksum().IsZero())
	})

	t.Run("required fields", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*RestoreVariantParams)
		}{
			{name: "file id", mutate: func(p *RestoreVariantParams) { p.FileID = FileID{} }},
			{name: "name", mutate: func(p *RestoreVariantParams) { p.Name = VariantName{} }},
			{name: "extension", mutate: func(p *RestoreVariantParams) { p.Ext = Extension{} }},
			{name: "location", mutate: func(p *RestoreVariantParams) { p.Location = Location{} }},
			{name: "created at", mutate: func(p *RestoreVariantParams) { p.CreatedAt = time.Time{} }},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				p := testRestoreVariantParams(t)
				tt.mutate(&p)

				v, err := RestoreVariant(p)
				require.ErrorIs(t, err, ErrInvalidVariant)
				assert.Equal(t, Variant{}, v)
			})
		}
	})
}

func TestVariant_MarshalJSON(t *testing.T) {
	t.Parallel()

	v, err := NewVariant(testNewVariantParams(t))
	require.NoError(t, err)

	data, err := json.Marshal(&v)
	require.Error(t, err)
	assert.Empty(t, data)
}
