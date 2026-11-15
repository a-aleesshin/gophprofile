package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

type stubStaleFiles struct {
	files    []domain.File
	err      error
	statuses []domain.FileStatus
	before   time.Time
	limit    int
}

func (s *stubStaleFiles) StaleByStatus(
	_ context.Context, statuses []domain.FileStatus, before time.Time, limit int,
) ([]domain.File, error) {
	s.statuses = statuses
	s.before = before
	s.limit = limit

	return s.files, s.err
}

type reapHarness struct {
	handler   *ReapStaleFilesHandler
	lister    *stubStaleFiles
	saver     *stubFileLoaderSaver
	publisher *stubPublisher
	enqueuer  *stubDeleteEnqueuer
	tx        *stubTransactor
}

func newReapHarness(t *testing.T, files []domain.File, locations []domain.Location) reapHarness {
	t.Helper()

	lister := &stubStaleFiles{files: files}
	saver := &stubFileLoaderSaver{}
	publisher := &stubPublisher{}
	enqueuer := &stubDeleteEnqueuer{}
	tx := &stubTransactor{}

	return reapHarness{
		handler: NewReapStaleFilesHandler(
			lister, saver, &stubVariantLister{locations: locations},
			publisher, enqueuer, tx, fixedClock{now: testNow},
		),
		lister:    lister,
		saver:     saver,
		publisher: publisher,
		enqueuer:  enqueuer,
		tx:        tx,
	}
}

func testProcessingFile(t *testing.T) domain.File {
	t.Helper()

	file := testReceivedFile(t)
	require.NoError(t, file.StartProcessing(testNow.Add(-time.Hour)))

	return file
}

func TestReapStaleFiles(t *testing.T) {
	t.Parallel()

	t.Run("a stuck processing file is rejected and queued for cleanup", func(t *testing.T) {
		t.Parallel()

		file := testProcessingFile(t)
		_, locations := testReadyFileWithVariants(t)
		h := newReapHarness(t, []domain.File{file}, locations)

		got, err := h.handler.Handle(context.Background(), ReapStaleFilesCommand{
			Limit: 10, StaleAfter: 15 * time.Minute,
		})
		require.NoError(t, err)

		assert.Equal(t, ReapStaleFilesResult{Rejected: 1}, got)

		require.Len(t, h.saver.saved, 1)
		assert.True(t, h.saver.saved[0].Status().Equal(domain.FileStatusDeleting),
			"the file passes through rejected and ends up queued for deletion")
		assert.True(t, h.saver.saved[0].RejectReason().Equal(domain.RejectReasonInternalError))

		require.Len(t, h.publisher.rejected, 1)
		assert.True(t, h.publisher.rejected[0].Reason.Equal(domain.RejectReasonInternalError))

		require.Len(t, h.enqueuer.commands, 1)
		assert.Len(t, h.enqueuer.commands[0].Locations, 3)
		assert.Equal(t, 1, h.tx.calls, "status, fact and command land in one transaction")

		assert.Equal(t, testNow.Add(-15*time.Minute), h.lister.before)
		assert.Len(t, h.lister.statuses, 2)
	})

	t.Run("a stuck deletion only gets its command republished", func(t *testing.T) {
		t.Parallel()

		file := testDeletingFile(t, domain.FileStatusDeleting)
		h := newReapHarness(t, []domain.File{file}, nil)

		got, err := h.handler.Handle(context.Background(), ReapStaleFilesCommand{
			Limit: 10, StaleAfter: 15 * time.Minute,
		})
		require.NoError(t, err)

		assert.Equal(t, ReapStaleFilesResult{Requeued: 1}, got)
		assert.Empty(t, h.saver.saved, "the status is already correct")
		assert.Empty(t, h.publisher.rejected)
		require.Len(t, h.enqueuer.commands, 1)
		assert.Zero(t, h.tx.calls, "a single outbox insert needs no transaction")
	})

	t.Run("one failing file does not stop the sweep", func(t *testing.T) {
		t.Parallel()

		h := newReapHarness(t, []domain.File{
			testProcessingFile(t),
			testDeletingFile(t, domain.FileStatusDeleting),
		}, nil)
		h.tx.err = errors.New("deadlock detected")

		got, err := h.handler.Handle(context.Background(), ReapStaleFilesCommand{
			Limit: 10, StaleAfter: 15 * time.Minute,
		})
		require.NoError(t, err)
		assert.Equal(t, ReapStaleFilesResult{Requeued: 1, Failed: 1}, got)
	})

	t.Run("listing failure aborts the run", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("connection refused")
		h := newReapHarness(t, nil, nil)
		h.lister.err = boom

		_, err := h.handler.Handle(context.Background(), ReapStaleFilesCommand{
			Limit: 10, StaleAfter: time.Minute,
		})
		require.ErrorIs(t, err, boom)
	})

	t.Run("rejects an incomplete command", func(t *testing.T) {
		t.Parallel()

		h := newReapHarness(t, nil, nil)

		_, err := h.handler.Handle(context.Background(), ReapStaleFilesCommand{})
		require.ErrorIs(t, err, ErrInvalidCommand)

		_, err = h.handler.Handle(context.Background(), ReapStaleFilesCommand{Limit: 10})
		require.ErrorIs(t, err, ErrInvalidCommand)
		assert.Zero(t, h.lister.limit)
	})
}
