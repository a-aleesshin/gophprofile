package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
	"github.com/a-aleesshin/gophprofile/internal/inbox"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
)

const (
	testFactFileID   = "0193abcd-ef01-7a23-8b45-0123456789ab"
	testFactAvatarID = "0193abcd-ef01-7a23-8b45-0123456789ac"
)

type stubFileDeleter struct {
	cmd    usecase.DeleteFileCommand
	result usecase.DeleteFileResult
	err    error
	calls  int
}

func (s *stubFileDeleter) Handle(
	_ context.Context,
	cmd usecase.DeleteFileCommand,
) (usecase.DeleteFileResult, error) {
	s.cmd = cmd
	s.calls++

	return s.result, s.err
}

func runFact(t *testing.T, deleter fileDeleter, data any) error {
	t.Helper()

	payload, err := json.Marshal(data)
	require.NoError(t, err)

	handler, known := FactHandlers(deleter)[TypeAvatarDeleted]
	require.True(t, known)

	return handler.Run(context.Background(), outbox.Envelope{
		MessageID:  uuid.New(),
		Type:       TypeAvatarDeleted,
		OccurredAt: time.Now().UTC(),
		Data:       payload,
	})
}

// Written the way profile writes it, by hand, so a rename on either side shows
// up here instead of quietly dropping a field.
func deletedPayload() map[string]any {
	return map[string]any{
		"avatar_id": testFactAvatarID,
		"user_id":   "user-1",
		"file_id":   testFactFileID,
	}
}

func TestAvatarDeletedDeletesTheFile(t *testing.T) {
	t.Parallel()

	// Arrange
	deleter := &stubFileDeleter{
		result: usecase.DeleteFileResult{Status: domain.FileStatusDeleting},
	}

	// Act
	err := runFact(t, deleter, deletedPayload())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, 1, deleter.calls)
	assert.Equal(t, testFactFileID, deleter.cmd.FileID.String())
	assert.Equal(t, "user-1", deleter.cmd.OwnerRef.String(),
		"the owner travels with the fact and is checked against the file")
}

func TestAvatarDeletedOfAFileWeNeverHadIsSettled(t *testing.T) {
	t.Parallel()

	// Arrange
	deleter := &stubFileDeleter{err: domain.ErrFileNotFound}

	// Act
	err := runFact(t, deleter, deletedPayload())

	// Assert
	require.NoError(t, err,
		"nothing is left to delete, so the fact is done, not retried and not dropped")
}

func TestAvatarDeletedAboutSomebodyElsesFileIsQuarantined(t *testing.T) {
	t.Parallel()

	// Arrange
	deleter := &stubFileDeleter{err: usecase.ErrOwnerMismatch}

	// Act
	err := runFact(t, deleter, deletedPayload())

	// Assert
	require.ErrorIs(t, err, inbox.ErrUnprocessable,
		"a fact pointing at a file of another owner must never be retried into effect")
}

func TestAFactWeCannotReadIsDropped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload any
	}{
		{
			name:    "no file id",
			payload: map[string]any{"avatar_id": testFactAvatarID, "user_id": "user-1"},
		},
		{
			name: "a file id that is not a uuid",
			payload: map[string]any{
				"avatar_id": testFactAvatarID, "user_id": "user-1", "file_id": "nope",
			},
		},
		{
			name: "no user id",
			payload: map[string]any{
				"avatar_id": testFactAvatarID, "file_id": testFactFileID,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			deleter := &stubFileDeleter{}

			require.ErrorIs(t, runFact(t, deleter, test.payload), inbox.ErrUnprocessable)
			assert.Zero(t, deleter.calls, "nothing reaches the use case")
		})
	}
}

func TestMalformedJSONIsDropped(t *testing.T) {
	t.Parallel()

	handler := FactHandlers(&stubFileDeleter{})[TypeAvatarDeleted]

	err := handler.Run(context.Background(), outbox.Envelope{
		MessageID: uuid.New(),
		Type:      TypeAvatarDeleted,
		Data:      json.RawMessage(`{"file_id": [1, 2`),
	})

	require.ErrorIs(t, err, inbox.ErrUnprocessable)
}

func TestAnInfrastructureFailureIsRetried(t *testing.T) {
	t.Parallel()

	// Arrange
	broken := errors.New("connection reset by peer")
	deleter := &stubFileDeleter{err: broken}

	// Act
	err := runFact(t, deleter, deletedPayload())

	// Assert
	require.ErrorIs(t, err, broken)
	assert.NotErrorIs(t, err, inbox.ErrUnprocessable,
		"a database that is down will be up again, and the fact must survive until then")
}

func TestAForbiddenTransitionIsDropped(t *testing.T) {
	t.Parallel()

	deleter := &stubFileDeleter{err: domain.ErrForbiddenFileTransition}

	require.ErrorIs(t, runFact(t, deleter, deletedPayload()), inbox.ErrUnprocessable,
		"a file that cannot move to deleting will never be able to, retries will not help")
}
