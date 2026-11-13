//go:build integration

package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/a-aleesshin/gophprofile/internal/clock"
	"github.com/a-aleesshin/gophprofile/internal/inbox"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
	"github.com/a-aleesshin/gophprofile/profile/migrations"
)

var sharedDB *pgtx.DB

func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("profile"),
		tcpostgres.WithUsername("profile"),
		tcpostgres.WithPassword("profile"),
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

type outboxRow struct {
	Transport    string
	Destination  string
	PartitionKey *string
	MessageType  string
	Payload      []byte
}

func testPublisher(t *testing.T) *Publisher {
	t.Helper()

	return NewPublisher(outbox.NewWriter(sharedDB, clock.System{}), sharedDB)
}

func testFact(t *testing.T) usecase.AvatarDeletedFact {
	t.Helper()

	avatarID, err := domain.NewAvatarID()
	require.NoError(t, err)

	userID, err := domain.NewUserID("user-" + uuid.NewString())
	require.NoError(t, err)

	fileID, err := domain.ParseFileRef(uuid.Must(uuid.NewV7()).String())
	require.NoError(t, err)

	return usecase.AvatarDeletedFact{AvatarID: avatarID, UserID: userID, FileID: fileID}
}

func rowsFor(t *testing.T, messageType, key string) []outboxRow {
	t.Helper()

	rows, err := sharedDB.Pool().Query(t.Context(), `
		SELECT transport, destination, partition_key, message_type, payload
		  FROM outbox
		 WHERE message_type = $1 AND partition_key = $2`, messageType, key)
	require.NoError(t, err)

	defer rows.Close()

	var found []outboxRow

	for rows.Next() {
		var row outboxRow

		require.NoError(t, rows.Scan(
			&row.Transport, &row.Destination, &row.PartitionKey,
			&row.MessageType, &row.Payload,
		))

		found = append(found, row)
	}

	require.NoError(t, rows.Err())

	return found
}

func TestPublisherWritesTheFactTheContractPromises(t *testing.T) {
	t.Parallel()

	// Arrange
	publisher := testPublisher(t)
	fact := testFact(t)

	// Act
	require.NoError(t, sharedDB.InTx(t.Context(), func(ctx context.Context) error {
		return publisher.PublishAvatarDeleted(ctx, fact)
	}))

	// Assert
	rows := rowsFor(t, TypeAvatarDeleted, fact.UserID.String())
	require.Len(t, rows, 1)

	assert.Equal(t, outbox.TransportKafka, rows[0].Transport)
	assert.Equal(t, "gophprofile.profile.v1", rows[0].Destination)

	require.NotNil(t, rows[0].PartitionKey)
	assert.Equal(t, fact.UserID.String(), *rows[0].PartitionKey,
		"the key is the user, so everything about one profile keeps its order")

	var envelope outbox.Envelope

	require.NoError(t, json.Unmarshal(rows[0].Payload, &envelope))
	assert.Equal(t, TypeAvatarDeleted, envelope.Type)
	assert.NotEqual(t, uuid.Nil, envelope.MessageID, "the consumer dedupes by this id")
	assert.WithinDuration(t, time.Now(), envelope.OccurredAt, time.Minute)

	assert.JSONEq(t, fmt.Sprintf(
		`{"avatar_id": %q, "user_id": %q, "file_id": %q}`,
		fact.AvatarID.String(), fact.UserID.String(), fact.FileID.String(),
	), string(envelope.Data), "file reads exactly these three fields, and no storage keys")
}

func TestPublisherRollsBackWithItsTransaction(t *testing.T) {
	t.Parallel()

	// Arrange
	publisher := testPublisher(t)
	fact := testFact(t)
	failed := fmt.Errorf("the avatar could not be saved")

	// Act
	err := sharedDB.InTx(t.Context(), func(ctx context.Context) error {
		if publishErr := publisher.PublishAvatarDeleted(ctx, fact); publishErr != nil {
			return publishErr
		}

		return failed
	})

	// Assert
	require.ErrorIs(t, err, failed)
	assert.Empty(t, rowsFor(t, TypeAvatarDeleted, fact.UserID.String()),
		"a fact about a deletion that did not happen must not reach kafka")
}

func TestPublisherRefusesToWriteOutsideATransaction(t *testing.T) {
	t.Parallel()

	// Arrange
	publisher := testPublisher(t)
	fact := testFact(t)

	// Act
	err := publisher.PublishAvatarDeleted(t.Context(), fact)

	// Assert
	require.ErrorIs(t, err, ErrOutsideTransaction,
		"outside a transaction the row commits on its own, which is the one thing "+
			"the outbox pattern exists to prevent")
	assert.Empty(t, rowsFor(t, TypeAvatarDeleted, fact.UserID.String()))
}

func TestPublisherRefusesAnIncompleteFact(t *testing.T) {
	t.Parallel()

	publisher := testPublisher(t)

	tests := []struct {
		name   string
		mutate func(*usecase.AvatarDeletedFact)
	}{
		{
			name:   "no avatar",
			mutate: func(f *usecase.AvatarDeletedFact) { f.AvatarID = domain.AvatarID{} },
		},
		{
			name:   "no user",
			mutate: func(f *usecase.AvatarDeletedFact) { f.UserID = domain.UserID{} },
		},
		{
			name:   "no file",
			mutate: func(f *usecase.AvatarDeletedFact) { f.FileID = domain.FileRef{} },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			fact := testFact(t)
			test.mutate(&fact)

			// Act
			err := sharedDB.InTx(t.Context(), func(ctx context.Context) error {
				return publisher.PublishAvatarDeleted(ctx, fact)
			})

			// Assert
			require.ErrorIs(t, err, ErrInvalidFact,
				"file cannot delete anything from a fact with a hole in it")
		})
	}
}

func TestPublisherFitsThePortItWasWrittenFor(t *testing.T) {
	t.Parallel()

	// The port is unexported, so handing the publisher to the handler that
	// declares it is the only way to assert the fit, and it fails to compile.
	assert.NotNil(t, usecase.NewDeleteAvatarHandler(nil, nil, testPublisher(t), nil, nil))
}

type recordedDelivery struct {
	body    []byte
	acked   int
	retried int
	dropped int
	reason  string
}

func (d *recordedDelivery) Body() []byte { return d.body }

func (d *recordedDelivery) Ack(context.Context) error { d.acked++; return nil }

func (d *recordedDelivery) Retry(context.Context) error { d.retried++; return nil }

func (d *recordedDelivery) Drop(_ context.Context, reason string) error {
	d.dropped++
	d.reason = reason

	return nil
}

type oneBatch struct {
	deliveries []inbox.Delivery
	fetched    bool
}

func (s *oneBatch) Fetch(context.Context) ([]inbox.Delivery, error) {
	if s.fetched {
		return nil, nil
	}

	s.fetched = true

	return s.deliveries, nil
}

type transactionProbeApplier struct {
	db       *pgtx.DB
	inside   bool
	observed bool
}

func (a *transactionProbeApplier) Handle(
	ctx context.Context,
	_ usecase.FileProcessedFact,
) (usecase.ApplyFactResult, error) {
	a.inside = a.db.InTransaction(ctx)
	a.observed = true

	return usecase.ApplyFactResult{}, nil
}

func (a *transactionProbeApplier) rejected(
	ctx context.Context,
	_ usecase.FileRejectedFact,
) (usecase.ApplyFactResult, error) {
	a.inside = a.db.InTransaction(ctx)
	a.observed = true

	return usecase.ApplyFactResult{}, nil
}

type rejectedFunc func(context.Context, usecase.FileRejectedFact) (usecase.ApplyFactResult, error)

func (f rejectedFunc) Handle(
	ctx context.Context,
	fact usecase.FileRejectedFact,
) (usecase.ApplyFactResult, error) {
	return f(ctx, fact)
}

type deletedNoop struct{}

func (deletedNoop) Handle(
	context.Context,
	usecase.FileDeletedFact,
) (usecase.ApplyFactResult, error) {
	return usecase.ApplyFactResult{}, nil
}

func TestFactsAreAppliedInsideATransaction(t *testing.T) {
	// Arrange
	applier := &transactionProbeApplier{db: sharedDB}

	body, err := json.Marshal(outbox.Envelope{
		MessageID:  uuid.New(),
		Type:       TypeFileProcessed,
		OccurredAt: time.Now().UTC(),
		Data: json.RawMessage(`{
			"file_id": "0193abcd-ef01-7a23-8b45-0123456789ab",
			"original_name": "avatar.jpg",
			"mime_type": "image/jpeg",
			"size_bytes": 2048,
			"width": 1920,
			"height": 1080,
			"variants": [
				{"name": "original", "ext": "jpg", "mime_type": "image/jpeg",
				 "width": 1920, "height": 1080},
				{"name": "300x300", "ext": "jpg", "mime_type": "image/jpeg",
				 "width": 300, "height": 300}
			]
		}`),
	})
	require.NoError(t, err)

	delivery := &recordedDelivery{body: body}

	consumer, err := inbox.NewConsumer("profile.worker",
		&oneBatch{deliveries: []inbox.Delivery{delivery}},
		inbox.NewInbox(sharedDB),
		FactHandlers(applier, rejectedFunc(applier.rejected), deletedNoop{}))
	require.NoError(t, err)

	// Act
	stats, err := consumer.Consume(t.Context())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, inbox.Stats{Handled: 1}, stats)
	assert.Equal(t, 1, delivery.acked)

	require.True(t, applier.observed, "the handler must actually have run")
	assert.True(t, applier.inside,
		"the avatar row and the processed mark commit together or not at all")
}

func TestARedeliveredFactIsNotAppliedTwice(t *testing.T) {
	// Arrange
	messageID := uuid.New()

	payload := func() []byte {
		body, err := json.Marshal(outbox.Envelope{
			MessageID:  messageID,
			Type:       TypeFileRejected,
			OccurredAt: time.Now().UTC(),
			Data: json.RawMessage(
				`{"file_id": "0193abcd-ef01-7a23-8b45-0123456789ab", "reason": "decode_failed"}`),
		})
		require.NoError(t, err)

		return body
	}

	applied := 0

	handlers := FactHandlers(
		&transactionProbeApplier{db: sharedDB},
		rejectedFunc(func(
			context.Context, usecase.FileRejectedFact,
		) (usecase.ApplyFactResult, error) {
			applied++

			return usecase.ApplyFactResult{}, nil
		}),
		deletedNoop{})

	newConsumer := func(delivery inbox.Delivery) *inbox.Consumer {
		consumer, err := inbox.NewConsumer("profile.worker",
			&oneBatch{deliveries: []inbox.Delivery{delivery}},
			inbox.NewInbox(sharedDB), handlers)
		require.NoError(t, err)

		return consumer
	}

	first, second := &recordedDelivery{body: payload()}, &recordedDelivery{body: payload()}

	// Act
	_, err := newConsumer(first).Consume(t.Context())
	require.NoError(t, err)

	stats, err := newConsumer(second).Consume(t.Context())
	require.NoError(t, err)

	// Assert
	assert.Equal(t, inbox.Stats{Duplicate: 1}, stats)
	assert.Equal(t, 1, applied, "kafka redelivers on rebalance, the effect must not")
	assert.Equal(t, 1, second.acked, "a duplicate is settled, not retried")
}

func TestAFactWeCannotUseIsQuarantined(t *testing.T) {
	// Arrange
	body, err := json.Marshal(outbox.Envelope{
		MessageID:  uuid.New(),
		Type:       TypeFileRejected,
		OccurredAt: time.Now().UTC(),
		Data:       json.RawMessage(`{"file_id": "not-a-uuid", "reason": "decode_failed"}`),
	})
	require.NoError(t, err)

	delivery := &recordedDelivery{body: body}

	consumer, err := inbox.NewConsumer("profile.worker",
		&oneBatch{deliveries: []inbox.Delivery{delivery}},
		inbox.NewInbox(sharedDB),
		FactHandlers(&transactionProbeApplier{db: sharedDB},
			rejectedFunc(func(
				context.Context, usecase.FileRejectedFact,
			) (usecase.ApplyFactResult, error) {
				t.Fatal("the handler must not be reached")

				return usecase.ApplyFactResult{}, nil
			}), deletedNoop{}))
	require.NoError(t, err)

	// Act
	stats, err := consumer.Consume(t.Context())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, inbox.Stats{Dropped: 1}, stats)
	assert.Equal(t, 1, delivery.dropped)
	assert.Contains(t, delivery.reason, "file id")
	assert.Zero(t, delivery.retried, "retrying a malformed uuid would spin forever")
}
