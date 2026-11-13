//go:build integration

package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
	"github.com/a-aleesshin/gophprofile/file/migrations"
	"github.com/a-aleesshin/gophprofile/internal/clock"
	"github.com/a-aleesshin/gophprofile/internal/inbox"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
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

type storedRow struct {
	Transport    string
	Destination  string
	PartitionKey *string
	MessageType  string
	Payload      []byte
	PublishedAt  *time.Time
	Attempts     int32
}

func readRows(t *testing.T, messageType string) []storedRow {
	t.Helper()

	rows, err := sharedDB.Pool().Query(t.Context(), `
		SELECT transport, destination, partition_key, message_type, payload,
		       published_at, attempts
		  FROM outbox WHERE message_type = $1 ORDER BY id`, messageType)
	require.NoError(t, err)

	defer rows.Close()

	out := make([]storedRow, 0, 2)

	for rows.Next() {
		var row storedRow

		require.NoError(t, rows.Scan(&row.Transport, &row.Destination, &row.PartitionKey,
			&row.MessageType, &row.Payload, &row.PublishedAt, &row.Attempts))

		out = append(out, row)
	}

	require.NoError(t, rows.Err())

	return out
}

func newPublisher() *Publisher {
	return NewPublisher(outbox.NewWriter(
		sharedDB, clock.Fixed{At: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)}))
}

func testFileID(t *testing.T) domain.FileID {
	t.Helper()

	id, err := domain.NewFileID()
	require.NoError(t, err)

	return id
}

func TestPublisher_EnqueueProcessFileGoesToRabbit(t *testing.T) {
	t.Parallel()

	fileID := testFileID(t)

	uploadID, err := domain.NewUploadID()
	require.NoError(t, err)

	staging, err := domain.NewStagingLocation(uploadID)
	require.NoError(t, err)

	small, err := domain.NewVariantName("100x100")
	require.NoError(t, err)

	require.NoError(t, newPublisher().EnqueueProcessFile(t.Context(), usecase.ProcessFileCommand{
		FileID:     fileID,
		StagingKey: staging,
		Variants:   []domain.VariantName{small},
	}))

	rows := readRows(t, TypeProcessFile)

	var found *storedRow

	for i := range rows {
		var payload struct {
			Data processFileData `json:"data"`
		}

		require.NoError(t, json.Unmarshal(rows[i].Payload, &payload))

		if payload.Data.FileID == fileID.String() {
			found = &rows[i]
		}
	}

	require.NotNil(t, found)

	assert.Equal(t, outbox.TransportRabbit, found.Transport,
		"a command carries an instruction, so it goes to the queue with acks and retries")
	assert.Equal(t, DestinationProcessFile, found.Destination)
	require.NotNil(t, found.PartitionKey)
	assert.Equal(t, fileID.String(), *found.PartitionKey,
		"rabbit ignores it for routing, but the relay orders commands about one file by it")
	assert.Nil(t, found.PublishedAt, "the relay has not seen it yet")
	assert.Zero(t, found.Attempts)

	var envelope struct {
		MessageID  string          `json:"message_id"`
		Type       string          `json:"type"`
		OccurredAt time.Time       `json:"occurred_at"`
		Data       processFileData `json:"data"`
	}

	require.NoError(t, json.Unmarshal(found.Payload, &envelope))

	assert.NotEmpty(t, envelope.MessageID,
		"the id is generated once and stored, so a redelivery is recognised by the inbox")
	assert.Equal(t, TypeProcessFile, envelope.Type)
	assert.Equal(t, staging.String(), envelope.Data.StagingKey)
	assert.Equal(t, []string{"100x100"}, envelope.Data.Variants)
}

func TestPublisher_PublishFactGoesToKafkaKeyedByFile(t *testing.T) {
	t.Parallel()

	fileID := testFileID(t)

	owner, err := domain.NewOwnerRef("user-1")
	require.NoError(t, err)

	require.NoError(t, newPublisher().PublishFileRejected(t.Context(), usecase.FileRejectedFact{
		FileID:   fileID,
		OwnerRef: owner,
		Reason:   domain.RejectReasonDecodeFailed,
	}))

	rows := readRows(t, TypeFileRejected)

	var found *storedRow

	for i := range rows {
		if rows[i].PartitionKey != nil && *rows[i].PartitionKey == fileID.String() {
			found = &rows[i]
		}
	}

	require.NotNil(t, found)

	assert.Equal(t, outbox.TransportKafka, found.Transport,
		"a fact has no single executor, so it goes to the log")
	assert.Equal(t, DestinationFileEvents, found.Destination)
	require.NotNil(t, found.PartitionKey)
	assert.Equal(t, fileID.String(), *found.PartitionKey,
		"keying by file id is what keeps FileProcessed ahead of FileDeleted in one partition")

	var envelope struct {
		Data fileRejectedData `json:"data"`
	}

	require.NoError(t, json.Unmarshal(found.Payload, &envelope))
	assert.Equal(t, "decode_failed", envelope.Data.Reason)
}

func TestPublisher_JoinsTheCallersTransaction(t *testing.T) {
	t.Parallel()

	fileID := testFileID(t)

	owner, err := domain.NewOwnerRef("user-2")
	require.NoError(t, err)

	boom := errors.New("business rule said no")

	err = sharedDB.InTx(t.Context(), func(ctx context.Context) error {
		if err := newPublisher().PublishFileDeleted(ctx, usecase.FileDeletedFact{
			FileID:   fileID,
			OwnerRef: owner,
		}); err != nil {
			return err
		}

		return boom
	})
	require.ErrorIs(t, err, boom)

	for _, row := range readRows(t, TypeFileDeleted) {
		var envelope struct {
			Data fileDeletedData `json:"data"`
		}

		require.NoError(t, json.Unmarshal(row.Payload, &envelope))
		assert.NotEqual(t, fileID.String(), envelope.Data.FileID,
			"the whole point of the outbox is that the row and the state commit together")
	}
}

func TestPublisher_SatisfiesTheUseCasePorts(t *testing.T) {
	t.Parallel()

	writer := newPublisher()

	assert.NotNil(t, usecase.NewDeleteObjectsHandler(
		nil, nil, writer, sharedDB, clock.System{}))
	assert.NotNil(t, usecase.NewReapStaleFilesHandler(
		nil, nil, nil, writer, writer, sharedDB, clock.System{}))
	assert.NotNil(t, usecase.NewCompleteUploadHandler(
		nil, nil, nil, nil, writer, sharedDB, clock.System{}))
	assert.NotNil(t, usecase.NewProcessFileHandler(
		nil, nil, nil, nil, writer, sharedDB, clock.System{}))
	assert.NotNil(t, usecase.NewDeleteFileHandler(
		nil, nil, writer, sharedDB, clock.System{}))
}

func TestWriter_RejectsIncompleteMessages(t *testing.T) {
	t.Parallel()

	writer := outbox.NewWriter(sharedDB, clock.System{})

	tests := []struct {
		name    string
		message outbox.Message
	}{
		{name: "unknown transport", message: outbox.Message{
			Transport: "carrier pigeon", Destination: "x", Type: "T"}},
		{name: "no destination", message: outbox.Message{
			Transport: outbox.TransportRabbit, Type: "T"}},
		{name: "no type", message: outbox.Message{
			Transport: outbox.TransportRabbit, Destination: "x"}},
		{name: "kafka without a partition key", message: outbox.Message{
			Transport: outbox.TransportKafka, Destination: "x", Type: "T"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := writer.Write(t.Context(), tt.message)
			require.ErrorIs(t, err, outbox.ErrInvalidMessage,
				"a typo in a destination must fail here, not sit until the relay picks it up")
		})
	}
}

type factDelivery struct {
	body    []byte
	acked   int
	retried int
	dropped int
	reason  string
}

func (d *factDelivery) Body() []byte { return d.body }

func (d *factDelivery) Ack(context.Context) error { d.acked++; return nil }

func (d *factDelivery) Retry(context.Context) error { d.retried++; return nil }

func (d *factDelivery) Drop(_ context.Context, reason string) error {
	d.dropped++
	d.reason = reason

	return nil
}

type oneFactBatch struct {
	deliveries []inbox.Delivery
	fetched    bool
}

func (s *oneFactBatch) Fetch(context.Context) ([]inbox.Delivery, error) {
	if s.fetched {
		return nil, nil
	}

	s.fetched = true

	return s.deliveries, nil
}

type probeDeleter struct {
	db       *pgtx.DB
	inside   bool
	observed bool
}

func (p *probeDeleter) Handle(
	ctx context.Context,
	_ usecase.DeleteFileCommand,
) (usecase.DeleteFileResult, error) {
	p.inside = p.db.InTransaction(ctx)
	p.observed = true

	return usecase.DeleteFileResult{}, nil
}

func TestAvatarDeletedIsAppliedInsideATransaction(t *testing.T) {
	// Arrange
	deleter := &probeDeleter{db: sharedDB}

	body, err := json.Marshal(outbox.Envelope{
		MessageID:  uuid.New(),
		Type:       TypeAvatarDeleted,
		OccurredAt: time.Now().UTC(),
		Data: json.RawMessage(`{
			"avatar_id": "0193abcd-ef01-7a23-8b45-0123456789ac",
			"user_id": "user-1",
			"file_id": "0193abcd-ef01-7a23-8b45-0123456789ab"
		}`),
	})
	require.NoError(t, err)

	delivery := &factDelivery{body: body}

	consumer, err := inbox.NewConsumer("file.worker.facts",
		&oneFactBatch{deliveries: []inbox.Delivery{delivery}},
		inbox.NewInbox(sharedDB), FactHandlers(deleter))
	require.NoError(t, err)

	// Act
	stats, err := consumer.Consume(t.Context())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, inbox.Stats{Handled: 1}, stats)
	assert.Equal(t, 1, delivery.acked)

	require.True(t, deleter.observed, "the handler must actually have run")
	assert.True(t, deleter.inside,
		"marking the file deleting, queueing the cleanup and the processed mark "+
			"commit together or not at all")
}

func TestARedeliveredAvatarDeletedIsNotAppliedTwice(t *testing.T) {
	// Arrange
	messageID := uuid.New()

	payload := func() []byte {
		body, err := json.Marshal(outbox.Envelope{
			MessageID:  messageID,
			Type:       TypeAvatarDeleted,
			OccurredAt: time.Now().UTC(),
			Data: json.RawMessage(`{
				"avatar_id": "0193abcd-ef01-7a23-8b45-0123456789ac",
				"user_id": "user-1",
				"file_id": "0193abcd-ef01-7a23-8b45-0123456789ab"
			}`),
		})
		require.NoError(t, err)

		return body
	}

	deleter := &probeDeleter{db: sharedDB}
	handlers := FactHandlers(deleter)

	newConsumer := func(delivery inbox.Delivery) *inbox.Consumer {
		consumer, err := inbox.NewConsumer("file.worker.facts",
			&oneFactBatch{deliveries: []inbox.Delivery{delivery}},
			inbox.NewInbox(sharedDB), handlers)
		require.NoError(t, err)

		return consumer
	}

	first, second := &factDelivery{body: payload()}, &factDelivery{body: payload()}

	// Act
	_, err := newConsumer(first).Consume(t.Context())
	require.NoError(t, err)

	deleter.observed = false

	stats, err := newConsumer(second).Consume(t.Context())
	require.NoError(t, err)

	// Assert
	assert.Equal(t, inbox.Stats{Duplicate: 1}, stats)
	assert.False(t, deleter.observed,
		"kafka redelivers on rebalance, and deleting a file twice is not harmless")
	assert.Equal(t, 1, second.acked)
}
