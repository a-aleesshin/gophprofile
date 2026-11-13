package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
	"github.com/a-aleesshin/gophprofile/internal/inbox"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
)

type spyProcessor struct {
	cmd usecase.ProcessFileCommand
	err error
}

func (s *spyProcessor) Handle(
	_ context.Context,
	cmd usecase.ProcessFileCommand,
) (usecase.ProcessFileResult, error) {
	s.cmd = cmd

	return usecase.ProcessFileResult{}, s.err
}

type spyDeleter struct {
	cmd usecase.DeleteObjectsCommand
	err error
}

func (s *spyDeleter) Handle(
	_ context.Context,
	cmd usecase.DeleteObjectsCommand,
) (usecase.DeleteObjectsResult, error) {
	s.cmd = cmd

	return usecase.DeleteObjectsResult{}, s.err
}

func envelopeFor(t *testing.T, messageType string, data any) outbox.Envelope {
	t.Helper()

	raw, err := json.Marshal(data)
	require.NoError(t, err)

	return outbox.Envelope{Type: messageType, Data: raw}
}

func TestCommandHandlersDecodeProcessFile(t *testing.T) {
	t.Parallel()

	// Arrange
	fileID, err := domain.NewFileID()
	require.NoError(t, err)

	processor := &spyProcessor{}
	handlers := CommandHandlers(processor, &spyDeleter{})

	message := envelopeFor(t, TypeProcessFile, processFileData{
		FileID:     fileID.String(),
		StagingKey: "staging/01930000-0000-7000-8000-000000000000",
		Variants:   []string{"100x100", "300x300"},
	})

	// Act
	err = handlers[TypeProcessFile].Run(t.Context(), message)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, fileID, processor.cmd.FileID)
	assert.Equal(t, "staging/01930000-0000-7000-8000-000000000000",
		processor.cmd.StagingKey.String())
	require.Len(t, processor.cmd.Variants, 2)
	assert.Equal(t, "100x100", processor.cmd.Variants[0].String())
}

func TestCommandHandlersDecodeDeleteObjects(t *testing.T) {
	t.Parallel()

	// Arrange
	fileID, err := domain.NewFileID()
	require.NoError(t, err)

	deleter := &spyDeleter{}
	handlers := CommandHandlers(&spyProcessor{}, deleter)

	message := envelopeFor(t, TypeDeleteObjects, deleteObjectsData{
		FileID:      fileID.String(),
		StorageKeys: []string{"originals/" + fileID.String()},
	})

	// Act
	err = handlers[TypeDeleteObjects].Run(t.Context(), message)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, fileID, deleter.cmd.FileID)
	require.Len(t, deleter.cmd.Locations, 1)
	assert.Equal(t, "originals/"+fileID.String(), deleter.cmd.Locations[0].String())
}

func TestCommandHandlersRefuseWhatCanNeverBeDecoded(t *testing.T) {
	t.Parallel()

	fileID, err := domain.NewFileID()
	require.NoError(t, err)

	tests := []struct {
		name        string
		messageType string
		data        any
	}{
		{
			name: "process file with a broken id", messageType: TypeProcessFile,
			data: processFileData{FileID: "not-a-uuid", StagingKey: "staging/x"},
		},
		{
			name: "process file with a v4 id", messageType: TypeProcessFile,
			data: processFileData{
				FileID:     "6f8c3f7e-0e2e-4a1f-9b7a-2c9b1f0a5d31",
				StagingKey: "staging/x",
			},
		},
		{
			name: "process file with an escaping staging key", messageType: TypeProcessFile,
			data: processFileData{FileID: fileID.String(), StagingKey: "../etc/passwd"},
		},
		{
			name: "process file with a reserved variant", messageType: TypeProcessFile,
			data: processFileData{
				FileID:     fileID.String(),
				StagingKey: "staging/x",
				Variants:   []string{"100x100", "NOT A VARIANT"},
			},
		},
		{
			name: "delete objects with a broken id", messageType: TypeDeleteObjects,
			data: deleteObjectsData{FileID: "not-a-uuid"},
		},
		{
			name: "delete objects with an escaping key", messageType: TypeDeleteObjects,
			data: deleteObjectsData{
				FileID:      fileID.String(),
				StorageKeys: []string{"/absolute"},
			},
		},
	}

	handlers := CommandHandlers(&spyProcessor{}, &spyDeleter{})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Act
			err := handlers[test.messageType].Run(t.Context(), envelopeFor(t, test.messageType, test.data))

			// Assert
			require.ErrorIs(t, err, inbox.ErrUnprocessable,
				"a payload the domain refuses can never succeed, so it must not be retried")
		})
	}
}

func TestCommandHandlersRefuseABrokenPayload(t *testing.T) {
	t.Parallel()

	// Arrange
	handlers := CommandHandlers(&spyProcessor{}, &spyDeleter{})
	message := outbox.Envelope{Type: TypeProcessFile, Data: json.RawMessage(`{"file_id":`)}

	// Act
	err := handlers[TypeProcessFile].Run(t.Context(), message)

	// Assert
	require.ErrorIs(t, err, inbox.ErrUnprocessable)
}

func TestCommandHandlersSeparateTransientFromPermanent(t *testing.T) {
	t.Parallel()

	fileID, err := domain.NewFileID()
	require.NoError(t, err)

	data := processFileData{FileID: fileID.String(), StagingKey: "staging/x"}
	transient := errors.New("s3 is unreachable")
	permanent := fmt.Errorf("%w: file id is required", usecase.ErrInvalidCommand)

	t.Run("an infrastructure error is retried", func(t *testing.T) {
		t.Parallel()

		// Act
		handlers := CommandHandlers(&spyProcessor{err: transient}, &spyDeleter{})
		err := handlers[TypeProcessFile].Run(t.Context(), envelopeFor(t, TypeProcessFile, data))

		// Assert
		require.ErrorIs(t, err, transient)
		assert.NotErrorIs(t, err, inbox.ErrUnprocessable)
	})

	t.Run("an invalid command is dropped", func(t *testing.T) {
		t.Parallel()

		// Act
		handlers := CommandHandlers(&spyProcessor{err: permanent}, &spyDeleter{})
		err := handlers[TypeProcessFile].Run(t.Context(), envelopeFor(t, TypeProcessFile, data))

		// Assert
		require.ErrorIs(t, err, inbox.ErrUnprocessable)
		assert.ErrorIs(t, err, usecase.ErrInvalidCommand)
	})
}
