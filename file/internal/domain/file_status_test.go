package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func allFileStatuses() []FileStatus {
	return []FileStatus{
		FileStatusReceived,
		FileStatusProcessing,
		FileStatusReady,
		FileStatusRejected,
		FileStatusDeleting,
		FileStatusDeleted,
	}
}

func TestParseFileStatus(t *testing.T) {
	t.Parallel()

	t.Run("round trip for every status", func(t *testing.T) {
		t.Parallel()

		for _, want := range allFileStatuses() {
			t.Run(want.String(), func(t *testing.T) {
				t.Parallel()

				got, err := ParseFileStatus(want.String())
				require.NoError(t, err)
				assert.True(t, got.Equal(want))
				assert.True(t, got.IsValid())
			})
		}
	})

	t.Run("rejected", func(t *testing.T) {
		t.Parallel()

		for _, raw := range []string{"", "READY", " ready", "unknown", "unspecified"} {
			t.Run(raw, func(t *testing.T) {
				t.Parallel()

				got, err := ParseFileStatus(raw)
				require.ErrorIs(t, err, ErrInvalidFileStatus)
				assert.True(t, got.IsZero())
			})
		}
	})
}

func TestFileStatus_ValidityIsCheckedAgainstConstants(t *testing.T) {
	t.Parallel()

	for _, s := range allFileStatuses() {
		assert.Truef(t, s.IsValid(), "%s must be valid", s)
		assert.Falsef(t, s.IsZero(), "%s must not be zero", s)
	}

	var zero FileStatus
	assert.False(t, zero.IsValid())
	assert.True(t, zero.IsZero())
	assert.Empty(t, zero.String())

	handmade := FileStatus{value: "garbage"}
	assert.False(t, handmade.IsValid())
	assert.False(t, handmade.IsZero())
	assert.False(t, handmade.CanTransitionTo(FileStatusDeleting))
	assert.False(t, FileStatusProcessing.CanTransitionTo(handmade))
}

func TestFileStatus_CanTransitionTo(t *testing.T) {
	t.Parallel()

	allowed := map[string][]FileStatus{
		fileStatusReceived:   {FileStatusProcessing, FileStatusDeleting},
		fileStatusProcessing: {FileStatusReady, FileStatusRejected, FileStatusDeleting},
		fileStatusReady:      {FileStatusDeleting},
		fileStatusRejected:   {FileStatusDeleting},
		fileStatusDeleting:   {FileStatusDeleted},
		fileStatusDeleted:    {},
	}

	for _, from := range allFileStatuses() {
		t.Run(from.String(), func(t *testing.T) {
			t.Parallel()

			targets, known := allowed[from.String()]
			require.Truef(t, known, "%s is missing from the expected table", from)

			permitted := make(map[string]bool, len(targets))
			for _, to := range targets {
				permitted[to.String()] = true
			}

			for _, to := range allFileStatuses() {
				if permitted[to.String()] {
					assert.Truef(t, from.CanTransitionTo(to), "%s -> %s must be allowed", from, to)
					continue
				}

				assert.Falsef(t, from.CanTransitionTo(to), "%s -> %s must be forbidden", from, to)
			}

			assert.Falsef(t, from.CanTransitionTo(FileStatus{}),
				"%s -> zero must be forbidden", from)
		})
	}
}

func TestFileStatus_TransitionTo(t *testing.T) {
	t.Parallel()

	t.Run("allowed", func(t *testing.T) {
		t.Parallel()

		got, err := FileStatusProcessing.TransitionTo(FileStatusReady)
		require.NoError(t, err)
		assert.True(t, got.Equal(FileStatusReady))
	})

	t.Run("delete while processing", func(t *testing.T) {
		t.Parallel()

		got, err := FileStatusProcessing.TransitionTo(FileStatusDeleting)
		require.NoError(t, err)
		assert.True(t, got.Equal(FileStatusDeleting))
	})

	t.Run("backwards is forbidden", func(t *testing.T) {
		t.Parallel()

		got, err := FileStatusReady.TransitionTo(FileStatusProcessing)
		require.ErrorIs(t, err, ErrForbiddenFileTransition)
		assert.True(t, got.Equal(FileStatusReady))
	})

	t.Run("worker cannot revive a file being deleted", func(t *testing.T) {
		t.Parallel()

		got, err := FileStatusDeleting.TransitionTo(FileStatusReady)
		require.ErrorIs(t, err, ErrForbiddenFileTransition)
		assert.True(t, got.Equal(FileStatusDeleting))
	})

	t.Run("terminal has no exit", func(t *testing.T) {
		t.Parallel()

		for _, to := range allFileStatuses() {
			got, err := FileStatusDeleted.TransitionTo(to)
			require.ErrorIs(t, err, ErrForbiddenFileTransition)
			assert.True(t, got.Equal(FileStatusDeleted))
		}
	})

	t.Run("invalid target", func(t *testing.T) {
		t.Parallel()

		for _, to := range []FileStatus{{}, {value: "garbage"}} {
			got, err := FileStatusReceived.TransitionTo(to)
			require.ErrorIs(t, err, ErrInvalidFileStatus)
			assert.True(t, got.Equal(FileStatusReceived))
		}
	})
}

func TestFileStatus_Predicates(t *testing.T) {
	t.Parallel()

	assert.True(t, FileStatusReady.IsServable())
	assert.True(t, FileStatusDeleted.IsTerminal())

	for _, s := range []FileStatus{
		FileStatusReceived, FileStatusProcessing, FileStatusRejected,
		FileStatusDeleting, FileStatusDeleted,
	} {
		assert.Falsef(t, s.IsServable(), "%s must not be servable", s)
	}

	for _, s := range []FileStatus{
		FileStatusReceived, FileStatusProcessing, FileStatusReady,
		FileStatusRejected, FileStatusDeleting,
	} {
		assert.Falsef(t, s.IsTerminal(), "%s must not be terminal", s)
	}
}

func TestFileStatus_MarshalJSON(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(struct {
		Status FileStatus `json:"status"`
	}{Status: FileStatusReady})
	require.Error(t, err)
	assert.Empty(t, data)
}
