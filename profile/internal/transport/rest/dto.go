package rest

import (
	"time"

	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

type startUploadRequest struct {
	FileName  string `json:"file_name"`
	MimeType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
}

type bindEmailRequest struct {
	Email string `json:"email"`
}

type uploadTicketBody struct {
	URL       string            `json:"url"`
	Fields    map[string]string `json:"fields"`
	ExpiresAt time.Time         `json:"expires_at"`
}

type startUploadBody struct {
	AvatarID string           `json:"avatar_id"`
	Status   string           `json:"status"`
	Upload   uploadTicketBody `json:"upload"`
}

type completeUploadBody struct {
	AvatarID string `json:"avatar_id"`
	Status   string `json:"status"`
}

type dimensionsBody struct {
	Width  int32 `json:"width"`
	Height int32 `json:"height"`
}

type fileBody struct {
	FileName   string         `json:"file_name"`
	MimeType   string         `json:"mime_type"`
	SizeBytes  int64          `json:"size_bytes"`
	Dimensions dimensionsBody `json:"dimensions"`
}

type variantBody struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Width    int32  `json:"width"`
	Height   int32  `json:"height"`
	MimeType string `json:"mime_type"`
}

type avatarBody struct {
	ID           string        `json:"id"`
	UserID       string        `json:"user_id"`
	Status       string        `json:"status"`
	File         *fileBody     `json:"file,omitempty"`
	Variants     []variantBody `json:"variants"`
	FailedReason string        `json:"failed_reason,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

type listBody struct {
	Items      []avatarBody `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type bindEmailBody struct {
	UserID    string `json:"user_id"`
	EmailHash string `json:"email_hash"`
}

func toAvatarBody(view usecase.AvatarView, urls map[string]string) avatarBody {
	body := avatarBody{
		ID:           view.ID.String(),
		UserID:       view.UserID.String(),
		Status:       view.Status.String(),
		Variants:     make([]variantBody, 0, len(view.Variants)),
		FailedReason: view.FailedReason,
		CreatedAt:    view.CreatedAt,
		UpdatedAt:    view.UpdatedAt,
	}

	if view.MimeType != "" {
		body.File = &fileBody{
			FileName:   view.FileName,
			MimeType:   view.MimeType,
			SizeBytes:  view.SizeBytes,
			Dimensions: dimensionsBody{Width: view.Width, Height: view.Height},
		}
	}

	for _, variant := range view.Variants {
		body.Variants = append(body.Variants, variantBody{
			Name:     variant.Name,
			URL:      urls[variant.Name],
			Width:    variant.Width,
			Height:   variant.Height,
			MimeType: variant.MimeType,
		})
	}

	return body
}
