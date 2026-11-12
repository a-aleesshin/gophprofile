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
