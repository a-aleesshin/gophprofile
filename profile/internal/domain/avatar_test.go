package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testNow = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func testUserID(t *testing.T) UserID {
	t.Helper()

	value, err := NewUserID("user-1")
	require.NoError(t, err)

	return value
}

func testUploadRef(t *testing.T) UploadRef {
	t.Helper()

	value, err := ParseUploadRef(uuid.Must(uuid.NewV7()).String())
	require.NoError(t, err)

	return value
}

func testFileRef(t *testing.T) FileRef {
	t.Helper()

	value, err := ParseFileRef(uuid.Must(uuid.NewV7()).String())
	require.NoError(t, err)

	return value
}

func testSnapshot(t *testing.T) FileSnapshot {
	t.Helper()

	value, err := NewFileSnapshot(NewFileSnapshotParams{
		FileName:  "avatar.jpg",
		MimeType:  "image/jpeg",
		SizeBytes: 2048,
		Width:     1920,
		Height:    1080,
	})
	require.NoError(t, err)

	return value
}

func testVariants(t *testing.T) []Variant {
	t.Helper()

	small, err := NewVariant(NewVariantParams{
		Name: "100x100", Ext: "jpg", MimeType: "image/jpeg", Width: 100, Height: 100,
	})
	require.NoError(t, err)

	large, err := NewVariant(NewVariantParams{
		Name: "300x300", Ext: "jpg", MimeType: "image/jpeg", Width: 300, Height: 300,
	})
	require.NoError(t, err)

	return []Variant{small, large}
}

func testPendingAvatar(t *testing.T) Avatar {
	t.Helper()

	id, err := NewAvatarID()
	require.NoError(t, err)

	avatar, err := NewAvatar(NewAvatarParams{
		ID:       id,
		UserID:   testUserID(t),
		UploadID: testUploadRef(t),
		Now:      testNow,
	})
	require.NoError(t, err)

	return avatar
}

func TestNewAvatarStartsPending(t *testing.T) {
	t.Parallel()

	// Act
	avatar := testPendingAvatar(t)

	// Assert
	assert.True(t, avatar.Status().Equal(AvatarStatusPending))
	assert.Equal(t, int64(1), avatar.Version())
	assert.False(t, avatar.IsDeleted())
	assert.True(t, avatar.FileID().IsZero(),
		"the row exists before the upload, so there is no file yet")
	assert.Equal(t, testNow, avatar.CreatedAt())
}

func TestNewAvatarRequiresEveryField(t *testing.T) {
	t.Parallel()

	id, err := NewAvatarID()
	require.NoError(t, err)

	full := NewAvatarParams{
		ID: id, UserID: testUserID(t), UploadID: testUploadRef(t), Now: testNow,
	}

	tests := []struct {
		name   string
		mutate func(*NewAvatarParams)
	}{
		{name: "no id", mutate: func(p *NewAvatarParams) { p.ID = AvatarID{} }},
		{name: "no user", mutate: func(p *NewAvatarParams) { p.UserID = UserID{} }},
		{name: "no upload", mutate: func(p *NewAvatarParams) { p.UploadID = UploadRef{} }},
		{name: "no time", mutate: func(p *NewAvatarParams) { p.Now = time.Time{} }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			params := full
			test.mutate(&params)

			// Act
			_, err := NewAvatar(params)

			// Assert
			require.ErrorIs(t, err, ErrInvalidAvatar)
		})
	}
}

func TestAvatarWalksTheHappyPath(t *testing.T) {
	t.Parallel()

	// Arrange
	avatar := testPendingAvatar(t)
	fileID := testFileRef(t)

	later := testNow.Add(time.Minute)

	// Act
	require.NoError(t, avatar.StartProcessing(fileID, later))
	require.NoError(t, avatar.MarkReady(testSnapshot(t), testVariants(t), later.Add(time.Minute)))

	// Assert
	assert.True(t, avatar.Status().Equal(AvatarStatusReady))
	assert.True(t, avatar.FileID().Equal(fileID))
	assert.Equal(t, "image/jpeg", avatar.Snapshot().MimeType())
	assert.Len(t, avatar.Variants(), 2)
	assert.Equal(t, later.Add(time.Minute), avatar.UpdatedAt())

	variant, found := avatar.VariantByName("300x300")
	require.True(t, found)
	assert.Equal(t, "300x300.jpg", variant.Path())
}

func TestAvatarRefusesReadyWithoutSubstance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		snapshot func(*testing.T) FileSnapshot
		variants func(*testing.T) []Variant
	}{
		{
			name:     "no snapshot",
			snapshot: func(*testing.T) FileSnapshot { return FileSnapshot{} },
			variants: testVariants,
		},
		{
			name:     "no variants",
			snapshot: testSnapshot,
			variants: func(*testing.T) []Variant { return nil },
		},
		{
			name:     "an empty variant",
			snapshot: testSnapshot,
			variants: func(*testing.T) []Variant { return []Variant{{}} },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			avatar := testPendingAvatar(t)
			require.NoError(t, avatar.StartProcessing(testFileRef(t), testNow))

			// Act
			err := avatar.MarkReady(test.snapshot(t), test.variants(t), testNow)

			// Assert
			require.ErrorIs(t, err, ErrInvalidAvatar)
			assert.True(t, avatar.Status().Equal(AvatarStatusProcessing),
				"a refused transition must leave the aggregate untouched")
		})
	}
}

func TestAvatarRefusesToGoBackwards(t *testing.T) {
	t.Parallel()

	// Arrange
	avatar := testPendingAvatar(t)
	require.NoError(t, avatar.StartProcessing(testFileRef(t), testNow))
	require.NoError(t, avatar.MarkReady(testSnapshot(t), testVariants(t), testNow))

	// Act
	err := avatar.StartProcessing(testFileRef(t), testNow)

	// Assert
	require.ErrorIs(t, err, ErrForbiddenAvatarTransition)
	assert.True(t, avatar.Status().Equal(AvatarStatusReady))
}

func TestAvatarDeletedAfterTheWorkerLostTheRace(t *testing.T) {
	t.Parallel()

	// Arrange
	avatar := testPendingAvatar(t)
	require.NoError(t, avatar.StartProcessing(testFileRef(t), testNow))
	require.NoError(t, avatar.MarkDeleted(testNow.Add(time.Second)))

	// Act
	err := avatar.MarkReady(testSnapshot(t), testVariants(t), testNow.Add(time.Minute))

	// Assert
	require.ErrorIs(t, err, ErrForbiddenAvatarTransition,
		"the table of transitions settles the race, the worker must read this as deleted")
	assert.True(t, avatar.IsDeleted())
	assert.Equal(t, testNow.Add(time.Second), avatar.DeletedAt())
}

func TestAvatarFailureCarriesAReason(t *testing.T) {
	t.Parallel()

	// Arrange
	avatar := testPendingAvatar(t)
	require.NoError(t, avatar.StartProcessing(testFileRef(t), testNow))

	// Act
	withoutReason := avatar.MarkFailed("", testNow)
	withReason := avatar.MarkFailed("decode_failed", testNow)

	// Assert
	require.ErrorIs(t, withoutReason, ErrInvalidAvatar,
		"the reason is shown to the user, so an empty one is useless")
	require.NoError(t, withReason)
	assert.Equal(t, "decode_failed", avatar.FailedReason())
}

func TestAvatarOwnership(t *testing.T) {
	t.Parallel()

	// Arrange
	avatar := testPendingAvatar(t)

	other, err := NewUserID("user-2")
	require.NoError(t, err)

	// Assert
	assert.True(t, avatar.OwnedBy(testUserID(t)))
	assert.False(t, avatar.OwnedBy(other))
}

func TestRestoreAvatarChecksWhatTheDatabaseCannot(t *testing.T) {
	t.Parallel()

	id, err := NewAvatarID()
	require.NoError(t, err)

	full := RestoreAvatarParams{
		ID:        id,
		Version:   3,
		UserID:    testUserID(t),
		Status:    AvatarStatusReady,
		CreatedAt: testNow,
		UpdatedAt: testNow,
	}

	t.Run("accepts a complete row", func(t *testing.T) {
		t.Parallel()

		avatar, err := RestoreAvatar(full)

		require.NoError(t, err)
		assert.Equal(t, int64(3), avatar.Version())
	})

	tests := []struct {
		name   string
		mutate func(*RestoreAvatarParams)
	}{
		{name: "no id", mutate: func(p *RestoreAvatarParams) { p.ID = AvatarID{} }},
		{name: "zero version", mutate: func(p *RestoreAvatarParams) { p.Version = 0 }},
		{name: "no user", mutate: func(p *RestoreAvatarParams) { p.UserID = UserID{} }},
		{
			name:   "unknown status",
			mutate: func(p *RestoreAvatarParams) { p.Status = AvatarStatus{value: "deleting"} },
		},
		{
			name: "too many variants",
			mutate: func(p *RestoreAvatarParams) {
				p.Variants = make([]Variant, maxVariantsPerAvatar+1)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			params := full
			test.mutate(&params)

			// Act
			_, err := RestoreAvatar(params)

			// Assert
			require.ErrorIs(t, err, ErrInvalidAvatar)
		})
	}
}
