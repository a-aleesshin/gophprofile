package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type FailStuckAvatarsCommand struct {
	StaleAfter time.Duration
	Limit      int
}

type FailStuckAvatarsResult struct {
	Failed  int
	Skipped int
}

type FailStuckAvatarsHandler struct {
	avatars staleAvatarFinder
	tx      transactor
	clock   clock
}

func NewFailStuckAvatarsHandler(
	avatars staleAvatarFinder,
	tx transactor,
	clock clock,
) *FailStuckAvatarsHandler {
	return &FailStuckAvatarsHandler{avatars: avatars, tx: tx, clock: clock}
}

func (h *FailStuckAvatarsHandler) Handle(
	ctx context.Context,
	cmd FailStuckAvatarsCommand,
) (FailStuckAvatarsResult, error) {
	switch {
	case cmd.StaleAfter <= 0:
		return FailStuckAvatarsResult{}, fmt.Errorf(
			"%w: stale after must be positive", ErrInvalidCommand)
	case cmd.Limit <= 0:
		return FailStuckAvatarsResult{}, fmt.Errorf(
			"%w: limit must be positive", ErrInvalidCommand)
	}

	now := h.clock.Now()

	avatars, err := h.avatars.StaleByStatus(ctx,
		[]domain.AvatarStatus{domain.AvatarStatusProcessing},
		now.Add(-cmd.StaleAfter), cmd.Limit)
	if err != nil {
		return FailStuckAvatarsResult{}, err
	}

	var result FailStuckAvatarsResult

	for _, avatar := range avatars {
		if failErr := avatar.MarkFailed(ReasonProcessingStuck, now); failErr != nil {
			return FailStuckAvatarsResult{}, failErr
		}

		saveErr := h.tx.InTx(ctx, func(ctx context.Context) error {
			return h.avatars.Save(ctx, &avatar)
		})

		switch {
		case errors.Is(saveErr, ErrStaleVersion):
			result.Skipped++
		case saveErr != nil:
			return FailStuckAvatarsResult{}, saveErr
		default:
			result.Failed++
		}
	}

	return result, nil
}
