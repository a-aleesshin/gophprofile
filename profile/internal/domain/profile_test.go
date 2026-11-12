package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testEmail(t *testing.T, raw string) Email {
	t.Helper()

	email, err := NewEmail(raw)
	require.NoError(t, err)

	return email
}

func TestNewProfileStartsWithoutEmailOrAvatar(t *testing.T) {
	t.Parallel()

	// Act
	profile, err := NewProfile(testUserID(t), testNow)

	// Assert
	require.NoError(t, err)
	assert.False(t, profile.HasEmail())
	assert.True(t, profile.CurrentAvatarID().IsZero())
	assert.Equal(t, testNow, profile.CreatedAt())
}

func TestSetEmailKeepsTheHashInStep(t *testing.T) {
	t.Parallel()

	// Arrange
	profile, err := NewProfile(testUserID(t), testNow)
	require.NoError(t, err)

	email := testEmail(t, "User@Example.com")

	// Act
	require.NoError(t, profile.SetEmail(email, testNow.Add(time.Minute)))

	// Assert
	assert.Equal(t, "user@example.com", profile.Email().String())
	assert.Equal(t, email.Hash().String(), profile.EmailHash().String(),
		"the hash is stored, not computed in a query, so it must never drift")
	assert.Equal(t, testNow.Add(time.Minute), profile.UpdatedAt())
}

func TestCurrentAvatarPointer(t *testing.T) {
	t.Parallel()

	// Arrange
	profile, err := NewProfile(testUserID(t), testNow)
	require.NoError(t, err)

	id, err := NewAvatarID()
	require.NoError(t, err)

	// Act
	require.NoError(t, profile.SetCurrentAvatar(id, testNow))

	assert.True(t, profile.CurrentAvatarID().Equal(id))

	require.NoError(t, profile.ClearCurrentAvatar(testNow))

	// Assert
	assert.True(t, profile.CurrentAvatarID().IsZero(),
		"deleting the current avatar leaves the profile without one, not with a dangling id")
}

func TestRestoreProfileCatchesADriftedHash(t *testing.T) {
	t.Parallel()

	email := testEmail(t, "user@example.com")
	other := testEmail(t, "someone@example.com")

	tests := []struct {
		name   string
		mutate func(*RestoreProfileParams)
	}{
		{
			name: "a hash that belongs to another email",
			mutate: func(p *RestoreProfileParams) {
				p.Email = email
				p.EmailHash = other.Hash()
			},
		},
		{
			name:   "an email without its hash",
			mutate: func(p *RestoreProfileParams) { p.Email = email },
		},
		{
			name:   "a hash without its email",
			mutate: func(p *RestoreProfileParams) { p.EmailHash = email.Hash() },
		},
		{
			name:   "no user",
			mutate: func(p *RestoreProfileParams) { p.UserID = UserID{} },
		},
		{
			name:   "no version",
			mutate: func(p *RestoreProfileParams) { p.Version = 0 },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			params := RestoreProfileParams{
				UserID: testUserID(t), Version: 1, CreatedAt: testNow, UpdatedAt: testNow,
			}
			test.mutate(&params)

			// Act
			_, err := RestoreProfile(params)

			// Assert
			require.ErrorIs(t, err, ErrInvalidProfile)
		})
	}
}

func TestRestoreProfileAcceptsAMatchingPair(t *testing.T) {
	t.Parallel()

	// Arrange
	email := testEmail(t, "user@example.com")

	// Act
	profile, err := RestoreProfile(RestoreProfileParams{
		UserID:    testUserID(t),
		Version:   3,
		Email:     email,
		EmailHash: email.Hash(),
		CreatedAt: testNow,
		UpdatedAt: testNow,
	})

	// Assert
	require.NoError(t, err)
	assert.True(t, profile.HasEmail())
	assert.Equal(t, int64(3), profile.Version(),
		"the version comes from the row, it is not reset on reconstitution")
}
