package usecase

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

var (
	ErrAvatarNotReady = errors.New("avatar is not ready")
	ErrVariantAbsent  = errors.New("the avatar has no such variant")
	ErrInvalidCursor  = errors.New("invalid cursor")
)

const (
	DefaultVariant = "300x300"

	DefaultPageLimit = 20
	MaxPageLimit     = 100
)

type VariantView struct {
	Name     string
	Ext      string
	MimeType string
	Width    int32
	Height   int32
}

type AvatarView struct {
	ID           domain.AvatarID
	UserID       domain.UserID
	FileID       domain.FileRef
	Status       domain.AvatarStatus
	FileName     string
	MimeType     string
	SizeBytes    int64
	Width        int32
	Height       int32
	Variants     []VariantView
	FailedReason string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (v AvatarView) VariantByName(name string) (VariantView, bool) {
	for _, variant := range v.Variants {
		if variant.Name == name {
			return variant, true
		}
	}

	return VariantView{}, false
}

type Page struct {
	Limit          int
	AfterCreatedAt time.Time
	AfterID        domain.AvatarID
}

type contentURLBuilder interface {
	VariantURL(fileID domain.FileRef, name, ext string) (string, error)
	PlaceholderURL() string
}

func encodeCursor(view AvatarView) string {
	raw := strconv.FormatInt(view.CreatedAt.UTC().UnixMicro(), 10) + "|" + view.ID.String()

	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor string) (time.Time, domain.AvatarID, error) {
	if cursor == "" {
		return time.Time{}, domain.AvatarID{}, nil
	}

	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, domain.AvatarID{}, fmt.Errorf("%w: not base64url", ErrInvalidCursor)
	}

	micros, rawID, found := strings.Cut(string(decoded), "|")
	if !found {
		return time.Time{}, domain.AvatarID{}, fmt.Errorf(
			"%w: expected a timestamp and an id", ErrInvalidCursor)
	}

	parsed, err := strconv.ParseInt(micros, 10, 64)
	if err != nil {
		return time.Time{}, domain.AvatarID{}, fmt.Errorf(
			"%w: the timestamp is not a number", ErrInvalidCursor)
	}

	id, err := domain.ParseAvatarID(rawID)
	if err != nil {
		return time.Time{}, domain.AvatarID{}, fmt.Errorf("%w: %w", ErrInvalidCursor, err)
	}

	return time.UnixMicro(parsed).UTC(), id, nil
}

func resolveVariant(
	urls contentURLBuilder,
	view AvatarView,
	size string,
) (string, error) {
	if size == "" {
		size = DefaultVariant
	}

	if !view.Status.IsServable() {
		return "", fmt.Errorf("%w: status %q", ErrAvatarNotReady, view.Status.String())
	}

	variant, found := view.VariantByName(size)
	if !found {
		return "", fmt.Errorf("%w: %q", ErrVariantAbsent, size)
	}

	return urls.VariantURL(view.FileID, variant.Name, variant.Ext)
}
