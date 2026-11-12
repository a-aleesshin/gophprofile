package pgtx

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, batch *pgx.Batch) pgx.BatchResults
}

type txKey struct{}

type DB struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *DB {
	return &DB{pool: pool}
}

func (d *DB) Pool() *pgxpool.Pool {
	return d.pool
}

func (d *DB) Querier(ctx context.Context) Querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}

	return d.pool
}

func (d *DB) InTransaction(ctx context.Context) bool {
	_, inside := ctx.Value(txKey{}).(pgx.Tx)

	return inside
}

func (d *DB) InTx(ctx context.Context, fn func(context.Context) error) error {
	if outer, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		nested, err := outer.Begin(ctx)
		if err != nil {
			return fmt.Errorf("savepoint: %w", err)
		}

		return settle(ctx, nested, fn)
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}

	return settle(ctx, tx, fn)
}

func settle(ctx context.Context, tx pgx.Tx, fn func(context.Context) error) error {
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}
