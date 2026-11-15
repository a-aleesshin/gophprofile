package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func allRejectReasons() []RejectReason {
	return []RejectReason{
		RejectReasonUnsupportedFormat,
		RejectReasonMagicMismatch,
		RejectReasonDecodeFailed,
		RejectReasonTooManyPixels,
		RejectReasonTooSmall,
		RejectReasonSizeMismatch,
		RejectReasonContainerInvalid,
		RejectReasonContainerTooLarge,
		RejectReasonInternalError,
	}
}

func TestParseRejectReason(t *testing.T) {
	t.Parallel()

	t.Run("round trip for every reason", func(t *testing.T) {
		t.Parallel()

		for _, want := range allRejectReasons() {
			t.Run(want.String(), func(t *testing.T) {
				t.Parallel()

				got, err := ParseRejectReason(want.String())
				require.NoError(t, err)
				assert.True(t, got.Equal(want))
				assert.True(t, got.IsValid())
			})
		}
	})

	t.Run("rejected", func(t *testing.T) {
		t.Parallel()

		for _, raw := range []string{"", "DECODE_FAILED", "decode failed", "unknown"} {
			t.Run(raw, func(t *testing.T) {
				t.Parallel()

				got, err := ParseRejectReason(raw)
				require.ErrorIs(t, err, ErrInvalidRejectReason)
				assert.True(t, got.IsZero())
			})
		}
	})
}

func TestRejectReason_ValidityIsCheckedAgainstConstants(t *testing.T) {
	t.Parallel()

	for _, r := range allRejectReasons() {
		assert.Truef(t, r.IsValid(), "%s must be valid", r)
		assert.Falsef(t, r.IsZero(), "%s must not be zero", r)
	}

	var zero RejectReason
	assert.False(t, zero.IsValid())
	assert.True(t, zero.IsZero())
	assert.Empty(t, zero.String())

	handmade := RejectReason{value: "garbage"}
	assert.False(t, handmade.IsValid())
	assert.False(t, handmade.IsZero())
}

func TestRejectReason_NamesAreUnique(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, len(allRejectReasons()))
	for _, r := range allRejectReasons() {
		require.Falsef(t, seen[r.String()], "%q is declared twice", r.String())
		seen[r.String()] = true
	}
}

func TestRejectReason_MarshalJSON(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(struct {
		Reason RejectReason `json:"reason"`
	}{Reason: RejectReasonDecodeFailed})
	require.Error(t, err)
	assert.Empty(t, data)
}
