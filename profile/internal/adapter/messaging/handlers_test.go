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

	"github.com/a-aleesshin/gophprofile/internal/inbox"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

const testFileID = "0193abcd-ef01-7a23-8b45-0123456789ab"

type stubProcessed struct {
	fact  usecase.FileProcessedFact
	err   error
	calls int
}

func (s *stubProcessed) Handle(
	_ context.Context,
	fact usecase.FileProcessedFact,
) (usecase.ApplyFactResult, error) {
	s.fact = fact
	s.calls++

	return usecase.ApplyFactResult{}, s.err
}

type stubRejected struct {
	fact  usecase.FileRejectedFact
	err   error
	calls int
}

func (s *stubRejected) Handle(
	_ context.Context,
	fact usecase.FileRejectedFact,
) (usecase.ApplyFactResult, error) {
	s.fact = fact
	s.calls++

	return usecase.ApplyFactResult{}, s.err
}

type stubDeleted struct {
	fact  usecase.FileDeletedFact
	err   error
	calls int
}

func (s *stubDeleted) Handle(
	_ context.Context,
	fact usecase.FileDeletedFact,
) (usecase.ApplyFactResult, error) {
	s.fact = fact
	s.calls++

	return usecase.ApplyFactResult{}, s.err
}

func envelopeOf(t *testing.T, messageType string, data any) outbox.Envelope {
	t.Helper()

	payload, err := json.Marshal(data)
	require.NoError(t, err)

	return outbox.Envelope{
		MessageID:  uuid.New(),
		Type:       messageType,
		OccurredAt: time.Now().UTC(),
		Data:       payload,
	}
}

func run(
	t *testing.T, handlers map[string]inbox.Handler, messageType string, data any,
) error {
	t.Helper()

	handler, known := handlers[messageType]
	require.True(t, known, "no handler for %s", messageType)

	return handler.Run(context.Background(), envelopeOf(t, messageType, data))
}

// The payload is written the way file writes it, by hand, so a rename on either
// side shows up here instead of silently dropping a field.
func readyPayload() map[string]any {
	return map[string]any{
		"file_id":       testFileID,
		"owner_ref":     "user-1",
		"original_name": "avatar.jpg",
		"mime_type":     "image/jpeg",
		"size_bytes":    2048,
		"width":         1920,
		"height":        1080,
		"variants": []map[string]any{
			{
				"name": "original", "ext": "jpg", "mime_type": "image/jpeg",
				"width": 1920, "height": 1080, "size_bytes": 2048, "etag": "abc",
			},
			{
				"name": "100x100", "ext": "jpg", "mime_type": "image/jpeg",
				"width": 100, "height": 100, "size_bytes": 512, "etag": "def",
			},
			{
				"name": "300x300", "ext": "jpg", "mime_type": "image/jpeg",
				"width": 300, "height": 300, "size_bytes": 1024, "etag": "ghi",
			},
		},
	}
}

func TestFileProcessedBecomesASnapshotAndVariants(t *testing.T) {
	t.Parallel()

	// Arrange
	processed := &stubProcessed{}
	handlers := FactHandlers(processed, &stubRejected{}, &stubDeleted{})

	// Act
	err := run(t, handlers, TypeFileProcessed, readyPayload())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, 1, processed.calls)

	assert.Equal(t, testFileID, processed.fact.FileID.String())
	assert.Equal(t, "avatar.jpg", processed.fact.Snapshot.FileName(),
		"the original name lives in file, and the fact is the only way it gets here")
	assert.Equal(t, "image/jpeg", processed.fact.Snapshot.MimeType())
	assert.Equal(t, int64(2048), processed.fact.Snapshot.SizeBytes())
	assert.Equal(t, int32(1920), processed.fact.Snapshot.Width())

	require.Len(t, processed.fact.Variants, 2)
	assert.Equal(t, "100x100", processed.fact.Variants[0].Name())
	assert.Equal(t, "300x300", processed.fact.Variants[1].Name())
}

func TestFileProcessedDropsTheOriginal(t *testing.T) {
	t.Parallel()

	// Arrange
	processed := &stubProcessed{}
	handlers := FactHandlers(processed, &stubRejected{}, &stubDeleted{})

	// Act
	require.NoError(t, run(t, handlers, TypeFileProcessed, readyPayload()))

	// Assert
	for _, variant := range processed.fact.Variants {
		assert.NotEqual(t, "original", variant.Name(),
			"the original keeps its exif and needs a signed link, so profile must "+
				"never hand out an unsigned url for it")
	}
}

func TestFileProcessedWithNothingServableIsDropped(t *testing.T) {
	t.Parallel()

	// Arrange
	payload := readyPayload()
	payload["variants"] = []map[string]any{
		{
			"name": "original", "ext": "jpg", "mime_type": "image/jpeg",
			"width": 1920, "height": 1080,
		},
	}

	processed := &stubProcessed{}

	// Act
	err := run(t, FactHandlers(processed, &stubRejected{}, &stubDeleted{}),
		TypeFileProcessed, payload)

	// Assert
	require.ErrorIs(t, err, inbox.ErrUnprocessable,
		"retrying cannot make a variant appear, so this belongs in the dead letter queue")
	assert.Zero(t, processed.calls)
}

func TestFileRejectedCarriesTheReason(t *testing.T) {
	t.Parallel()

	// Arrange
	rejected := &stubRejected{}

	// Act
	err := run(t, FactHandlers(&stubProcessed{}, rejected, &stubDeleted{}),
		TypeFileRejected, map[string]any{
			"file_id": testFileID, "owner_ref": "user-1", "reason": "decode_failed",
		})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, testFileID, rejected.fact.FileID.String())
	assert.Equal(t, "decode_failed", rejected.fact.Reason)
}

func TestFileDeletedCarriesTheFile(t *testing.T) {
	t.Parallel()

	// Arrange
	deleted := &stubDeleted{}

	// Act
	err := run(t, FactHandlers(&stubProcessed{}, &stubRejected{}, deleted),
		TypeFileDeleted, map[string]any{"file_id": testFileID, "owner_ref": "user-1"})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, testFileID, deleted.fact.FileID.String())
}

func TestAFactWeCannotReadIsDropped(t *testing.T) {
	t.Parallel()

	handlers := FactHandlers(&stubProcessed{}, &stubRejected{}, &stubDeleted{})

	tests := []struct {
		name        string
		messageType string
		payload     any
	}{
		{
			name:        "processed without a file id",
			messageType: TypeFileProcessed,
			payload:     map[string]any{"mime_type": "image/jpeg", "size_bytes": 1},
		},
		{
			name:        "processed with a file id that is not a uuid",
			messageType: TypeFileProcessed,
			payload:     map[string]any{"file_id": "nope"},
		},
		{
			name:        "processed without a mime type",
			messageType: TypeFileProcessed,
			payload:     map[string]any{"file_id": testFileID, "size_bytes": 1},
		},
		{
			name:        "processed with a variant that has no extension",
			messageType: TypeFileProcessed,
			payload: map[string]any{
				"file_id": testFileID, "mime_type": "image/jpeg", "size_bytes": 1,
				"variants": []map[string]any{
					{"name": "100x100", "mime_type": "image/jpeg", "width": 100, "height": 100},
				},
			},
		},
		{
			name:        "rejected without a reason",
			messageType: TypeFileRejected,
			payload:     map[string]any{"file_id": testFileID},
		},
		{
			name:        "rejected with a file id that is not a uuid",
			messageType: TypeFileRejected,
			payload:     map[string]any{"file_id": "nope", "reason": "decode_failed"},
		},
		{
			name:        "deleted with a file id that is not a uuid",
			messageType: TypeFileDeleted,
			payload:     map[string]any{"file_id": "nope"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			require.ErrorIs(t, run(t, handlers, test.messageType, test.payload),
				inbox.ErrUnprocessable)
		})
	}
}

func TestMalformedJSONIsDropped(t *testing.T) {
	t.Parallel()

	handlers := FactHandlers(&stubProcessed{}, &stubRejected{}, &stubDeleted{})

	for messageType := range handlers {
		t.Run(messageType, func(t *testing.T) {
			t.Parallel()

			err := handlers[messageType].Run(context.Background(), outbox.Envelope{
				MessageID: uuid.New(),
				Type:      messageType,
				Data:      json.RawMessage(`{"file_id": [1, 2`),
			})

			require.ErrorIs(t, err, inbox.ErrUnprocessable)
		})
	}
}

func TestAnInfrastructureFailureIsRetried(t *testing.T) {
	t.Parallel()

	// Arrange
	broken := errors.New("connection reset by peer")
	processed := &stubProcessed{err: broken}

	// Act
	err := run(t, FactHandlers(processed, &stubRejected{}, &stubDeleted{}),
		TypeFileProcessed, readyPayload())

	// Assert
	require.ErrorIs(t, err, broken)
	assert.NotErrorIs(t, err, inbox.ErrUnprocessable,
		"a database that is down will be up again, and the fact must survive until then")
}

func TestALostRaceIsRetried(t *testing.T) {
	t.Parallel()

	// Arrange
	processed := &stubProcessed{err: usecase.ErrStaleVersion}

	// Act
	err := run(t, FactHandlers(processed, &stubRejected{}, &stubDeleted{}),
		TypeFileProcessed, readyPayload())

	// Assert
	require.ErrorIs(t, err, usecase.ErrStaleVersion)
	assert.NotErrorIs(t, err, inbox.ErrUnprocessable,
		"a version conflict is momentary, and dropping the fact would leave the avatar processing")
}

func TestAPermanentFailureOfTheUseCaseIsDropped(t *testing.T) {
	t.Parallel()

	permanent := []error{
		usecase.ErrInvalidCommand,
		domain.ErrInvalidAvatar,
		domain.ErrInvalidSnapshot,
		domain.ErrInvalidVariant,
		domain.ErrInvalidProfile,
	}

	for _, sentinel := range permanent {
		t.Run(sentinel.Error(), func(t *testing.T) {
			t.Parallel()

			err := run(t, FactHandlers(&stubProcessed{err: sentinel}, &stubRejected{},
				&stubDeleted{}), TypeFileProcessed, readyPayload())

			require.ErrorIs(t, err, inbox.ErrUnprocessable,
				"no retry turns a malformed fact into a valid one")
		})
	}
}

func TestEveryFactTypeHasAHandler(t *testing.T) {
	t.Parallel()

	handlers := FactHandlers(&stubProcessed{}, &stubRejected{}, &stubDeleted{})

	require.Len(t, handlers, 3)

	for _, messageType := range []string{TypeFileProcessed, TypeFileRejected, TypeFileDeleted} {
		_, known := handlers[messageType]
		assert.True(t, known, "%s would go straight to the dead letter queue", messageType)
	}
}
