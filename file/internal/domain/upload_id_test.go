package domain

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUploadID(t *testing.T) {
	t.Parallel()

	id, err := NewUploadID()
	require.NoError(t, err)
	assert.False(t, id.IsZero())

	parsed, err := uuid.Parse(id.String())
	require.NoError(t, err)
	assert.Equal(t, defaultVersion, parsed.Version())

	other, err := NewUploadID()
	require.NoError(t, err)
	assert.False(t, id.Equal(other))
}

func TestParseUploadID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr error
		result  string
	}{
		{name: "empty input", input: "", wantErr: ErrEmptyUploadID},
		{name: "valid uuid v7", input: "01a18818-c65b-732b-9e0a-e8603949ed3d", result: "01a18818-c65b-732b-9e0a-e8603949ed3d"},
		{name: "uppercase", input: "01A18818-C65B-732B-9E0A-E8603949ED3D", result: "01a18818-c65b-732b-9e0a-e8603949ed3d"},
		{name: "invalid uuid", input: "not-a-uuid", wantErr: ErrInvalidUploadID},
		{name: "nil uuid", input: "00000000-0000-0000-0000-000000000000", wantErr: ErrEmptyUploadID},
		{name: "wrong version", input: "a8e0c515-78dd-4508-9f96-68f7a52a8a6a", wantErr: ErrInvalidUploadIDVersion},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			id, err := ParseUploadID(tt.input)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.True(t, id.IsZero())

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.result, id.String())
		})
	}
}

func TestUploadIDFromUUID(t *testing.T) {
	t.Parallel()

	t.Run("does not validate version", func(t *testing.T) {
		t.Parallel()

		v4, err := uuid.Parse("a8e0c515-78dd-4508-9f96-68f7a52a8a6a")
		require.NoError(t, err)

		id := UploadIDFromUUID(v4)
		assert.False(t, id.IsZero())
		assert.Equal(t, "a8e0c515-78dd-4508-9f96-68f7a52a8a6a", id.String())
	})

	t.Run("zero uuid produces zero id", func(t *testing.T) {
		t.Parallel()

		assert.True(t, UploadIDFromUUID(uuid.Nil).IsZero())
		assert.Equal(t, UploadID{}, UploadIDFromUUID(uuid.Nil))
	})

	t.Run("round trip", func(t *testing.T) {
		t.Parallel()

		original, err := NewUploadID()
		require.NoError(t, err)

		assert.True(t, original.Equal(UploadIDFromUUID(original.UUID())))
	})
}

func TestUploadID_MarshalJSON(t *testing.T) {
	t.Parallel()

	id, err := NewUploadID()
	require.NoError(t, err)

	data, err := json.Marshal(struct {
		ID UploadID `json:"id"`
	}{ID: id})
	require.Error(t, err)
	assert.Empty(t, data)
}
