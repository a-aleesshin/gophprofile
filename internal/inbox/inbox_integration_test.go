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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/a-aleesshin/gophprofile/file/migrations"
	"github.com/a-aleesshin/gophprofile/internal/clock"
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

func resetProcessed(t *testing.T) {
	t.Helper()

	_, err := sharedDB.Pool().Exec(context.Background(), `TRUNCATE processed_messages`)
	require.NoError(t, err)
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

func TestOnceOutsideTransactionLeavesNoTransactionOpen(t *testing.T) {
	// Arrange
	in := NewInbox(sharedDB)
	messageID := uuid.New()

	var (
		insideTransaction bool
		visibleToOthers   bool
	)

	// Act
	applied, err := in.OnceOutsideTransaction(t.Context(), "file.worker", messageID,
		func(ctx context.Context) error {
			insideTransaction = sharedDB.InTransaction(ctx)

			if _, writeErr := sharedDB.Querier(ctx).Exec(ctx, `
				INSERT INTO processed_messages (consumer, message_id)
				VALUES ('probe', $1)`, messageID); writeErr != nil {
				return writeErr
			}

			visibleToOthers = processedCount(t, "probe", messageID) == 1

			return nil
		})

	// Assert
	require.NoError(t, err)
	assert.True(t, applied)

	assert.False(t, insideTransaction,
		"a heavy effect must not hold a pool connection in a transaction")
	assert.True(t, visibleToOthers,
		"what the effect writes is committed at once, so the reaper can see the progress")
	assert.Equal(t, 1, processedCount(t, "file.worker", messageID))
}

func TestOnceOutsideTransactionRefusesToRunInsideOne(t *testing.T) {
	// Arrange
	in := NewInbox(sharedDB)
	messageID := uuid.New()

	// Act
	err := sharedDB.InTx(t.Context(), func(ctx context.Context) error {
		_, onceErr := in.OnceOutsideTransaction(ctx, "file.worker", messageID,
			func(context.Context) error { return nil })

		return onceErr
	})

	// Assert
	require.ErrorIs(t, err, ErrInvalidEffect,
		"the guarantee is in the name, so a caller must not be able to break it quietly")
	assert.Zero(t, processedCount(t, "file.worker", messageID))
}

func TestOnceOutsideTransactionSkipsAMessageItAlreadySaw(t *testing.T) {
	// Arrange
	in := NewInbox(sharedDB)
	messageID := uuid.New()

	var runs int

	effect := func(context.Context) error {
		runs++

		return nil
	}

	// Act
	first, err := in.OnceOutsideTransaction(t.Context(), "file.worker", messageID, effect)
	require.NoError(t, err)

	second, err := in.OnceOutsideTransaction(t.Context(), "file.worker", messageID, effect)
	require.NoError(t, err)

	// Assert
	assert.True(t, first)
	assert.False(t, second)
	assert.Equal(t, 1, runs)
}

func TestOnceOutsideTransactionKeepsTheMessageUnmarkedWhenTheEffectFails(t *testing.T) {
	// Arrange
	in := NewInbox(sharedDB)
	messageID := uuid.New()
	broken := errors.New("s3 is unreachable")

	// Act
	applied, err := in.OnceOutsideTransaction(t.Context(), "file.worker", messageID,
		func(context.Context) error { return broken })

	// Assert
	require.ErrorIs(t, err, broken)
	assert.False(t, applied)
	assert.Zero(t, processedCount(t, "file.worker", messageID),
		"a failed effect must stay unmarked, or the retry is lost")
}

func TestPrunerKeepsTheRedeliveryWindow(t *testing.T) {
	// Arrange
	resetProcessed(t)

	in := NewInbox(sharedDB)
	now := time.Now().UTC().Truncate(time.Microsecond)

	old := uuid.New()
	recent := uuid.New()

	for id, age := range map[uuid.UUID]time.Duration{
		old:    400 * time.Hour,
		recent: time.Hour,
	} {
		_, err := in.Mark(t.Context(), "prune.test", id)
		require.NoError(t, err)

		_, err = sharedDB.Pool().Exec(t.Context(), `
			UPDATE processed_messages SET processed_at = $3
			 WHERE consumer = $1 AND message_id = $2`,
			"prune.test", id, now.Add(-age))
		require.NoError(t, err)
	}

	pruner, err := NewPruner(sharedDB, clock.Fixed{At: now},
		PrunerConfig{Retention: 336 * time.Hour, BatchSize: 100})
	require.NoError(t, err)

	// Act
	removed, err := pruner.Prune(t.Context())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, int64(1), removed)

	assert.Zero(t, processedCount(t, "prune.test", old))
	assert.Equal(t, 1, processedCount(t, "prune.test", recent),
		"inside the window the mark must stay, or a replayed message is applied twice")
}

func TestPrunerDeletesInBatches(t *testing.T) {
	// Arrange
	resetProcessed(t)

	in := NewInbox(sharedDB)
	now := time.Now().UTC().Truncate(time.Microsecond)
	consumer := "prune.batches"

	for range 5 {
		id := uuid.New()

		_, err := in.Mark(t.Context(), consumer, id)
		require.NoError(t, err)

		_, err = sharedDB.Pool().Exec(t.Context(), `
			UPDATE processed_messages SET processed_at = $3
			 WHERE consumer = $1 AND message_id = $2`, consumer, id, now.Add(-400*time.Hour))
		require.NoError(t, err)
	}

	pruner, err := NewPruner(sharedDB, clock.Fixed{At: now},
		PrunerConfig{Retention: time.Hour, BatchSize: 2})
	require.NoError(t, err)

	// Act
	counts := make([]int64, 0, 4)

	for range 4 {
		removed, pruneErr := pruner.Prune(t.Context())
		require.NoError(t, pruneErr)

		counts = append(counts, removed)
	}

	// Assert
	assert.Equal(t, []int64{2, 2, 1, 0}, counts)
}

func TestNewPrunerRejectsABrokenConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		db     *pgtx.DB
		clock  Clock
		mutate func(*PrunerConfig)
	}{
		{name: "no db", clock: clock.System{}},
		{name: "no clock", db: sharedDB},
		{
			name: "zero retention", db: sharedDB, clock: clock.System{},
			mutate: func(c *PrunerConfig) { c.Retention = 0 },
		},
		{
			name: "zero batch size", db: sharedDB, clock: clock.System{},
			mutate: func(c *PrunerConfig) { c.BatchSize = 0 },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg := PrunerConfig{Retention: time.Hour, BatchSize: 10}
			if test.mutate != nil {
				test.mutate(&cfg)
			}

			// Act
			pruner, err := NewPruner(test.db, test.clock, cfg)

			// Assert
			require.ErrorIs(t, err, ErrInvalidPrunerConfig)
			assert.Nil(t, pruner)
		})
	}
}
