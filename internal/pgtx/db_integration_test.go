//go:build integration

package pgtx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var sharedDB *DB

func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("pgtx"),
		tcpostgres.WithUsername("pgtx"),
		tcpostgres.WithPassword("pgtx"),
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

	if _, err := pool.Exec(ctx, `CREATE TABLE marks (name TEXT PRIMARY KEY)`); err != nil {
		fmt.Fprintf(os.Stderr, "create table: %v\n", err)
		os.Exit(1)
	}

	sharedDB = New(pool)

	code := m.Run()

	pool.Close()

	if err := container.Terminate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "terminate postgres: %v\n", err)
	}

	os.Exit(code)
}

func mark(t *testing.T, ctx context.Context, name string) error {
	t.Helper()

	_, err := sharedDB.Querier(ctx).Exec(ctx, `INSERT INTO marks (name) VALUES ($1)`, name)

	return err
}

func marks(t *testing.T) []string {
	t.Helper()

	rows, err := sharedDB.Pool().Query(context.Background(),
		`SELECT name FROM marks ORDER BY name`)
	require.NoError(t, err)

	defer rows.Close()

	var names []string

	for rows.Next() {
		var name string

		require.NoError(t, rows.Scan(&name))

		names = append(names, name)
	}

	require.NoError(t, rows.Err())

	return names
}

func reset(t *testing.T) {
	t.Helper()

	_, err := sharedDB.Pool().Exec(context.Background(), `TRUNCATE marks`)
	require.NoError(t, err)
}

func TestNestedFailureLeavesTheOuterTransactionUsable(t *testing.T) {
	// Arrange
	reset(t)

	boom := errors.New("the inner step gave up")

	// Act
	err := sharedDB.InTx(t.Context(), func(ctx context.Context) error {
		if err := mark(t, ctx, "outer"); err != nil {
			return err
		}

		inner := sharedDB.InTx(ctx, func(ctx context.Context) error {
			if err := mark(t, ctx, "inner"); err != nil {
				return err
			}

			return boom
		})

		require.ErrorIs(t, inner, boom)

		return mark(t, ctx, "after")
	})

	// Assert
	require.NoError(t, err,
		"a nested failure used to abort the whole transaction and make every later "+
			"statement fail with 25P02")
	assert.Equal(t, []string{"after", "outer"}, marks(t),
		"the savepoint takes back the nested work and nothing else")
}

func TestNestedSQLErrorDoesNotPoisonTheOuterTransaction(t *testing.T) {
	// Arrange
	reset(t)

	require.NoError(t, sharedDB.InTx(t.Context(), func(ctx context.Context) error {
		return mark(t, ctx, "taken")
	}))

	// Act
	err := sharedDB.InTx(t.Context(), func(ctx context.Context) error {
		conflict := sharedDB.InTx(ctx, func(ctx context.Context) error {
			return mark(t, ctx, "taken")
		})
		require.Error(t, conflict, "the primary key is already there")

		return mark(t, ctx, "survivor")
	})

	// Assert
	require.NoError(t, err,
		"a real sql error is the case the join could not recover from at all")
	assert.Equal(t, []string{"survivor", "taken"}, marks(t))
}

func TestNestedSuccessJoinsTheOuterTransaction(t *testing.T) {
	// Arrange
	reset(t)

	boom := errors.New("the outer step gave up")

	// Act
	err := sharedDB.InTx(t.Context(), func(ctx context.Context) error {
		if err := sharedDB.InTx(ctx, func(ctx context.Context) error {
			return mark(t, ctx, "inner")
		}); err != nil {
			return err
		}

		return boom
	})

	// Assert
	require.ErrorIs(t, err, boom)
	assert.Empty(t, marks(t),
		"a released savepoint is not a commit, so the outer rollback still takes it back")
}

func TestNestedTransactionStaysVisibleToTheGuard(t *testing.T) {
	// Arrange
	reset(t)

	// Act
	var insideNested bool

	require.NoError(t, sharedDB.InTx(t.Context(), func(ctx context.Context) error {
		return sharedDB.InTx(ctx, func(ctx context.Context) error {
			insideNested = sharedDB.InTransaction(ctx)

			return nil
		})
	}))

	// Assert
	assert.True(t, insideNested,
		"the io guards read this, and a savepoint must not look like no transaction")
}
