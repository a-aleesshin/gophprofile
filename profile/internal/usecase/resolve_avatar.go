package usecase

import (
	"context"
	"fmt"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type ResolveAvatarCommand struct {
	AvatarID domain.AvatarID
	Size     string
}

type ResolveResult struct {
	URL         string
	Placeholder bool
}

type ResolveAvatarHandler struct {
	avatars avatarViewLoader
	urls    contentURLBuilder
}

func NewResolveAvatarHandler(
	avatars avatarViewLoader,
	urls contentURLBuilder,
) *ResolveAvatarHandler {
	return &ResolveAvatarHandler{avatars: avatars, urls: urls}
}

func (h *ResolveAvatarHandler) Handle(
	ctx context.Context,
	cmd ResolveAvatarCommand,
) (ResolveResult, error) {
	if cmd.AvatarID.IsZero() {
		return ResolveResult{}, fmt.Errorf("%w: avatar id is required", ErrInvalidCommand)
	}

	view, err := h.avatars.ViewByID(ctx, cmd.AvatarID)
	if err != nil {
		return ResolveResult{}, err
	}

	url, err := resolveVariant(h.urls, view, cmd.Size)
	if err != nil {
		return ResolveResult{}, err
	}

	return ResolveResult{URL: url}, nil
}
