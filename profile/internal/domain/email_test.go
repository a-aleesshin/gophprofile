package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEmailNormalises(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"User@Example.COM",
		"  user@example.com  ",
		"user@example.com",
		"user@example.com\n",
	} {
		// Act
		email, err := NewEmail(raw)

		// Assert
		require.NoError(t, err, raw)
		assert.Equal(t, "user@example.com", email.String(),
			"the same address in any writing must give one hash, or lookup by hash breaks")
	}
}

func TestEmailHashIsTheSha256OfTheNormalisedForm(t *testing.T) {
	t.Parallel()

	// Arrange
	email, err := NewEmail("  User@Example.com ")
	require.NoError(t, err)

	digest := sha256.Sum256([]byte("user@example.com"))

	// Act
	hash := email.Hash()

	// Assert
	assert.Equal(t, hex.EncodeToString(digest[:]), hash.String())
	assert.Len(t, hash.String(), 64)
}

func TestNewEmailRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "no at", raw: "user.example.com"},
		{name: "no local part", raw: "@example.com"},
		{name: "no domain", raw: "user@"},
		{name: "two at signs", raw: "user@host@example.com"},
		{name: "domain without a dot", raw: "user@localhost"},
		{name: "domain starting with a dot", raw: "user@.example.com"},
		{name: "domain ending with a dot", raw: "user@example.com."},
		{name: "inner whitespace", raw: "us er@example.com"},
		{name: "an inner newline", raw: "user@exa\nmple.com"},
		{name: "an inner tab", raw: "user@exa\tmple.com"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Act
			email, err := NewEmail(test.raw)

			// Assert
			require.ErrorIs(t, err, ErrInvalidEmail)
			assert.True(t, email.IsZero())
		})
	}
}

func TestRestoreEmailRefusesANonCanonicalRow(t *testing.T) {
	t.Parallel()

	// Act
	_, err := RestoreEmail("User@Example.com")

	// Assert
	require.ErrorIs(t, err, ErrInvalidEmail,
		"citext compares case-insensitively, but our hash does not")
}

func TestParseEmailHash(t *testing.T) {
	t.Parallel()

	digest := sha256.Sum256([]byte("user@example.com"))
	canonical := hex.EncodeToString(digest[:])

	t.Run("accepts the canonical form and uppercase hex", func(t *testing.T) {
		t.Parallel()

		for _, raw := range []string{canonical, "  " + canonical + "  "} {
			hash, err := ParseEmailHash(raw)

			require.NoError(t, err)
			assert.Equal(t, canonical, hash.String())
		}
	})

	for _, raw := range []string{"", "abc", canonical + "00", "zz" + canonical[2:]} {
		t.Run("rejects "+raw[:min(len(raw), 8)], func(t *testing.T) {
			t.Parallel()

			hash, err := ParseEmailHash(raw)

			require.ErrorIs(t, err, ErrInvalidEmailHash)
			assert.True(t, hash.IsZero())
		})
	}
}

func TestEmailRefusesToMarshal(t *testing.T) {
	t.Parallel()

	email, err := NewEmail("user@example.com")
	require.NoError(t, err)

	_, marshalErr := email.MarshalJSON()
	require.Error(t, marshalErr)

	_, hashErr := email.Hash().MarshalJSON()
	require.Error(t, hashErr)
}
