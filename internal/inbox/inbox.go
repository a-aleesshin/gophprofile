package inbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

var ErrInvalidEffect = errors.New("invalid inbox effect")

type Inbox struct {
	db *pgtx.DB
}

func NewInbox(db *pgtx.DB) *Inbox {
	return &Inbox{db: db}
}

func (i *Inbox) Seen(
	ctx context.Context,
	consumer string,
	messageID uuid.UUID,
) (bool, error) {
	if err := validate(consumer, messageID); err != nil {
		return false, err
	}

	var exists bool

	if err := i.db.Querier(ctx).QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM processed_messages WHERE consumer = $1 AND message_id = $2
		)`, consumer, messageID).Scan(&exists); err != nil {
		return false, fmt.Errorf("look up message %s for %s: %w", messageID, consumer, err)
	}

	return exists, nil
}

func (i *Inbox) Mark(
	ctx context.Context,
	consumer string,
	messageID uuid.UUID,
) (bool, error) {
	if err := validate(consumer, messageID); err != nil {
		return false, err
	}

	tag, err := i.db.Querier(ctx).Exec(ctx, `
		INSERT INTO processed_messages (consumer, message_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, consumer, messageID)
	if err != nil {
		return false, fmt.Errorf("mark message %s for %s: %w", messageID, consumer, err)
	}

	return tag.RowsAffected() == 1, nil
}

func (i *Inbox) OnceOutsideTransaction(
	ctx context.Context,
	consumer string,
	messageID uuid.UUID,
	effect func(context.Context) error,
) (bool, error) {
	if err := validateEffect(consumer, messageID, effect); err != nil {
		return false, err
	}

	if i.db.InTransaction(ctx) {
		return false, fmt.Errorf(
			"%w: this effect must not run inside a transaction", ErrInvalidEffect)
	}

	seen, err := i.Seen(ctx, consumer, messageID)
	if err != nil {
		return false, err
	}

	if seen {
		return false, nil
	}

	if err := effect(ctx); err != nil {
		return false, err
	}

	return i.Mark(ctx, consumer, messageID)
}

type Failure struct {
	Attempts int
	Dropped  bool
}

func (i *Inbox) RecordFailure(
	ctx context.Context,
	consumer string,
	messageID uuid.UUID,
	cause error,
	maxAttempts int,
) (Failure, error) {
	if err := validate(consumer, messageID); err != nil {
		return Failure{}, err
	}

	switch {
	case cause == nil:
		return Failure{}, fmt.Errorf("%w: cause is required", ErrInvalidEffect)
	case maxAttempts <= 0:
		return Failure{}, fmt.Errorf("%w: max attempts must be positive", ErrInvalidEffect)
	}

	var failure Failure

	if err := i.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO failed_messages AS f (consumer, message_id, attempts, last_error, dropped_at)
		VALUES ($1, $2, 1, $3, CASE WHEN 1 >= $4 THEN now() END)
		ON CONFLICT (consumer, message_id) DO UPDATE
		   SET attempts = f.attempts + 1,
		       last_error = excluded.last_error,
		       last_failed_at = now(),
		       dropped_at = CASE
		           WHEN f.attempts + 1 >= $4 THEN now()
		           ELSE f.dropped_at
		       END
		RETURNING f.attempts, f.dropped_at IS NOT NULL`,
		consumer, messageID, cause.Error(), maxAttempts,
	).Scan(&failure.Attempts, &failure.Dropped); err != nil {
		return Failure{}, fmt.Errorf(
			"record failure of message %s for %s: %w", messageID, consumer, err)
	}

	return failure, nil
}

func validate(consumer string, messageID uuid.UUID) error {
	switch {
	case consumer == "":
		return fmt.Errorf("%w: consumer is required", ErrInvalidEffect)
	case messageID == uuid.Nil:
		return fmt.Errorf("%w: message id is required", ErrInvalidEffect)
	}

	return nil
}

func validateEffect(consumer string, messageID uuid.UUID, effect func(context.Context) error) error {
	if err := validate(consumer, messageID); err != nil {
		return err
	}

	if effect == nil {
		return fmt.Errorf("%w: effect is required", ErrInvalidEffect)
	}

	return nil
}

func (i *Inbox) Once(
	ctx context.Context,
	consumer string,
	messageID uuid.UUID,
	effect func(context.Context) error,
) (bool, error) {
	if err := validateEffect(consumer, messageID, effect); err != nil {
		return false, err
	}

	applied := false

	err := i.db.InTx(ctx, func(ctx context.Context) error {
		tag, err := i.db.Querier(ctx).Exec(ctx, `
			INSERT INTO processed_messages (consumer, message_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, consumer, messageID)
		if err != nil {
			return fmt.Errorf("claim message %s for %s: %w", messageID, consumer, err)
		}

		if tag.RowsAffected() == 0 {
			return nil
		}

		if err := effect(ctx); err != nil {
			return err
		}

		applied = true

		return nil
	})
	if err != nil {
		return false, err
	}

	return applied, nil
}
