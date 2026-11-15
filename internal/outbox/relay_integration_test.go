//go:build integration

package outbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/a-aleesshin/gophprofile/file/migrations"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/a-aleesshin/gophprofile/internal/clock"
	"github.com/a-aleesshin/gophprofile/internal/metrics"
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

type recordingSender struct {
	sent     []Pending
	attempts int
	failOn   int
	failAll  bool
	err      error
	before   func(context.Context, Pending)
}

func (s *recordingSender) Send(ctx context.Context, message Pending) error {
	if s.before != nil {
		s.before(ctx, message)
	}

	s.attempts++

	if s.err != nil && (s.failAll || s.attempts == s.failOn) {
		return s.err
	}

	s.sent = append(s.sent, message)

	return nil
}

type storedRow struct {
	Transport   string
	PublishedAt *time.Time
	Attempts    int
	LastError   *string
	AvailableAt time.Time
}

func resetOutbox(t *testing.T) {
	t.Helper()

	_, err := sharedDB.Pool().Exec(context.Background(), `TRUNCATE outbox RESTART IDENTITY`)
	require.NoError(t, err)
}

func insertPending(
	t *testing.T,
	transport string,
	partitionKey string,
	attempts int,
	availableAt time.Time,
) int64 {
	t.Helper()

	var key *string

	if partitionKey != "" {
		key = &partitionKey
	}

	var id int64

	err := sharedDB.Pool().QueryRow(context.Background(), `
		INSERT INTO outbox (transport, destination, partition_key, message_type, payload,
			attempts, available_at)
		VALUES ($1, $2, $3, 'TestMessage', '{"x":1}', $4, $5)
		RETURNING id`,
		transport, "test.destination", key, attempts, availableAt).Scan(&id)
	require.NoError(t, err)

	return id
}

func readRow(t *testing.T, id int64) storedRow {
	t.Helper()

	var row storedRow

	err := sharedDB.Pool().QueryRow(context.Background(), `
		SELECT transport, published_at, attempts, last_error, available_at
		  FROM outbox WHERE id = $1`, id).
		Scan(&row.Transport, &row.PublishedAt, &row.Attempts, &row.LastError, &row.AvailableAt)
	require.NoError(t, err)

	return row
}

func testRelay(
	t *testing.T,
	transport string,
	sender Sender,
	now time.Time,
	mutate func(*RelayConfig),
) *Relay {
	t.Helper()

	cfg := RelayConfig{
		BatchSize:              100,
		Interval:               10 * time.Millisecond,
		MinBackoff:             time.Second,
		MaxBackoff:             time.Minute,
		MaxConsecutiveFailures: 3,
		Lease:                  30 * time.Second,
	}

	if mutate != nil {
		mutate(&cfg)
	}

	relay, err := NewRelay(sharedDB, transport, sender, clock.Fixed{At: now}, cfg)
	require.NoError(t, err)

	return relay
}

func TestRelayPublishesDueMessagesInIDOrder(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(-time.Minute)

	first := insertPending(t, TransportKafka, "file-1", 0, due)
	second := insertPending(t, TransportKafka, "", 0, due)
	third := insertPending(t, TransportKafka, "file-1", 0, due)

	sender := &recordingSender{}
	relay := testRelay(t, TransportKafka, sender, now, nil)

	// Act
	stats, err := relay.Tick(context.Background())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RelayStats{Published: 3}, stats)

	require.Len(t, sender.sent, 3)
	assert.Equal(t, []int64{first, second, third},
		[]int64{sender.sent[0].ID, sender.sent[1].ID, sender.sent[2].ID})
	assert.Equal(t, "test.destination", sender.sent[0].Destination)
	assert.Equal(t, "TestMessage", sender.sent[0].Type)
	assert.JSONEq(t, `{"x":1}`, string(sender.sent[0].Payload))
	assert.Equal(t, "file-1", sender.sent[0].PartitionKey)
	assert.Empty(t, sender.sent[1].PartitionKey)

	for _, id := range []int64{first, second, third} {
		row := readRow(t, id)
		require.NotNil(t, row.PublishedAt)
		assert.Equal(t, now, row.PublishedAt.UTC())
		assert.Equal(t, 1, row.Attempts)
		assert.Nil(t, row.LastError)
	}
}

func TestRelayLeavesMessagesThatAreNotDue(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	later := insertPending(t, TransportKafka, "file-1", 3, now.Add(time.Minute))

	sender := &recordingSender{}
	relay := testRelay(t, TransportKafka, sender, now, nil)

	// Act
	stats, err := relay.Tick(context.Background())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RelayStats{}, stats)
	assert.Empty(t, sender.sent)
	assert.Nil(t, readRow(t, later).PublishedAt)
}

func TestRelayIgnoresAlreadyPublishedMessages(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	id := insertPending(t, TransportKafka, "file-1", 1, now.Add(-time.Minute))

	_, err := sharedDB.Pool().Exec(context.Background(),
		`UPDATE outbox SET published_at = now() WHERE id = $1`, id)
	require.NoError(t, err)

	sender := &recordingSender{}
	relay := testRelay(t, TransportKafka, sender, now, nil)

	// Act
	stats, err := relay.Tick(context.Background())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RelayStats{}, stats)
	assert.Empty(t, sender.sent)
}

func TestRelayBlocksTheScopeOfAFailedMessage(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(-time.Minute)

	first := insertPending(t, TransportKafka, "file-1", 0, due)
	second := insertPending(t, TransportKafka, "file-1", 0, due)
	third := insertPending(t, TransportKafka, "file-1", 0, due)

	sendErr := errors.New("broker is down")
	sender := &recordingSender{failOn: 2, err: sendErr}
	relay := testRelay(t, TransportKafka, sender, now, nil)

	// Act
	stats, err := relay.Tick(context.Background())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RelayStats{Published: 1, Failed: 1}, stats)
	require.Len(t, sender.sent, 1)
	assert.Equal(t, first, sender.sent[0].ID)

	published := readRow(t, first)
	require.NotNil(t, published.PublishedAt)
	assert.Equal(t, 1, published.Attempts)

	failed := readRow(t, second)
	assert.Nil(t, failed.PublishedAt)
	assert.Equal(t, 1, failed.Attempts)
	require.NotNil(t, failed.LastError)
	assert.Contains(t, *failed.LastError, "broker is down")
	assert.Equal(t, now.Add(time.Second), failed.AvailableAt.UTC())

	untouched := readRow(t, third)
	assert.Nil(t, untouched.PublishedAt)
	assert.Zero(t, untouched.Attempts)
	assert.Nil(t, untouched.LastError)
	assert.Equal(t, due, untouched.AvailableAt.UTC())
}

func TestRelayBacksOffExponentiallyPerAttempt(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)

	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{attempts: 0, want: time.Second},
		{attempts: 3, want: 8 * time.Second},
		{attempts: 30, want: time.Minute},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("after %d attempts", test.attempts), func(t *testing.T) {
			resetOutbox(t)

			id := insertPending(t, TransportKafka, "file-1", test.attempts, now.Add(-time.Minute))
			sender := &recordingSender{failOn: 1, err: errors.New("broker is down")}
			relay := testRelay(t, TransportKafka, sender, now, nil)

			// Act
			_, err := relay.Tick(context.Background())

			// Assert
			require.NoError(t, err)

			row := readRow(t, id)
			assert.Equal(t, test.attempts+1, row.Attempts)
			assert.Equal(t, now.Add(test.want), row.AvailableAt.UTC())
		})
	}
}

func TestRelayIgnoresTheOtherTransport(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(-time.Minute)

	command := insertPending(t, TransportRabbit, "file-1", 0, due)
	fact := insertPending(t, TransportKafka, "file-1", 0, due)

	kafka := &recordingSender{}
	rabbit := &recordingSender{}

	// Act
	factStats, err := testRelay(t, TransportKafka, kafka, now, nil).Tick(context.Background())
	require.NoError(t, err)

	commandStats, err := testRelay(t, TransportRabbit, rabbit, now, nil).Tick(context.Background())
	require.NoError(t, err)

	// Assert
	assert.Equal(t, RelayStats{Published: 1}, factStats)
	assert.Equal(t, RelayStats{Published: 1}, commandStats)

	require.Len(t, kafka.sent, 1)
	assert.Equal(t, fact, kafka.sent[0].ID)
	assert.Equal(t, TransportKafka, kafka.sent[0].Transport)

	require.Len(t, rabbit.sent, 1)
	assert.Equal(t, command, rabbit.sent[0].ID)
	assert.Equal(t, TransportRabbit, rabbit.sent[0].Transport)
}

func TestRelayRespectsTheBatchSize(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(-time.Minute)

	ids := make([]int64, 0, 5)
	for range 5 {
		ids = append(ids, insertPending(t, TransportKafka, "file-1", 0, due))
	}

	sender := &recordingSender{}
	relay := testRelay(t, TransportKafka, sender, now, func(c *RelayConfig) { c.BatchSize = 2 })

	// Act
	stats, err := relay.Tick(context.Background())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RelayStats{Published: 2}, stats)
	assert.NotNil(t, readRow(t, ids[1]).PublishedAt)
	assert.Nil(t, readRow(t, ids[2]).PublishedAt)
}

func TestRelaySkipsRowsAnotherRelayIsAlreadySending(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(-time.Minute)

	ids := make([]int64, 0, 4)
	for n := range 4 {
		ids = append(ids, insertPending(t, TransportKafka, fmt.Sprintf("file-%d", n), 0, due))
	}

	claimed := make(chan struct{})
	release := make(chan struct{})

	blocking := &recordingSender{before: func(context.Context, Pending) {
		close(claimed)
		<-release
	}}

	slow := testRelay(t, TransportKafka, blocking, now, func(c *RelayConfig) { c.BatchSize = 1 })
	fast := testRelay(t, TransportKafka, &recordingSender{}, now, nil)

	done := make(chan RelayStats, 1)

	go func() {
		stats, err := slow.Tick(context.Background())
		assert.NoError(t, err)
		done <- stats
	}()

	<-claimed

	// Act
	stats, err := fast.Tick(context.Background())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RelayStats{Published: 3}, stats,
		"the leased message belongs to the relay that took it, the rest are free")

	close(release)
	assert.Equal(t, RelayStats{Published: 1}, <-done)

	for _, id := range ids {
		assert.NotNil(t, readRow(t, id).PublishedAt, "message %d", id)
	}
}

func TestRelayRunDrainsFullBatchesWithoutWaiting(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(-time.Minute)

	ids := make([]int64, 0, 6)
	for range 6 {
		ids = append(ids, insertPending(t, TransportKafka, "file-1", 0, due))
	}

	sender := &recordingSender{}
	relay := testRelay(t, TransportKafka, sender, now, func(c *RelayConfig) {
		c.BatchSize = 2
		c.Interval = time.Hour
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Act
	err := relay.Run(ctx)

	// Assert
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Len(t, sender.sent, 6)

	for _, id := range ids {
		assert.NotNil(t, readRow(t, id).PublishedAt, "message %d", id)
	}
}

func TestRelayRunStopsWhenTheContextIsDone(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	relay := testRelay(t, TransportKafka, &recordingSender{}, now, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	err := relay.Run(ctx)

	// Assert
	require.ErrorIs(t, err, context.Canceled)
}

func TestRelayDoesNotLetLaterMessagesOvertakeAStalledOne(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(-time.Minute)

	head := insertPending(t, TransportKafka, "file-1", 0, due)
	second := insertPending(t, TransportKafka, "file-1", 0, due)
	third := insertPending(t, TransportKafka, "file-1", 0, due)

	failing := &recordingSender{failOn: 1, err: errors.New("broker is down")}

	stats, err := testRelay(t, TransportKafka, failing, now, nil).Tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, RelayStats{Failed: 1}, stats)
	require.Greater(t, readRow(t, head).AvailableAt.UTC(), now)

	healthy := &recordingSender{}

	// Act
	stats, err = testRelay(t, TransportKafka, healthy, now, nil).Tick(context.Background())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RelayStats{}, stats)
	assert.Empty(t, healthy.sent, "a message behind a stalled one must not be published first")
	assert.Nil(t, readRow(t, second).PublishedAt)
	assert.Nil(t, readRow(t, third).PublishedAt)
	assert.Zero(t, readRow(t, second).Attempts)
}

func TestRelayResumesTheQueueOnceTheBackoffHasElapsed(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(-time.Minute)

	head := insertPending(t, TransportKafka, "file-1", 0, due)
	second := insertPending(t, TransportKafka, "file-1", 0, due)

	failing := &recordingSender{failOn: 1, err: errors.New("broker is down")}

	_, err := testRelay(t, TransportKafka, failing, now, nil).Tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, now.Add(time.Second), readRow(t, head).AvailableAt.UTC())

	healthy := &recordingSender{}

	// Act
	stats, err := testRelay(t, TransportKafka, healthy, now.Add(time.Second), nil).Tick(context.Background())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RelayStats{Published: 2}, stats)
	require.Len(t, healthy.sent, 2)
	assert.Equal(t, []int64{head, second},
		[]int64{healthy.sent[0].ID, healthy.sent[1].ID})
	assert.Equal(t, 2, readRow(t, head).Attempts)
	assert.Equal(t, 1, readRow(t, second).Attempts)
}

func TestRelayKeepsOtherScopesFlowingPastAFailure(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(-time.Minute)

	stalled := insertPending(t, TransportKafka, "file-1", 0, due)
	behind := insertPending(t, TransportKafka, "file-1", 0, due)
	other := insertPending(t, TransportKafka, "file-2", 0, due)
	keyless := insertPending(t, TransportKafka, "", 0, due)

	sender := &recordingSender{failOn: 1, err: errors.New("broker rejected it")}
	relay := testRelay(t, TransportKafka, sender, now, nil)

	// Act
	stats, err := relay.Tick(context.Background())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RelayStats{Published: 2, Failed: 1}, stats)

	require.Len(t, sender.sent, 2)
	assert.Equal(t, []int64{other, keyless},
		[]int64{sender.sent[0].ID, sender.sent[1].ID})

	assert.NotNil(t, readRow(t, other).PublishedAt)
	assert.NotNil(t, readRow(t, keyless).PublishedAt)

	assert.Equal(t, 1, readRow(t, stalled).Attempts)
	assert.Nil(t, readRow(t, stalled).PublishedAt)

	assert.Zero(t, readRow(t, behind).Attempts, "a message behind a failure must not be sent")
	assert.Nil(t, readRow(t, behind).PublishedAt)
	assert.Nil(t, readRow(t, behind).LastError)
}

func TestRelayRecordsEveryFailureInTheBatch(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(-time.Minute)

	first := insertPending(t, TransportKafka, "file-1", 0, due)
	second := insertPending(t, TransportKafka, "file-2", 2, due)
	third := insertPending(t, TransportKafka, "file-3", 30, due)

	sender := &recordingSender{failAll: true, err: errors.New("broker is down")}
	relay := testRelay(t, TransportKafka, sender, now, nil)

	// Act
	stats, err := relay.Tick(context.Background())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RelayStats{Failed: 3}, stats)
	assert.Empty(t, sender.sent)
	assert.Equal(t, 3, sender.attempts)

	for id, want := range map[int64]time.Duration{
		first:  time.Second,
		second: 4 * time.Second,
		third:  time.Minute,
	} {
		row := readRow(t, id)
		assert.Nil(t, row.PublishedAt, "message %d", id)
		require.NotNil(t, row.LastError, "message %d", id)
		assert.Contains(t, *row.LastError, "broker is down")
		assert.Equal(t, now.Add(want), row.AvailableAt.UTC(), "message %d", id)
	}
}

func TestRelayRecordsWhatItSentWhenTheContextIsCancelled(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(-time.Minute)

	first := insertPending(t, TransportKafka, "file-1", 0, due)
	second := insertPending(t, TransportKafka, "file-2", 0, due)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sender := &recordingSender{failOn: 2, err: errors.New("connection closed")}
	sender.before = func(context.Context, Pending) {
		if sender.attempts == 1 {
			cancel()
		}
	}

	relay := testRelay(t, TransportKafka, sender, now, nil)

	// Act
	_, err := relay.Tick(ctx)

	// Assert
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "connection closed")

	sent := readRow(t, first)
	require.NotNil(t, sent.PublishedAt,
		"the first message reached the broker, and re-sending what we know went out is worse "+
			"than recording it during a shutdown")
	assert.Equal(t, now, sent.PublishedAt.UTC())

	stopped := readRow(t, second)
	assert.Nil(t, stopped.PublishedAt)
	assert.Zero(t, stopped.Attempts, "a shutdown is not a delivery failure")
	assert.Nil(t, stopped.LastError)
	assert.Equal(t, due, stopped.AvailableAt.UTC(),
		"its own schedule is restored, so the next run picks it up at once")
}

func TestRelayStopsTheTickWhenFailuresRunInARow(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(-time.Minute)

	for n := range 50 {
		insertPending(t, TransportKafka, fmt.Sprintf("file-%d", n), 0, due)
	}

	sender := &recordingSender{failAll: true, err: errors.New("i/o timeout")}
	relay := testRelay(t, TransportKafka, sender, now, func(c *RelayConfig) {
		c.MaxConsecutiveFailures = 3
	})

	// Act
	stats, err := relay.Tick(context.Background())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RelayStats{Failed: 3}, stats)
	assert.Equal(t, 3, sender.attempts,
		"a dead broker must cost a bounded number of attempts, not the whole batch")
}

func TestRelayKeepsGoingPastAnIsolatedFailure(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(-time.Minute)

	ids := make([]int64, 0, 10)
	for n := range 10 {
		ids = append(ids, insertPending(t, TransportKafka, fmt.Sprintf("file-%d", n), 0, due))
	}

	sender := &recordingSender{failOn: 2, err: errors.New("payload too large")}
	relay := testRelay(t, TransportKafka, sender, now, func(c *RelayConfig) {
		c.MaxConsecutiveFailures = 3
	})

	// Act
	stats, err := relay.Tick(context.Background())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RelayStats{Published: 9, Failed: 1}, stats,
		"one bad message among healthy ones must not cut the batch short")
	assert.Nil(t, readRow(t, ids[1]).PublishedAt)
	assert.NotNil(t, readRow(t, ids[9]).PublishedAt)
}

func TestRelayPublishesOutsideAnyTransaction(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	id := insertPending(t, TransportKafka, "file-1", 0, now.Add(-time.Minute))

	var (
		insideTransaction bool
		leaseWhileSending time.Time
	)

	sender := &recordingSender{before: func(ctx context.Context, _ Pending) {
		insideTransaction = sharedDB.InTransaction(ctx)
		leaseWhileSending = readRow(t, id).AvailableAt.UTC()
	}}

	relay := testRelay(t, TransportKafka, sender, now, func(c *RelayConfig) {
		c.Lease = time.Minute
	})

	// Act
	stats, err := relay.Tick(context.Background())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RelayStats{Published: 1}, stats)

	assert.False(t, insideTransaction)
	assert.Equal(t, now.Add(time.Minute), leaseWhileSending,
		"the lease is committed before the send, so no transaction is open while the broker answers")

	row := readRow(t, id)
	require.NotNil(t, row.PublishedAt)
	assert.Equal(t, now, row.PublishedAt.UTC())
}

func TestRelayLeaseHoldsTheScopeForAnotherRelay(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(-time.Minute)

	first := insertPending(t, TransportKafka, "file-1", 0, due)
	second := insertPending(t, TransportKafka, "file-1", 0, due)
	other := insertPending(t, TransportKafka, "file-2", 0, due)

	claimed := make(chan struct{})
	release := make(chan struct{})

	slow := &recordingSender{before: func(context.Context, Pending) {
		close(claimed)
		<-release
	}}

	leasing := testRelay(t, TransportKafka, slow, now, func(c *RelayConfig) {
		c.BatchSize = 1
		c.Lease = time.Minute
	})

	done := make(chan RelayStats, 1)

	go func() {
		stats, tickErr := leasing.Tick(context.Background())
		assert.NoError(t, tickErr)
		done <- stats
	}()

	<-claimed

	// Act
	second2, err := testRelay(t, TransportKafka, &recordingSender{}, now, nil).
		Tick(context.Background())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, RelayStats{Published: 1}, second2,
		"the leased row holds its scope, so only the unrelated file goes out")
	assert.NotNil(t, readRow(t, other).PublishedAt)
	assert.Nil(t, readRow(t, second).PublishedAt,
		"the second message of a leased file must wait for its predecessor")

	close(release)
	assert.Equal(t, RelayStats{Published: 1}, <-done)
	assert.NotNil(t, readRow(t, first).PublishedAt)
}

func TestRelayRetriesALeasedMessageAfterTheLeaseExpires(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	id := insertPending(t, TransportKafka, "file-1", 0, now.Add(-time.Minute))

	abandoned := testRelay(t, TransportKafka, &recordingSender{}, now, func(c *RelayConfig) {
		c.Lease = time.Minute
	})

	leased, err := abandoned.lease(context.Background(), now)
	require.NoError(t, err)
	require.Len(t, leased, 1, "the row is leased and the worker then dies before settling")

	// Act
	tooEarly, err := testRelay(t, TransportKafka, &recordingSender{},
		now.Add(30*time.Second), nil).Tick(context.Background())
	require.NoError(t, err)

	sender := &recordingSender{}

	afterLease, err := testRelay(t, TransportKafka, sender,
		now.Add(2*time.Minute), nil).Tick(context.Background())
	require.NoError(t, err)

	// Assert
	assert.Equal(t, RelayStats{}, tooEarly, "within the lease nobody else touches it")
	assert.Equal(t, RelayStats{Published: 1}, afterLease,
		"once the lease expires the message is published again, which processed_messages absorbs")
	assert.Len(t, sender.sent, 1)
	assert.NotNil(t, readRow(t, id).PublishedAt)
}

func TestPrunerRemovesOnlyPublishedRowsPastRetention(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)

	old := insertPending(t, TransportKafka, "file-1", 0, now)
	recent := insertPending(t, TransportKafka, "file-2", 0, now)
	unpublished := insertPending(t, TransportKafka, "file-3", 0, now)

	_, err := sharedDB.Pool().Exec(context.Background(),
		`UPDATE outbox SET published_at = $2 WHERE id = $1`, old, now.Add(-100*time.Hour))
	require.NoError(t, err)

	_, err = sharedDB.Pool().Exec(context.Background(),
		`UPDATE outbox SET published_at = $2 WHERE id = $1`, recent, now.Add(-time.Hour))
	require.NoError(t, err)

	pruner, err := NewPruner(sharedDB, clock.Fixed{At: now},
		PrunerConfig{Retention: 72 * time.Hour, BatchSize: 100})
	require.NoError(t, err)

	// Act
	removed, err := pruner.Prune(context.Background())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, int64(1), removed)

	assert.False(t, rowExists(t, old))
	assert.True(t, rowExists(t, recent), "inside the retention window it stays for debugging")
	assert.True(t, rowExists(t, unpublished),
		"an unpublished row is the queue itself, retention has nothing to do with it")
}

func TestPrunerDeletesInBatches(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)

	for n := range 5 {
		insertPending(t, TransportKafka, fmt.Sprintf("file-%d", n), 0, now)
	}

	_, err := sharedDB.Pool().Exec(context.Background(),
		`UPDATE outbox SET published_at = $1`, now.Add(-100*time.Hour))
	require.NoError(t, err)

	pruner, err := NewPruner(sharedDB, clock.Fixed{At: now},
		PrunerConfig{Retention: time.Hour, BatchSize: 2})
	require.NoError(t, err)

	// Act
	first, err := pruner.pruneBatch(context.Background(), now.Add(-time.Hour))
	require.NoError(t, err)

	second, err := pruner.pruneBatch(context.Background(), now.Add(-time.Hour))
	require.NoError(t, err)

	third, err := pruner.pruneBatch(context.Background(), now.Add(-time.Hour))
	require.NoError(t, err)

	fourth, err := pruner.pruneBatch(context.Background(), now.Add(-time.Hour))
	require.NoError(t, err)

	// Assert
	assert.Equal(t, []int64{2, 2, 1, 0}, []int64{first, second, third, fourth},
		"one statement per batch keeps the lock and the wal bounded")
}

func TestPrunerDrainsThePastInOnePass(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)

	for n := range 5 {
		insertPending(t, TransportKafka, fmt.Sprintf("file-%d", n), 0, now)
	}

	_, err := sharedDB.Pool().Exec(context.Background(),
		`UPDATE outbox SET published_at = $1`, now.Add(-100*time.Hour))
	require.NoError(t, err)

	pruner, err := NewPruner(sharedDB, clock.Fixed{At: now},
		PrunerConfig{Retention: time.Hour, BatchSize: 2})
	require.NoError(t, err)

	// Act
	removed, err := pruner.Prune(context.Background())
	require.NoError(t, err)

	again, err := pruner.Prune(context.Background())
	require.NoError(t, err)

	// Assert
	assert.Equal(t, int64(5), removed,
		"a pass that stopped after one batch could never keep up with the relay")
	assert.Zero(t, again)

	var left int
	require.NoError(t, sharedDB.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM outbox`).Scan(&left))
	assert.Zero(t, left)
}

func TestPrunerStopsOnACancelledContext(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC().Truncate(time.Microsecond)

	for n := range 3 {
		insertPending(t, TransportKafka, fmt.Sprintf("file-%d", n), 0, now)
	}

	_, err := sharedDB.Pool().Exec(context.Background(),
		`UPDATE outbox SET published_at = $1`, now.Add(-100*time.Hour))
	require.NoError(t, err)

	pruner, err := NewPruner(sharedDB, clock.Fixed{At: now},
		PrunerConfig{Retention: time.Hour, BatchSize: 1})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	removed, err := pruner.Prune(ctx)

	// Assert
	require.Equal(t, context.Canceled, err,
		"the pass checks the context between batches itself, so shutdown does not "+
			"wait for a long backlog and the error arrives unwrapped by the driver")
	assert.Zero(t, removed)

	var left int
	require.NoError(t, sharedDB.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM outbox`).Scan(&left))
	assert.Equal(t, 3, left)
}

func rowExists(t *testing.T, id int64) bool {
	t.Helper()

	var exists bool

	require.NoError(t, sharedDB.Pool().QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM outbox WHERE id = $1)`, id).Scan(&exists))

	return exists
}

func backlogAge(t *testing.T, transport string) float64 {
	t.Helper()

	return testutil.ToFloat64(
		metrics.BacklogAgeForTest(transport))
}

func TestRelayObservesTheBacklogAge(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC()
	relay := testRelay(t, TransportKafka, &recordingSender{}, now, nil)

	// Act: a row that has been waiting for a while.
	_, err := sharedDB.Pool().Exec(t.Context(), `
		INSERT INTO outbox (transport, destination, partition_key, message_type, payload,
			created_at)
		VALUES ($1, 'test.destination', 'key', 'TestMessage', '{}', $2)`,
		TransportKafka, now.Add(-90*time.Second))
	require.NoError(t, err)

	require.NoError(t, relay.ObserveBacklog(t.Context()))
	waiting := backlogAge(t, TransportKafka)

	// The relay catches up and the table drains.
	resetOutbox(t)

	require.NoError(t, relay.ObserveBacklog(t.Context()))
	drained := backlogAge(t, TransportKafka)

	// Assert
	assert.InDelta(t, 90, waiting, 1,
		"this is the only number that shows a relay which stopped publishing")
	assert.InDelta(t, 0, drained, 0.001,
		"a drained outbox must say zero, not keep the age it last saw")
}

func TestRelayReportsTheBacklogOfItsOwnTransportOnly(t *testing.T) {
	// Arrange
	resetOutbox(t)

	now := time.Now().UTC()
	relay := testRelay(t, TransportKafka, &recordingSender{}, now, nil)

	_, err := sharedDB.Pool().Exec(t.Context(), `
		INSERT INTO outbox (transport, destination, partition_key, message_type, payload,
			created_at)
		VALUES ($1, 'test.destination', 'key', 'TestMessage', '{}', $2)`,
		TransportRabbit, now.Add(-time.Hour))
	require.NoError(t, err)

	// Act
	require.NoError(t, relay.ObserveBacklog(t.Context()))

	// Assert
	assert.InDelta(t, 0, backlogAge(t, TransportKafka), 0.001,
		"a stuck rabbit relay must not make the kafka one look stuck too")
}
