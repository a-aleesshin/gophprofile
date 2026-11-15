package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type currentAvatarLoader interface {
	ByUserID(ctx context.Context, userID domain.UserID) (domain.Profile, error)
	ViewByID(ctx context.Context, id domain.AvatarID) (AvatarView, error)
}

type ResolveCurrentAvatarCommand struct {
	UserID domain.UserID
	Size   string
}

type ResolveCurrentAvatarHandler struct {
	source currentAvatarLoader
	urls   contentURLBuilder
}

func NewResolveCurrentAvatarHandler(
	source currentAvatarLoader,
	urls contentURLBuilder,
) *ResolveCurrentAvatarHandler {
	return &ResolveCurrentAvatarHandler{source: source, urls: urls}
}

func (h *ResolveCurrentAvatarHandler) Handle(
	ctx context.Context,
	cmd ResolveCurrentAvatarCommand,
) (ResolveResult, error) {
	if cmd.UserID.IsZero() {
		return ResolveResult{}, fmt.Errorf("%w: user id is required", ErrInvalidCommand)
	}

	profile, err := h.source.ByUserID(ctx, cmd.UserID)

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
