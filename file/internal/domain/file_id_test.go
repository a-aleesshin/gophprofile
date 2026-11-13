package domain

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFileID(t *testing.T) {
	t.Parallel()

	// Act
	id, err := NewFileID()

	// Assert
	require.NoError(t, err, "unexpected error")
	require.False(t, id.IsZero(), "expected non-zero ID, got zero")

	stringUUID := id.String()
	require.NotEmpty(t, stringUUID, "NewFileID().String() is empty")

	uuidData, err := uuid.Parse(stringUUID)
	require.NoErrorf(t, err, "uuid.Parse(%q) error", stringUUID)
	assert.Equal(t, defaultVersion, uuidData.Version(), "unexpected uuid version")
}

func TestNewFileID_Unique(t *testing.T) {
	t.Parallel()

	first, err := NewFileID()
	require.NoError(t, err, "NewFileID() error")

	second, err := NewFileID()
	require.NoError(t, err, "NewFileID() error")

	assert.Falsef(t, first.Equal(second), "two calls returned the same id %q", first.String())
}

func TestParseFileID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr error
		result  string
	}{
		{name: "empty input", input: "", wantErr: ErrEmptyFileID},
		{name: "valid uuid v7", input: "01a18818-c65b-732b-9e0a-e8603949ed3d", wantErr: nil, result: "01a18818-c65b-732b-9e0a-e8603949ed3d"},
		{name: "valid uuid v7 uppercase", input: "01A18818-C65B-732B-9E0A-E8603949ED3D", wantErr: nil, result: "01a18818-c65b-732b-9e0a-e8603949ed3d"},
		{name: "invalid uuid", input: "not-a-uuid", wantErr: ErrInvalidFileID},
		{name: "empty uuid", input: "00000000-0000-0000-0000-000000000000", wantErr: ErrEmptyFileID},
		{name: "wrong version", input: "a8e0c515-78dd-4508-9f96-68f7a52a8a6a", wantErr: ErrInvalidFileIDVersion},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Act
			id, err := ParseFileID(tt.input)

			// Assert
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Truef(t, id.IsZero(), "expected zero ID on error, got %q", id.String())
				return
			}

			require.NoError(t, err, "unexpected error")
			assert.Equalf(t, tt.result, id.String(), "String() mismatch")
		})
	}
}

func TestFileID_String(t *testing.T) {
	t.Parallel()

	t.Run("non-zero", func(t *testing.T) {
		t.Parallel()

		// Arrange
		const strID = "01a18cfc-76b1-7090-b672-2277ff590b04"

		// Act
		id, err := ParseFileID(strID)

		// Assert
		require.NoError(t, err, "unexpected error")
		assert.Equal(t, strID, id.String())
	})

	t.Run("zero", func(t *testing.T) {
		t.Parallel()

		// Arrange
		var id FileID

		// Assert
		assert.Empty(t, id.String())
	})
}

func TestFileID_IsZero(t *testing.T) {
	t.Parallel()

	t.Run("correct", func(t *testing.T) {
		t.Parallel()

		// Arrange
		const strID = "01a18cfc-76b1-7090-b672-2277ff590b04"

		// Act
		id, err := ParseFileID(strID)
		require.NoError(t, err, "unexpected error")

		// Assert
		assert.False(t, id.IsZero())
	})

	t.Run("incorrect", func(t *testing.T) {
		t.Parallel()

		// Arrange
		var id FileID

		// Assert
		assert.True(t, id.IsZero())
	})
}

func TestFileID_Equal(t *testing.T) {
	t.Parallel()

	t.Run("same value", func(t *testing.T) {
		t.Parallel()

		const strID = "01a18cfc-76b1-7090-b672-2277ff590b04"

		id, err := ParseFileID(strID)
		require.NoError(t, err, "unexpected error")

		idOther, err := ParseFileID(strID)
		require.NoError(t, err, "unexpected error")

		assert.Truef(t, id.Equal(idOther), "FileID.Equal(%v) = false, want true", id)
	})

	t.Run("different values", func(t *testing.T) {
		t.Parallel()

		const strID = "01a18cfc-76b1-7090-b672-2277ff590b04"
		const strIDWant = "01a19119-c7c0-748d-b97c-ebfd9d4263d7"

		id, err := ParseFileID(strID)
		require.NoError(t, err, "unexpected error")

		idOther, err := ParseFileID(strIDWant)
		require.NoError(t, err, "unexpected error")

		assert.Falsef(t, id.Equal(idOther), "FileID.Equal(%v) = true, want false", id)
	})

	t.Run("zero vs zero", func(t *testing.T) {
		t.Parallel()

		idZero := FileID{}
		idOtherZero := FileID{}

		assert.True(t, idZero.Equal(idOtherZero))
	})

	t.Run("zero vs non-zero", func(t *testing.T) {
		t.Parallel()

		idZero := FileID{}

		idOther, err := ParseFileID("01a19119-c7c0-748d-b97c-ebfd9d4263d7")
		require.NoError(t, err, "unexpected error")

		assert.False(t, idZero.Equal(idOther))
	})
}

func TestFileID_RoundTrip(t *testing.T) {
	t.Parallel()

	// Act
	id, err := NewFileID()
	require.NoError(t, err, "NewFileID() error")

	parsed, err := ParseFileID(id.String())
	require.NoErrorf(t, err, "ParseFileID(%q) error", id.String())

	// Assert
	assert.True(t, id.Equal(parsed), "id.Equal(parsed) = false, want true")
	assert.Equal(t, id.String(), parsed.String())
}

func TestFileIDFromUUID(t *testing.T) {
	t.Parallel()

	t.Run("valid uuid v7", func(t *testing.T) {
		t.Parallel()

		// Arrange
		const strID = "01a18818-c65b-732b-9e0a-e8603949ed3d"
		u, err := uuid.Parse(strID)
		require.NoError(t, err, "uuid.Parse error")

		// Act
		id := FileIDFromUUID(u)

		// Assert
		assert.False(t, id.IsZero())
		assert.Equal(t, strID, id.String())
	})

	t.Run("round trip with NewFileID", func(t *testing.T) {
		t.Parallel()

		// Arrange
		original, err := NewFileID()
		require.NoError(t, err, "NewFileID() error")

		u, err := uuid.Parse(original.String())
		require.NoError(t, err, "uuid.Parse error")

		// Act
		id := FileIDFromUUID(u)

		// Assert
		assert.True(t, original.Equal(id), "FileIDFromUUID did not preserve value")
		assert.Equal(t, original.String(), id.String())
	})

	t.Run("uppercase uuid is normalized", func(t *testing.T) {
		t.Parallel()

		// Arrange
		u, err := uuid.Parse("01A18818-C65B-732B-9E0A-E8603949ED3D")
		require.NoError(t, err, "uuid.Parse error")

		// Act
		id := FileIDFromUUID(u)

		// Assert
		assert.Equal(t, "01a18818-c65b-732b-9e0a-e8603949ed3d", id.String())
	})

	t.Run("zero uuid produces zero FileID", func(t *testing.T) {
		t.Parallel()

		// Arrange
		var u uuid.UUID // nil / zero

		// Act
		id := FileIDFromUUID(u)

		// Assert
		assert.True(t, id.IsZero(), "expected zero FileID for zero uuid")
	})

	t.Run("does not validate version", func(t *testing.T) {
		t.Parallel()

		// Arrange
		const strID = "a8e0c515-78dd-4508-9f96-68f7a52a8a6a"
		u, err := uuid.Parse(strID)
		require.NoError(t, err, "uuid.Parse error")

		// Act
		id := FileIDFromUUID(u)

		// Assert
		assert.False(t, id.IsZero())
		assert.Equal(t, strID, id.String())
	})
}

func TestFileID_MarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("non-zero returns error", func(t *testing.T) {
		t.Parallel()

		// Arrange
		id, err := NewFileID()
		require.NoError(t, err, "NewFileID() error")

		// Act
		data, err := id.MarshalJSON()

		// Assert
		require.Error(t, err, "expected error, got nil")
		assert.Nil(t, data, "expected nil payload on error")
		assert.Contains(t, err.Error(), "serialize via DTO")
	})

	t.Run("zero returns error", func(t *testing.T) {
		t.Parallel()

		// Arrange
		var id FileID

		// Act
		data, err := id.MarshalJSON()

		// Assert
		require.Error(t, err, "expected error, got nil")
		assert.Nil(t, data, "expected nil payload on error")
	})

	t.Run("through encoding/json", func(t *testing.T) {
		t.Parallel()

		// Arrange
		id, err := NewFileID()
		require.NoError(t, err, "NewFileID() error")

		// Act
		data, err := json.Marshal(id)

		// Assert
		require.Error(t, err, "expected error, got nil")
		assert.Empty(t, data, "expected empty output on error")
		assert.Contains(t, err.Error(), "serialize via DTO")
	})

	t.Run("through encoding/json as struct field", func(t *testing.T) {
		t.Parallel()

		// Arrange
		type payload struct {
			ID FileID `json:"id"`
		}
		id, err := NewFileID()
		require.NoError(t, err, "NewFileID() error")

		// Act
		data, err := json.Marshal(payload{ID: id})

		// Assert
		require.Error(t, err, "expected error, got nil")
		assert.Empty(t, data, "expected empty output on error")
		assert.Contains(t, err.Error(), "serialize via DTO")
	})
}

func TestFileID_UUID(t *testing.T) {
	t.Parallel()

	t.Run("round trip", func(t *testing.T) {
		t.Parallel()

		const strID = "01a18818-c65b-732b-9e0a-e8603949ed3d"
		id, err := ParseFileID(strID)
		require.NoError(t, err, "ParseFileID error")

		u := id.UUID()

		assert.Equal(t, strID, u.String())
		assert.True(t, id.Equal(FileIDFromUUID(u)), "UUID() did not preserve value")
	})

	t.Run("zero FileID gives nil uuid", func(t *testing.T) {
		t.Parallel()

		var id FileID

		assert.Equal(t, uuid.Nil, id.UUID())
	})
}
