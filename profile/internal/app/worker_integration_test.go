//go:build integration

package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/internal/outbox"
	"github.com/a-aleesshin/gophprofile/profile/internal/adapter/messaging"
)

func TestBuildWorkerStartsAndDrainsTheOutbox(t *testing.T) {
	// Arrange
	cfg := workerConfig(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	pool, err := pgxpool.New(t.Context(), postgresDSN)
	require.NoError(t, err)

	t.Cleanup(pool.Close)

	payload, err := json.Marshal(outbox.Envelope{
		MessageID:  uuid.New(),
		Type:       messaging.TypeAvatarDeleted,
		OccurredAt: time.Now().UTC(),
		Data: json.RawMessage(
			`{"avatar_id": "0193abcd-ef01-7a23-8b45-0123456789ab",
			  "user_id": "user-1",
			  "file_id": "0193abcd-ef01-7a23-8b45-0123456789ac"}`),
	})
	require.NoError(t, err)

	var id int64

	require.NoError(t, pool.QueryRow(t.Context(), `
		INSERT INTO outbox (transport, destination, partition_key, message_type, payload)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		outbox.TransportKafka, messaging.DestinationProfileEvents, "user-1",
		messaging.TypeAvatarDeleted, payload,
	).Scan(&id))

	ctx, cancel := context.WithCancel(context.Background())

	runner, shutdown, err := BuildWorker(ctx, cfg, logger)
	require.NoError(t, err)

	defer shutdown()

	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	waitReady(t, cfg.HealthServer.Addr)

	select {
	case err := <-done:
		t.Fatalf("the worker stopped before it did any work: %v", err)
	case <-time.After(time.Second):
	}

	// Act
	var published *time.Time

	drained := assert.Eventually(t, func() bool {
		if err := pool.QueryRow(t.Context(),
			`SELECT published_at FROM outbox WHERE id = $1`, id).Scan(&published); err != nil {
			return false
		}

		return published != nil
	}, 30*time.Second, 200*time.Millisecond,
		"the relay must pick the row up and mark it published")

	// Assert
	if !drained {
		// Without this the failure says only "condition never satisfied", and the
		// relay records exactly why it could not publish.
		var (
			attempts  int
			lastError *string
		)

		require.NoError(t, pool.QueryRow(context.Background(),
			`SELECT attempts, last_error FROM outbox WHERE id = $1`, id).
			Scan(&attempts, &lastError))

		reason := "<none>"
		if lastError != nil {
			reason = *lastError
		}

		t.Fatalf("the row is still unpublished after %d attempts: %s", attempts, reason)
	}

	var attempts int

	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT attempts FROM outbox WHERE id = $1`, id).Scan(&attempts))
	assert.Equal(t, 1, attempts, "one attempt is enough when kafka is up")

	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled,
			"cancelling the context must stop the relay, the consumer and every job")
	case <-time.After(30 * time.Second):
		t.Fatal("the worker did not stop after the context was cancelled")
	}
}
