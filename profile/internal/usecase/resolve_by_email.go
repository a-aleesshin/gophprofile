package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type profileByEmailLoader interface {
	ByEmailHash(ctx context.Context, hash domain.EmailHash) (domain.Profile, error)
	ViewByID(ctx context.Context, id domain.AvatarID) (AvatarView, error)
}

type ResolveByEmailCommand struct {
	EmailHash domain.EmailHash
	Size      string
}

type ResolveByEmailHandler struct {
	source profileByEmailLoader
	urls   contentURLBuilder
}

func NewResolveByEmailHandler(
	source profileByEmailLoader,
	urls contentURLBuilder,
) *ResolveByEmailHandler {
	return &ResolveByEmailHandler{source: source, urls: urls}
}

func (h *ResolveByEmailHandler) Handle(
	ctx context.Context,
	cmd ResolveByEmailCommand,
) (ResolveResult, error) {
	if cmd.EmailHash.IsZero() {
		return ResolveResult{}, fmt.Errorf("%w: email hash is required", ErrInvalidCommand)
	}

	profile, err := h.source.ByEmailHash(ctx, cmd.EmailHash)

	if errors.Is(err, domain.ErrProfileNotFound) {
		return ResolveResult{URL: h.urls.PlaceholderURL(), Placeholder: true}, nil
	}

	if err != nil {
		return ResolveResult{}, err
	}

	if profile.CurrentAvatarID().IsZero() {
		return ResolveResult{URL: h.urls.PlaceholderURL(), Placeholder: true}, nil
	}

	view, err := h.source.ViewByID(ctx, profile.CurrentAvatarID())

	if errors.Is(err, domain.ErrAvatarNotFound) {
		return ResolveResult{URL: h.urls.PlaceholderURL(), Placeholder: true}, nil
	}

	if err != nil {
		return ResolveResult{}, err
	}

	url, err := resolveVariant(h.urls, view, cmd.Size)
	if err != nil {
		return ResolveResult{URL: h.urls.PlaceholderURL(), Placeholder: true}, nil
	}

	return ResolveResult{URL: url}, nil
}
