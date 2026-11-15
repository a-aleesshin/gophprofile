package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewNamespace(t *testing.T) {
	t.Parallel()

	t.Run("normalized", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			raw  string
			want string
		}{
			{raw: "avatar", want: "avatar"},
			{raw: "document", want: "document"},
			{raw: "  AVATAR  ", want: "avatar"},
			{raw: "user-banner", want: "user-banner"},
			{raw: "a", want: "a"},
			{raw: "ns2", want: "ns2"},
			{raw: strings.Repeat("x", maxNamespaceLength), want: strings.Repeat("x", maxNamespaceLength)},
		}

		for _, tt := range tests {
			t.Run(tt.raw, func(t *testing.T) {
				t.Parallel()

				got, err := NewNamespace(tt.raw)
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
			{name: "too long", raw: strings.Repeat("x", maxNamespaceLength+1)},
			{name: "leading digit", raw: "1avatar"},
			{name: "leading hyphen", raw: "-avatar"},
			{name: "underscore", raw: "user_banner"},
			{name: "inner space", raw: "user banner"},
			{name: "dot", raw: "user.banner"},
			{name: "slash", raw: "user/banner"},
			{name: "non ascii", raw: "аватар"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := NewNamespace(tt.raw)
				require.ErrorIs(t, err, ErrInvalidNamespace)
				assert.True(t, got.IsZero())
			})
		}
	})
}

func TestRestoreNamespace(t *testing.T) {
	t.Parallel()

	t.Run("canonical", func(t *testing.T) {
		t.Parallel()

		got, err := RestoreNamespace("avatar")
		require.NoError(t, err)
		assert.Equal(t, "avatar", got.String())
	})

	t.Run("rejects non canonical", func(t *testing.T) {
		t.Parallel()

		for _, raw := range []string{"AVATAR", " avatar", "avatar "} {
			t.Run(raw, func(t *testing.T) {
				t.Parallel()

				got, err := RestoreNamespace(raw)
				require.ErrorIs(t, err, ErrInvalidNamespace)
				assert.True(t, got.IsZero())
			})
		}
	})
}

func TestNamespace_EqualAndMarshal(t *testing.T) {
	t.Parallel()

	first, err := NewNamespace("avatar")
	require.NoError(t, err)

	second, err := NewNamespace("AVATAR")
	require.NoError(t, err)

	other, err := NewNamespace("document")
	require.NoError(t, err)

	assert.True(t, first.Equal(second))
	assert.False(t, first.Equal(other))
	assert.Empty(t, Namespace{}.String())

	data, err := json.Marshal(struct {
		Namespace Namespace `json:"namespace"`
	}{Namespace: first})
	require.Error(t, err)
	assert.Empty(t, data)
}
