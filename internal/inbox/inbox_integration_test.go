//go:build integration

package inbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/a-aleesshin/gophprofile/file/migrations"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

var sharedDB *pgtx.DB

func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("file"),
		tcpostgres.WithUsername("file"),
		tcpostgres.WithPassword("file"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run postgres: %v\n", err)
		os.Exit(1)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "connection string: %v\n", err)
		os.Exit(1)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new pool: %v\n", err)
		os.Exit(1)
	}

	if err := migrations.Up(ctx, pool); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}

	sharedDB = pgtx.New(pool)

	code := m.Run()

	pool.Close()

	if err := container.Terminate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "terminate postgres: %v\n", err)
	}

	os.Exit(code)
}

func processedCount(t *testing.T, consumer string, messageID uuid.UUID) int {
	t.Helper()

	var count int

	require.NoError(t, sharedDB.Pool().QueryRow(context.Background(), `
		SELECT count(*) FROM processed_messages
		 WHERE consumer = $1 AND message_id = $2`, consumer, messageID).Scan(&count))

	return count
}

func TestInboxAppliesAnEffectOnce(t *testing.T) {
	// Arrange
	in := NewInbox(sharedDB)
	messageID := uuid.New()

	var runs int

	effect := func(context.Context) error {
		runs++

		return nil
	}

	// Act
	first, err := in.Once(t.Context(), "file.worker", messageID, effect)
	require.NoError(t, err)

	second, err := in.Once(t.Context(), "file.worker", messageID, effect)
	require.NoError(t, err)

	// Assert
	assert.True(t, first)
	assert.False(t, second, "a redelivery must not apply the effect again")
	assert.Equal(t, 1, runs)
	assert.Equal(t, 1, processedCount(t, "file.worker", messageID))
}

func TestInboxLetsAnotherConsumerSeeTheSameMessage(t *testing.T) {
	// Arrange
	in := NewInbox(sharedDB)
	messageID := uuid.New()

	// Act
	first, err := in.Once(t.Context(), "file.worker", messageID,
		func(context.Context) error { return nil })
	require.NoError(t, err)

	second, err := in.Once(t.Context(), "profile.worker", messageID,
		func(context.Context) error { return nil })
	require.NoError(t, err)

	// Assert
	assert.True(t, first)
	assert.True(t, second, "the key is (consumer, message_id), not the message alone")
}

func TestInboxRollsBackTheMarkWhenTheEffectFails(t *testing.T) {
	// Arrange
	in := NewInbox(sharedDB)
	messageID := uuid.New()
	broken := errors.New("s3 is unreachable")

	// Act
	applied, err := in.Once(t.Context(), "file.worker", messageID,
		func(context.Context) error { return broken })

	// Assert
	require.ErrorIs(t, err, broken)
	assert.False(t, applied)
	assert.Zero(t, processedCount(t, "file.worker", messageID),
		"a failed effect must leave the message unprocessed, so it can be retried")

	retried, err := in.Once(t.Context(), "file.worker", messageID,
		func(context.Context) error { return nil })
	require.NoError(t, err)
	assert.True(t, retried)
}

func TestInboxAppliesOnceUnderConcurrentRedelivery(t *testing.T) {
	// Arrange
	in := NewInbox(sharedDB)
	messageID := uuid.New()

	var (
		runs    atomic.Int32
		applied atomic.Int32
		wg      sync.WaitGroup
	)

	// Act
	for range 8 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			ok, err := in.Once(context.Background(), "file.worker", messageID,
				func(context.Context) error {
					runs.Add(1)

					return nil
				})
			assert.NoError(t, err)

			if ok {
				applied.Add(1)
			}
		}()
	}

	wg.Wait()

	// Assert
	assert.Equal(t, int32(1), applied.Load())
	assert.Equal(t, int32(1), runs.Load(),
		"two workers must not run the same effect at the same time")
}

func TestInboxRejectsAnIncompleteCall(t *testing.T) {
	in := NewInbox(sharedDB)
	effect := func(context.Context) error { return nil }

	tests := []struct {
		name      string
		consumer  string
		messageID uuid.UUID
		effect    func(context.Context) error
	}{
		{name: "no consumer", messageID: uuid.New(), effect: effect},
		{name: "no message id", consumer: "file.worker", effect: effect},
		{name: "no effect", consumer: "file.worker", messageID: uuid.New()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act
			applied, err := in.Once(t.Context(), test.consumer, test.messageID, test.effect)

			// Assert
			require.ErrorIs(t, err, ErrInvalidEffect)
			assert.False(t, applied)
		})
	}
}
