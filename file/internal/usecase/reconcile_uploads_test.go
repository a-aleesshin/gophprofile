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

const testReconcileAttempts = 3

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
	errs      []error
	deleteErr error
	calls     int
	deleted   []domain.Location
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

func (s *stubScriptedHeader) Delete(_ context.Context, locations ...domain.Location) error {
	s.deleted = append(s.deleted, locations...)

	return s.deleteErr
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

func newTestReconciler(
	t *testing.T,
	lister expiredUploadLister,
	saver uploadSaver,
	objects stagingObjects,
	completer uploadCompleter,
) *ReconcileUploadsHandler {
	t.Helper()

	handler, err := NewReconcileUploadsHandler(
		lister, saver, objects, completer, fixedClock{now: testNow}, testReconcileAttempts,
	)
	require.NoError(t, err)

	return handler
}

func testUploadWithAttempts(t *testing.T, attempts int) domain.Upload {
	t.Helper()

	upload := testPendingUpload(t, testNow.Add(-time.Hour))

	for range attempts {
		require.NoError(t, upload.FailAttempt(testNow.Add(-time.Minute)))
	}

	require.Equal(t, attempts, upload.Attempts())

	return upload
}

func TestReconcileUploads(t *testing.T) {
	t.Parallel()

	t.Run("present object is driven to completion", func(t *testing.T) {
		t.Parallel()

		upload := testPendingUpload(t, testNow.Add(-time.Hour))
		lister := &stubExpiredUploads{uploads: []domain.Upload{upload}}
		saver := &stubUploadRepository{}
		completer := &stubCompleter{}

		handler := newTestReconciler(t, lister, saver, &stubScriptedHeader{}, completer)

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

		handler := newTestReconciler(t,
			&stubExpiredUploads{uploads: []domain.Upload{upload}},
			saver,
			&stubScriptedHeader{errs: []error{ErrObjectNotFound}},
			completer,
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

		handler := newTestReconciler(t,
			&stubExpiredUploads{uploads: uploads},
			&stubUploadRepository{},
			&stubScriptedHeader{errs: []error{
				nil,
				errors.New("connection reset"),
				ErrObjectNotFound,
			}},
			&stubCompleter{},
		)

		got, err := handler.Handle(context.Background(), ReconcileUploadsCommand{Limit: 10})
		require.NoError(t, err)
		assert.Equal(t, 1, got.Completed)
		assert.Equal(t, 1, got.Expired)
		assert.Equal(t, 1, got.Failed)
		assert.Zero(t, got.Aborted)
	})

	t.Run("a failing completion burns one attempt and keeps the ticket", func(t *testing.T) {
		t.Parallel()

		saver := &stubUploadRepository{}
		objects := &stubScriptedHeader{}

		handler := newTestReconciler(t,
			&stubExpiredUploads{uploads: []domain.Upload{testUploadWithAttempts(t, 0)}},
			saver,
			objects,
			&stubCompleter{err: errors.New("deadlock detected")},
		)

		got, err := handler.Handle(context.Background(), ReconcileUploadsCommand{Limit: 10})
		require.NoError(t, err)

		assert.Equal(t, 1, got.Failed)
		assert.Zero(t, got.Aborted)
		require.Len(t, saver.saved, 1)
		assert.Equal(t, 1, saver.saved[0].Attempts(),
			"a transient failure must cost exactly one attempt")
		assert.True(t, saver.saved[0].IsPending(),
			"a ticket with attempts left stays pending so the next pass retries it")
		assert.Empty(t, objects.deleted)
	})

	t.Run("the last attempt aborts the ticket and drops its object", func(t *testing.T) {
		t.Parallel()

		upload := testUploadWithAttempts(t, testReconcileAttempts-1)
		saver := &stubUploadRepository{}
		objects := &stubScriptedHeader{}

		handler := newTestReconciler(t,
			&stubExpiredUploads{uploads: []domain.Upload{upload}},
			saver,
			objects,
			&stubCompleter{err: errors.New(`mime: expected slash after first token`)},
		)

		got, err := handler.Handle(context.Background(), ReconcileUploadsCommand{Limit: 10})
		require.NoError(t, err)

		assert.Equal(t, 1, got.Aborted)
		assert.Zero(t, got.Failed,
			"an aborted ticket is settled, not another failure to retry")

		require.Len(t, saver.saved, 1)
		assert.True(t, saver.saved[0].Status().Equal(domain.UploadStatusAborted),
			"a poisoned ticket must leave the pending set, or it is retried forever")

		require.Len(t, objects.deleted, 1)
		assert.Equal(t, upload.Location().String(), objects.deleted[0].String(),
			"the staging object of a ticket nothing will ever reference must not survive it")

		require.Len(t, got.Failures, 1)
		assert.True(t, got.Failures[0].UploadID.Equal(upload.ID()),
			"the caller must learn which ticket died, not just how many")
		assert.Equal(t, testReconcileAttempts, got.Failures[0].Attempts)
		assert.ErrorContains(t, got.Failures[0].Err, "mime")
	})

	t.Run("a ticket whose object will not delete stays pending", func(t *testing.T) {
		t.Parallel()

		saver := &stubUploadRepository{}
		objects := &stubScriptedHeader{deleteErr: errors.New("503 SlowDown")}

		handler := newTestReconciler(t,
			&stubExpiredUploads{
				uploads: []domain.Upload{testUploadWithAttempts(t, testReconcileAttempts-1)},
			},
			saver,
			objects,
			&stubCompleter{err: errors.New("bad content type")},
		)

		got, err := handler.Handle(context.Background(), ReconcileUploadsCommand{Limit: 10})
		require.NoError(t, err)

		assert.Zero(t, got.Aborted,
			"aborting before the object is gone would strand it with nothing left to retry")
		assert.Equal(t, 1, got.Failed)
		assert.Empty(t, saver.saved,
			"the row must keep its pending status so the next pass retries the delete")
		require.Len(t, got.Failures, 1)
		assert.ErrorContains(t, got.Failures[0].Err, "SlowDown")
	})

	t.Run("an unreachable store also runs out of attempts", func(t *testing.T) {
		t.Parallel()

		saver := &stubUploadRepository{}
		objects := &stubScriptedHeader{
			errs: []error{errors.New("AccessDenied")},
		}

		handler := newTestReconciler(t,
			&stubExpiredUploads{
				uploads: []domain.Upload{testUploadWithAttempts(t, testReconcileAttempts-1)},
			},
			saver,
			objects,
			&stubCompleter{},
		)

		got, err := handler.Handle(context.Background(), ReconcileUploadsCommand{Limit: 10})
		require.NoError(t, err)

		assert.Equal(t, 1, got.Aborted,
			"a HEAD that keeps failing for a reason of its own must terminate too")
		require.Len(t, saver.saved, 1)
		assert.True(t, saver.saved[0].Status().Equal(domain.UploadStatusAborted))
	})

	t.Run("listing failure aborts the run", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("connection refused")
		handler := newTestReconciler(t,
			&stubExpiredUploads{err: boom},
			&stubUploadRepository{},
			&stubScriptedHeader{},
			&stubCompleter{},
		)

		_, err := handler.Handle(context.Background(), ReconcileUploadsCommand{Limit: 10})
		require.ErrorIs(t, err, boom)
	})

	t.Run("limit must be positive", func(t *testing.T) {
		t.Parallel()

		lister := &stubExpiredUploads{}
		handler := newTestReconciler(t,
			lister, &stubUploadRepository{}, &stubScriptedHeader{}, &stubCompleter{},
		)

		_, err := handler.Handle(context.Background(), ReconcileUploadsCommand{})
		require.ErrorIs(t, err, ErrInvalidCommand)
		assert.Zero(t, lister.limit)
	})

	t.Run("max attempts must be positive", func(t *testing.T) {
		t.Parallel()

		_, err := NewReconcileUploadsHandler(
			&stubExpiredUploads{}, &stubUploadRepository{}, &stubScriptedHeader{},
			&stubCompleter{}, fixedClock{now: testNow}, 0,
		)
		require.ErrorIs(t, err, ErrInvalidCommand)
	})
}
