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

type stubExpiredUploads struct {
	uploads []domain.Upload
	err     error
	before  time.Time
	limit   int
}

func (s *stubExpiredUploads) PendingExpiredBefore(
	_ context.Context, before time.Time, limit int,
) ([]domain.Upload, error) {
	s.before = before
	s.limit = limit

	return s.uploads, s.err
}

type stubScriptedHeader struct {
	errs  []error
	calls int
}

func (s *stubScriptedHeader) Head(context.Context, domain.Location) (ObjectInfo, error) {
	s.calls++

	if len(s.errs) == 0 {
		return ObjectInfo{SizeBytes: 2048, ContentType: "image/jpeg"}, nil
	}

	next := s.errs[0]
	if len(s.errs) > 1 {
		s.errs = s.errs[1:]
	}

	if next != nil {
		return ObjectInfo{}, next
	}

	return ObjectInfo{SizeBytes: 2048, ContentType: "image/jpeg"}, nil
}

type stubCompleter struct {
	err      error
	commands []CompleteUploadCommand
}

func (s *stubCompleter) Handle(
	_ context.Context, cmd CompleteUploadCommand,
) (CompleteUploadResult, error) {
	s.commands = append(s.commands, cmd)

	return CompleteUploadResult{}, s.err
}

func TestReconcileUploads(t *testing.T) {
	t.Parallel()

	t.Run("present object is driven to completion", func(t *testing.T) {
		t.Parallel()

		upload := testPendingUpload(t, testNow.Add(-time.Hour))
		lister := &stubExpiredUploads{uploads: []domain.Upload{upload}}
		saver := &stubUploadRepository{}
		completer := &stubCompleter{}

		handler := NewReconcileUploadsHandler(
			lister, saver, &stubScriptedHeader{}, completer, fixedClock{now: testNow},
		)

		got, err := handler.Handle(context.Background(), ReconcileUploadsCommand{Limit: 10})
		require.NoError(t, err)

		assert.Equal(t, ReconcileUploadsResult{Completed: 1}, got)
		require.Len(t, completer.commands, 1)
		assert.True(t, completer.commands[0].UploadID.Equal(upload.ID()),
			"reconciliation reuses CompleteUpload instead of duplicating its logic")
		assert.Empty(t, saver.saved)
		assert.Equal(t, testNow, lister.before)
		assert.Equal(t, 10, lister.limit)
	})

	t.Run("absent object expires the ticket", func(t *testing.T) {
		t.Parallel()

		upload := testPendingUpload(t, testNow.Add(-time.Hour))
		saver := &stubUploadRepository{}
		completer := &stubCompleter{}

		handler := NewReconcileUploadsHandler(
			&stubExpiredUploads{uploads: []domain.Upload{upload}},
			saver,
			&stubScriptedHeader{errs: []error{ErrObjectNotFound}},
			completer,
			fixedClock{now: testNow},
		)

		got, err := handler.Handle(context.Background(), ReconcileUploadsCommand{Limit: 10})
		require.NoError(t, err)

		assert.Equal(t, ReconcileUploadsResult{Expired: 1}, got)
		require.Len(t, saver.saved, 1)
		assert.True(t, saver.saved[0].Status().Equal(domain.UploadStatusExpired))
		assert.Empty(t, completer.commands)
	})

	t.Run("one bad ticket does not stop the sweep", func(t *testing.T) {
		t.Parallel()

		uploads := []domain.Upload{
			testPendingUpload(t, testNow.Add(-time.Hour)),
			testPendingUpload(t, testNow.Add(-time.Hour)),
			testPendingUpload(t, testNow.Add(-time.Hour)),
		}

		handler := NewReconcileUploadsHandler(
			&stubExpiredUploads{uploads: uploads},
			&stubUploadRepository{},
			&stubScriptedHeader{errs: []error{
				nil,
				errors.New("connection reset"),
				ErrObjectNotFound,
			}},
			&stubCompleter{},
			fixedClock{now: testNow},
		)

		got, err := handler.Handle(context.Background(), ReconcileUploadsCommand{Limit: 10})
		require.NoError(t, err)
		assert.Equal(t, ReconcileUploadsResult{Completed: 1, Expired: 1, Failed: 1}, got)
	})

	t.Run("a failing completion is counted, not propagated", func(t *testing.T) {
		t.Parallel()

		handler := NewReconcileUploadsHandler(
			&stubExpiredUploads{uploads: []domain.Upload{testPendingUpload(t, testNow)}},
			&stubUploadRepository{},
			&stubScriptedHeader{},
			&stubCompleter{err: errors.New("deadlock detected")},
			fixedClock{now: testNow},
		)

		got, err := handler.Handle(context.Background(), ReconcileUploadsCommand{Limit: 10})
		require.NoError(t, err)
		assert.Equal(t, ReconcileUploadsResult{Failed: 1}, got)
	})

	t.Run("listing failure aborts the run", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("connection refused")
		handler := NewReconcileUploadsHandler(
			&stubExpiredUploads{err: boom},
			&stubUploadRepository{},
			&stubScriptedHeader{},
			&stubCompleter{},
			fixedClock{now: testNow},
		)

		_, err := handler.Handle(context.Background(), ReconcileUploadsCommand{Limit: 10})
		require.ErrorIs(t, err, boom)
	})

	t.Run("limit must be positive", func(t *testing.T) {
		t.Parallel()

		lister := &stubExpiredUploads{}
		handler := NewReconcileUploadsHandler(
			lister, &stubUploadRepository{}, &stubScriptedHeader{}, &stubCompleter{},
			fixedClock{now: testNow},
		)

		_, err := handler.Handle(context.Background(), ReconcileUploadsCommand{})
		require.ErrorIs(t, err, ErrInvalidCommand)
		assert.Zero(t, lister.limit)
	})
}
