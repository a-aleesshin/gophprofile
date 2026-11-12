package domain

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAvatarIDIsAlwaysV7(t *testing.T) {
	t.Parallel()

	t.Run("generates a v7", func(t *testing.T) {
		t.Parallel()

		id, err := NewAvatarID()

		require.NoError(t, err)
		assert.Equal(t, uuid.Version(7), id.UUID().Version())
	})

	t.Run("refuses a v4 from the wire", func(t *testing.T) {
		t.Parallel()

		_, err := ParseAvatarID("6f8c3f7e-0e2e-4a1f-9b7a-2c9b1f0a5d31")

		require.ErrorIs(t, err, ErrInvalidAvatarID,
			"our own ids are v7 for index locality, a v4 cannot be ours")
	})

	t.Run("refuses nonsense", func(t *testing.T) {
		t.Parallel()

		for _, raw := range []string{"", "not-a-uuid", "0193abcd"} {
			_, err := ParseAvatarID(raw)

			require.ErrorIs(t, err, ErrInvalidAvatarID, raw)
		}
	})
}

func TestForeignReferencesDoNotJudgeTheVersion(t *testing.T) {
	t.Parallel()

	v4 := "6f8c3f7e-0e2e-4a1f-9b7a-2c9b1f0a5d31"

	// Act
	fileRef, fileErr := ParseFileRef(v4)
	uploadRef, uploadErr := ParseUploadRef(v4)

	// Assert
	require.NoError(t, fileErr,
		"these ids belong to the file service, its id policy is not ours to enforce")
	require.NoError(t, uploadErr)
	assert.Equal(t, v4, fileRef.String())
	assert.Equal(t, v4, uploadRef.String())
}

func TestForeignReferencesRefuseTheNilUUID(t *testing.T) {
	t.Parallel()

	// Act
	_, fileErr := ParseFileRef(uuid.Nil.String())
	_, uploadErr := ParseUploadRef(uuid.Nil.String())

	// Assert
	require.ErrorIs(t, fileErr, ErrInvalidReference,
		"the nil uuid is how a missing reference looks, it is not a reference")
	require.ErrorIs(t, uploadErr, ErrInvalidReference)
}

func TestUserIDIsOpaqueButBounded(t *testing.T) {
	t.Parallel()

	t.Run("keeps whatever the caller sends", func(t *testing.T) {
		t.Parallel()

		for _, raw := range []string{"user-1", "auth0|abc123", "42", "пользователь-1"} {
			id, err := NewUserID(raw)

			require.NoError(t, err, raw)
			assert.Equal(t, raw, id.String(),
				"profile does not interpret the id, it only stores it")
		}
	})

	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "only spaces", raw: "   "},
		{name: "too long", raw: strings.Repeat("x", maxUserIDLength+1)},
		{name: "a newline inside", raw: "user\n1"},
		{name: "a null byte", raw: "user\x001"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewUserID(test.raw)

			require.ErrorIs(t, err, ErrInvalidUserID)
		})
	}
}

func TestVariantBuildsThePathItWillBeServedFrom(t *testing.T) {
	t.Parallel()

	// Act
	variant, err := NewVariant(NewVariantParams{
		Name: " 300X300 ", Ext: ".JPG", MimeType: " IMAGE/JPEG ", Width: 300, Height: 300,
	})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "300x300", variant.Name())
	assert.Equal(t, "jpg", variant.Ext())
	assert.Equal(t, "image/jpeg", variant.MimeType())
	assert.Equal(t, "300x300.jpg", variant.Path(),
		"the path is half of the url the gallery hands to the browser")
}

func TestVariantRejects(t *testing.T) {
	t.Parallel()

	full := NewVariantParams{
		Name: "300x300", Ext: "jpg", MimeType: "image/jpeg", Width: 300, Height: 300,
	}

	tests := []struct {
		name   string
		mutate func(*NewVariantParams)
	}{
		{name: "no name", mutate: func(p *NewVariantParams) { p.Name = "" }},
		{name: "no extension", mutate: func(p *NewVariantParams) { p.Ext = "" }},
		{name: "no mime", mutate: func(p *NewVariantParams) { p.MimeType = "" }},
		{name: "zero width", mutate: func(p *NewVariantParams) { p.Width = 0 }},
		{name: "negative height", mutate: func(p *NewVariantParams) { p.Height = -1 }},
		{name: "a slash in the name", mutate: func(p *NewVariantParams) { p.Name = "300/300" }},
		{name: "a dot in the extension", mutate: func(p *NewVariantParams) { p.Ext = "jp.g" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			params := full
			test.mutate(&params)

			// Act
			_, err := NewVariant(params)

			// Assert
			require.ErrorIs(t, err, ErrInvalidVariant)
		})
	}
}

func TestFileSnapshotRejects(t *testing.T) {
	t.Parallel()

	full := NewFileSnapshotParams{
		FileName: "a.jpg", MimeType: "image/jpeg", SizeBytes: 1, Width: 1, Height: 1,
	}

	tests := []struct {
		name   string
		mutate func(*NewFileSnapshotParams)
	}{
		{name: "no mime", mutate: func(p *NewFileSnapshotParams) { p.MimeType = "" }},
		{name: "zero size", mutate: func(p *NewFileSnapshotParams) { p.SizeBytes = 0 }},
		{name: "negative width", mutate: func(p *NewFileSnapshotParams) { p.Width = -1 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			params := full
			test.mutate(&params)

			_, err := NewFileSnapshot(params)

			require.ErrorIs(t, err, ErrInvalidSnapshot)
		})
	}

	t.Run("a document has no dimensions and that is fine", func(t *testing.T) {
		t.Parallel()

		snapshot, err := NewFileSnapshot(NewFileSnapshotParams{
			MimeType: "application/pdf", SizeBytes: 2048,
		})

		require.NoError(t, err)
		assert.Zero(t, snapshot.Width())
	})
}
