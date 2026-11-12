package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type profileEmailBinder interface {
	ByUserID(ctx context.Context, userID domain.UserID) (domain.Profile, error)
	Create(ctx context.Context, profile *domain.Profile) error
	Save(ctx context.Context, profile *domain.Profile) error
}

type BindEmailCommand struct {
	UserID domain.UserID
	Email  domain.Email
}

type BindEmailResult struct {
	EmailHash domain.EmailHash
}

type BindEmailHandler struct {
	profiles profileEmailBinder
	tx       transactor
	clock    clock
}

func NewBindEmailHandler(
	profiles profileEmailBinder,
	tx transactor,
	clock clock,
) *BindEmailHandler {
	return &BindEmailHandler{profiles: profiles, tx: tx, clock: clock}
}

func (h *BindEmailHandler) Handle(
	ctx context.Context,
	cmd BindEmailCommand,
) (BindEmailResult, error) {
	switch {
	case cmd.UserID.IsZero():
		return BindEmailResult{}, fmt.Errorf("%w: user id is required", ErrInvalidCommand)
	case cmd.Email.IsZero():
		return BindEmailResult{}, fmt.Errorf("%w: email is required", ErrInvalidCommand)
	}

	now := h.clock.Now()

	profile, err := h.profiles.ByUserID(ctx, cmd.UserID)

	created := false

	if errors.Is(err, domain.ErrProfileNotFound) {
		profile, err = domain.NewProfile(cmd.UserID, now)
		if err != nil {
			return BindEmailResult{}, err
		}

		created = true
	} else if err != nil {
		return BindEmailResult{}, err
	}

	if err := profile.SetEmail(cmd.Email, now); err != nil {
		return BindEmailResult{}, err
	}

	if err := h.tx.InTx(ctx, func(ctx context.Context) error {
		if created {
			return h.profiles.Create(ctx, &profile)
		}

		return h.profiles.Save(ctx, &profile)
	}); err != nil {
		return BindEmailResult{}, err
	}

	return BindEmailResult{EmailHash: profile.EmailHash()}, nil
}
