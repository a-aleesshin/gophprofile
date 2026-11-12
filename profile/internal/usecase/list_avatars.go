package usecase

import (
	"context"
	"fmt"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type avatarViewLister interface {
	ViewsByUser(ctx context.Context, userID domain.UserID, page Page) ([]AvatarView, error)
}

type ListAvatarsCommand struct {
	UserID domain.UserID
	Viewer domain.UserID
	Limit  int
	Cursor string
}

type ListAvatarsResult struct {
	Items      []AvatarView
	URLs       map[string]map[string]string
	NextCursor string
	Owner      bool
}

type ListAvatarsHandler struct {
	avatars avatarViewLister
	urls    contentURLBuilder
}

func NewListAvatarsHandler(
	avatars avatarViewLister,
	urls contentURLBuilder,
) *ListAvatarsHandler {
	return &ListAvatarsHandler{avatars: avatars, urls: urls}
}

func (h *ListAvatarsHandler) Handle(
	ctx context.Context,
	cmd ListAvatarsCommand,
) (ListAvatarsResult, error) {
	if cmd.UserID.IsZero() {
		return ListAvatarsResult{}, fmt.Errorf("%w: user id is required", ErrInvalidCommand)
	}

	limit := cmd.Limit

	switch {
	case limit <= 0:
		limit = DefaultPageLimit
	case limit > MaxPageLimit:
		limit = MaxPageLimit
	}

	after, afterID, err := decodeCursor(cmd.Cursor)
	if err != nil {
		return ListAvatarsResult{}, err
	}

	owner := !cmd.Viewer.IsZero() && cmd.Viewer.Equal(cmd.UserID)

	views, err := h.avatars.ViewsByUser(ctx, cmd.UserID, Page{
		Limit:          limit + 1,
		AfterCreatedAt: after,
		AfterID:        afterID,
		OnlyServable:   !owner,
	})
	if err != nil {
		return ListAvatarsResult{}, err
	}

	next := ""

	if len(views) > limit {
		next = encodeCursor(views[limit-1])
		views = views[:limit]
	}

	urls := make(map[string]map[string]string, len(views))

	for _, view := range views {
		perVariant := make(map[string]string, len(view.Variants))

		for _, variant := range view.Variants {
			url, urlErr := h.urls.VariantURL(view.FileID, variant.Name, variant.Ext)
			if urlErr != nil {
				return ListAvatarsResult{}, urlErr
			}

			perVariant[variant.Name] = url
		}

		urls[view.ID.String()] = perVariant
	}

	return ListAvatarsResult{Items: views, URLs: urls, NextCursor: next, Owner: owner}, nil
}
