package usecase

import (
	"context"
	"fmt"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type avatarViewLoader interface {
	ViewByID(ctx context.Context, id domain.AvatarID) (AvatarView, error)
}

type GetAvatarCommand struct {
	AvatarID domain.AvatarID
	UserID   domain.UserID
}

type GetAvatarResult struct {
	Avatar AvatarView
	URLs   map[string]string
}

type GetAvatarHandler struct {
	avatars avatarViewLoader
	urls    contentURLBuilder
}

func NewGetAvatarHandler(avatars avatarViewLoader, urls contentURLBuilder) *GetAvatarHandler {
	return &GetAvatarHandler{avatars: avatars, urls: urls}
}

func (h *GetAvatarHandler) Handle(
	ctx context.Context,
	cmd GetAvatarCommand,
) (GetAvatarResult, error) {
	if cmd.AvatarID.IsZero() {
		return GetAvatarResult{}, fmt.Errorf("%w: avatar id is required", ErrInvalidCommand)
	}

	view, err := h.avatars.ViewByID(ctx, cmd.AvatarID)
	if err != nil {
		return GetAvatarResult{}, err
	}

	if !cmd.UserID.IsZero() && !view.UserID.Equal(cmd.UserID) {
		return GetAvatarResult{}, fmt.Errorf("%w: %s", ErrForbidden, cmd.AvatarID.String())
	}

	urls := make(map[string]string, len(view.Variants))

	for _, variant := range view.Variants {
		url, err := h.urls.VariantURL(view.FileID, variant.Name, variant.Ext)
		if err != nil {
			return GetAvatarResult{}, err
		}

		urls[variant.Name] = url
	}

	return GetAvatarResult{Avatar: view, URLs: urls}, nil
}
