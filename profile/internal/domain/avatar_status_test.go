package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAvatarStatusMovesOnlyForward(t *testing.T) {
	t.Parallel()

	every := []AvatarStatus{
		AvatarStatusPending,
		AvatarStatusProcessing,
		AvatarStatusReady,
		AvatarStatusFailed,
		AvatarStatusDeleted,
	}

	allowed := map[string]map[string]bool{
		"pending":    {"processing": true, "failed": true, "deleted": true},
		"processing": {"ready": true, "failed": true, "deleted": true},
		"ready":      {"deleted": true},
		"failed":     {"deleted": true},
		"deleted":    {},
	}

	for _, from := range every {
		for _, to := range every {
			// Act
			got := from.CanBecome(to)

			// Assert
			assert.Equal(t, allowed[from.String()][to.String()], got,
				"%s -> %s", from.String(), to.String())
		}
	}
}

func TestAvatarStatusNeverGoesBackToProcessing(t *testing.T) {
	t.Parallel()

	// Assert
	assert.False(t, AvatarStatusReady.CanBecome(AvatarStatusProcessing),
		"regenerating variants would change the bytes behind an immutable url")
	assert.False(t, AvatarStatusFailed.CanBecome(AvatarStatusProcessing))
	assert.False(t, AvatarStatusDeleted.CanBecome(AvatarStatusReady))
}

func TestAvatarStatusDeletedIsReachableFromAnywhere(t *testing.T) {
	t.Parallel()

	for _, from := range []AvatarStatus{
		AvatarStatusPending, AvatarStatusProcessing, AvatarStatusReady, AvatarStatusFailed,
	} {
		// Assert
		assert.True(t, from.CanBecome(AvatarStatusDeleted),
			"a user may delete an avatar while it is still being processed")
	}
}

func TestParseAvatarStatus(t *testing.T) {
	t.Parallel()

	t.Run("accepts every status the database allows", func(t *testing.T) {
		t.Parallel()

		for _, raw := range []string{"pending", "processing", "ready", "failed", "deleted"} {
			status, err := ParseAvatarStatus(raw)

			require.NoError(t, err, raw)
			assert.Equal(t, raw, status.String())
			assert.True(t, status.IsValid())
		}
	})

	for _, raw := range []string{"", "Ready", "deleting", "rejected", " ready"} {
		t.Run("rejects "+raw, func(t *testing.T) {
			t.Parallel()

			status, err := ParseAvatarStatus(raw)

			require.ErrorIs(t, err, ErrInvalidAvatarStatus)
			assert.True(t, status.IsZero())
		})
	}
}

func TestAvatarStatusAnswersAboutItself(t *testing.T) {
	t.Parallel()

	// Assert
	assert.True(t, AvatarStatusReady.IsServable())
	assert.False(t, AvatarStatusProcessing.IsServable())
	assert.True(t, AvatarStatusDeleted.IsTerminal())
	assert.False(t, AvatarStatusFailed.IsTerminal(),
		"a failed avatar can still be deleted, so it is not terminal")
}

func TestAvatarStatusRefusesToMarshal(t *testing.T) {
	t.Parallel()

	_, err := AvatarStatusReady.MarshalJSON()
	require.Error(t, err)
}
