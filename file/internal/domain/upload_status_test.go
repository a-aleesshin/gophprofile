package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func allUploadStatuses() []UploadStatus {
	return []UploadStatus{
		UploadStatusPending,
		UploadStatusUploaded,
		UploadStatusExpired,
		UploadStatusAborted,
	}
}

func TestParseUploadStatus(t *testing.T) {
	t.Parallel()

	t.Run("round trip for every status", func(t *testing.T) {
		t.Parallel()

		for _, want := range allUploadStatuses() {
			t.Run(want.String(), func(t *testing.T) {
				t.Parallel()

				got, err := ParseUploadStatus(want.String())
				require.NoError(t, err)
				assert.True(t, got.Equal(want))
				assert.True(t, got.IsValid())
			})
		}
	})

	t.Run("rejected", func(t *testing.T) {
		t.Parallel()

		for _, raw := range []string{"", "PENDING", " pending", "unknown", "ready"} {
			t.Run(raw, func(t *testing.T) {
				t.Parallel()

				got, err := ParseUploadStatus(raw)
				require.ErrorIs(t, err, ErrInvalidUploadStatus)
				assert.True(t, got.IsZero())
			})
		}
	})
}

func TestUploadStatus_ValidityIsCheckedAgainstConstants(t *testing.T) {
	t.Parallel()

	for _, s := range allUploadStatuses() {
		assert.Truef(t, s.IsValid(), "%s must be valid", s)
	}

	var zero UploadStatus
	assert.False(t, zero.IsValid())
	assert.True(t, zero.IsZero())

	handmade := UploadStatus{value: "garbage"}
	assert.False(t, handmade.IsValid())
	assert.False(t, handmade.CanTransitionTo(UploadStatusUploaded))
	assert.False(t, UploadStatusPending.CanTransitionTo(handmade))
}

func TestUploadStatus_CanTransitionTo(t *testing.T) {
	t.Parallel()

	allowed := map[string][]UploadStatus{
		uploadStatusPending:  {UploadStatusUploaded, UploadStatusExpired, UploadStatusAborted},
		uploadStatusUploaded: {},
		uploadStatusExpired:  {},
		uploadStatusAborted:  {},
	}

	for _, from := range allUploadStatuses() {
		t.Run(from.String(), func(t *testing.T) {
			t.Parallel()

			targets, known := allowed[from.String()]
			require.Truef(t, known, "%s is missing from the expected table", from)

			permitted := make(map[string]bool, len(targets))
			for _, to := range targets {
				permitted[to.String()] = true
			}

			for _, to := range allUploadStatuses() {
				if permitted[to.String()] {
					assert.Truef(t, from.CanTransitionTo(to), "%s -> %s must be allowed", from, to)
					continue
				}

				assert.Falsef(t, from.CanTransitionTo(to), "%s -> %s must be forbidden", from, to)
			}
		})
	}
}

func TestUploadStatus_Terminal(t *testing.T) {
	t.Parallel()

	assert.False(t, UploadStatusPending.IsTerminal())

	for _, s := range []UploadStatus{UploadStatusUploaded, UploadStatusExpired, UploadStatusAborted} {
		assert.Truef(t, s.IsTerminal(), "%s must be terminal", s)

		got, err := s.TransitionTo(UploadStatusUploaded)
		require.ErrorIs(t, err, ErrForbiddenUploadTransition)
		assert.True(t, got.Equal(s))
	}
}

func TestUploadStatus_MarshalJSON(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(struct {
		Status UploadStatus `json:"status"`
	}{Status: UploadStatusPending})
	require.Error(t, err)
	assert.Empty(t, data)
}
