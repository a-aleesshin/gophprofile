package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAvatarRoundTripsThroughTheDatabaseShape(t *testing.T) {
	t.Parallel()

	// Arrange
	id, err := NewAvatarID()
	require.NoError(t, err)

	user := testUserID(t)
	upload := testUploadRef(t)
	file := testFileRef(t)

	// Act
	restored, err := RestoreAvatar(RestoreAvatarParams{
		ID:           AvatarIDFromUUID(id.UUID()),
		Version:      7,
		UserID:       user,
		UploadID:     UploadRefFromUUID(upload.UUID()),
		FileID:       FileRefFromUUID(file.UUID()),
		Status:       AvatarStatusFailed,
		Snapshot:     testSnapshot(t),
		Variants:     testVariants(t),
		FailedReason: "decode_failed",
		CreatedAt:    testNow,
		UpdatedAt:    testNow.Add(time.Minute),
		DeletedAt:    time.Time{},
	})
	require.NoError(t, err)

	// Assert
	assert.True(t, restored.ID().Equal(id))
	assert.Equal(t, int64(7), restored.Version())
	assert.True(t, restored.UserID().Equal(user))
	assert.Equal(t, upload.String(), restored.UploadID().String())
	assert.Equal(t, file.String(), restored.FileID().String())
	assert.Equal(t, "decode_failed", restored.FailedReason())
	assert.Equal(t, testNow, restored.CreatedAt())
	assert.Equal(t, testNow.Add(time.Minute), restored.UpdatedAt())
	assert.False(t, restored.IsDeleted())

	assert.Equal(t, int32(100), restored.Variants()[0].Width())
	assert.Equal(t, int32(100), restored.Variants()[0].Height())
	assert.Equal(t, "avatar.jpg", restored.Snapshot().FileName())
	assert.Equal(t, int64(2048), restored.Snapshot().SizeBytes())
	assert.Equal(t, int32(1080), restored.Snapshot().Height())

	require.NoError(t, restored.SetVersion(8))
	assert.Equal(t, int64(8), restored.Version())
	require.Error(t, restored.SetVersion(0))
}

func TestRestoreUserIDRefusesWhatNewUserIDWouldHaveTrimmed(t *testing.T) {
	t.Parallel()

	// Act
	trimmed, err := RestoreUserID("user-1")
	require.NoError(t, err)

	_, emptyErr := RestoreUserID("")
	_, controlErr := RestoreUserID("user\n1")

	// Assert
	assert.Equal(t, "user-1", trimmed.String())
	require.ErrorIs(t, emptyErr, ErrInvalidUserID)
	require.ErrorIs(t, controlErr, ErrInvalidUserID)
}

func TestRestoreEmailHashRefusesUppercaseFromTheRow(t *testing.T) {
	t.Parallel()

	// Arrange
	email := testEmail(t, "user@example.com")
	canonical := email.Hash().String()

	// Act
	ok, err := RestoreEmailHash(canonical)
	require.NoError(t, err)

	_, upperErr := RestoreEmailHash("ABCDEF" + canonical[6:])

	// Assert
	assert.True(t, ok.Equal(email.Hash()))
	require.ErrorIs(t, upperErr, ErrInvalidEmailHash,
		"the column is lowercase hex, an uppercase row means something wrote it wrong")
}

func TestNothingInTheDomainSerialisesItself(t *testing.T) {
	t.Parallel()

	avatar := testPendingAvatar(t)

	profile, err := NewProfile(testUserID(t), testNow)
	require.NoError(t, err)

	email := testEmail(t, "user@example.com")

	id, err := NewAvatarID()
	require.NoError(t, err)

	values := map[string]json.Marshaler{
		"avatar":     &avatar,
		"profile":    &profile,
		"avatar id":  id,
		"user id":    testUserID(t),
		"email":      email,
		"email hash": email.Hash(),
		"file ref":   testFileRef(t),
		"upload ref": testUploadRef(t),
		"status":     AvatarStatusReady,
		"variant":    testVariants(t)[0],
		"snapshot":   testSnapshot(t),
	}

	for name, value := range values {
		// Act
		_, err := value.MarshalJSON()

		// Assert
		require.Error(t, err, "%s must not serialise itself, that is what a dto is for", name)
	}
}

func TestZeroValuesAnswerIsZero(t *testing.T) {
	t.Parallel()

	// Assert
	assert.True(t, AvatarID{}.IsZero())
	assert.True(t, UserID{}.IsZero())
	assert.True(t, FileRef{}.IsZero())
	assert.True(t, UploadRef{}.IsZero())
	assert.True(t, Email{}.IsZero())
	assert.True(t, EmailHash{}.IsZero())
	assert.True(t, AvatarStatus{}.IsZero())
	assert.True(t, Variant{}.IsZero())
	assert.True(t, FileSnapshot{}.IsZero())

	assert.Equal(t, uuid.Nil, AvatarID{}.UUID())
	assert.Equal(t, uuid.Nil, FileRef{}.UUID())
	assert.Equal(t, uuid.Nil, UploadRef{}.UUID())
}
