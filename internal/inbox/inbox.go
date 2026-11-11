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

func (i *Inbox) Once(
	ctx context.Context,
	consumer string,
	messageID uuid.UUID,
	effect func(context.Context) error,
) (bool, error) {
	switch {
	case consumer == "":
		return false, fmt.Errorf("%w: consumer is required", ErrInvalidEffect)
	case messageID == uuid.Nil:
		return false, fmt.Errorf("%w: message id is required", ErrInvalidEffect)
	case effect == nil:
		return false, fmt.Errorf("%w: effect is required", ErrInvalidEffect)
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
