package usecase

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

var (
	ErrInvalidCommand      = errors.New("invalid command")
	ErrUnsupportedMimeType = errors.New("unsupported mime type")
	ErrFileTooLarge        = errors.New("file is too large")
	ErrTooManyAvatars      = errors.New("the user has too many avatars")
	ErrForbidden           = errors.New("the avatar belongs to another user")
	ErrStaleVersion        = errors.New("aggregate was modified concurrently")

	ErrUploadNotFound         = errors.New("the file service has no such upload")
	ErrUploadNotUsable        = errors.New("the upload cannot be completed")
	ErrFileServiceUnavailable = errors.New("the file service is unavailable")
	ErrFileServiceTimeout     = errors.New("the file service did not answer in time")
)

type UploadPolicy struct {
	MaxSizeBytes      int64
	AllowedMime       []string
	MaxAvatarsPerUser int
}

type CreateUploadParams struct {
	Namespace      string
	OwnerRef       string
	OriginalName   string
	DeclaredMime   string
	MaxSizeBytes   int64
	IdempotencyKey string
}

type CreateUploadTicket struct {
	UploadID  string
	URL       string
	Fields    map[string]string
	ExpiresAt time.Time
}

type clock interface {
	Now() time.Time
}

type transactor interface {
	InTx(ctx context.Context, fn func(context.Context) error) error
}

type uploadPolicyProvider interface {
	UploadPolicy() UploadPolicy
}

type profileUpserter interface {
	ByUserID(ctx context.Context, userID domain.UserID) (domain.Profile, error)
	Create(ctx context.Context, profile *domain.Profile) error
}

type avatarCreator interface {
	Create(ctx context.Context, avatar *domain.Avatar) error
	CountByUser(ctx context.Context, userID domain.UserID) (int, error)
}

type uploadTicketIssuer interface {
	CreateUpload(ctx context.Context, params CreateUploadParams) (CreateUploadTicket, error)
}

type StartUploadCommand struct {
	UserID    domain.UserID
	FileName  string
	MimeType  string
	SizeBytes int64
}

type StartUploadResult struct {
	AvatarID  domain.AvatarID
	Status    domain.AvatarStatus
	URL       string
	Fields    map[string]string
	ExpiresAt time.Time
}

type StartUploadHandler struct {
	policies uploadPolicyProvider
	profiles profileUpserter
	avatars  avatarCreator
	tickets  uploadTicketIssuer
	tx       transactor
	clock    clock
}

func NewStartUploadHandler(
	policies uploadPolicyProvider,
	profiles profileUpserter,
	avatars avatarCreator,
	tickets uploadTicketIssuer,
	tx transactor,
	clock clock,
) *StartUploadHandler {
	return &StartUploadHandler{
		policies: policies,
		profiles: profiles,
		avatars:  avatars,
		tickets:  tickets,
		tx:       tx,
		clock:    clock,
	}
}

func (h *StartUploadHandler) Handle(
	ctx context.Context,
	cmd StartUploadCommand,
) (StartUploadResult, error) {
	if cmd.UserID.IsZero() {
		return StartUploadResult{}, fmt.Errorf("%w: user id is required", ErrInvalidCommand)
	}

	policy := h.policies.UploadPolicy()

	declaredMime := strings.ToLower(strings.TrimSpace(cmd.MimeType))

	switch {
	case declaredMime == "":
		return StartUploadResult{}, fmt.Errorf("%w: mime type is required", ErrInvalidCommand)
	case !slices.Contains(policy.AllowedMime, declaredMime):
		return StartUploadResult{}, fmt.Errorf("%w: %q is not among %s",
			ErrUnsupportedMimeType, declaredMime, strings.Join(policy.AllowedMime, ", "))
	case cmd.SizeBytes <= 0:
		return StartUploadResult{}, fmt.Errorf("%w: size must be positive", ErrInvalidCommand)
	case cmd.SizeBytes > policy.MaxSizeBytes:
		return StartUploadResult{}, fmt.Errorf("%w: %d exceeds %d",
			ErrFileTooLarge, cmd.SizeBytes, policy.MaxSizeBytes)
	}

	now := h.clock.Now()

	profile, err := h.profiles.ByUserID(ctx, cmd.UserID)

	created := false

	if errors.Is(err, domain.ErrProfileNotFound) {
		profile, err = domain.NewProfile(cmd.UserID, now)
		if err != nil {
			return StartUploadResult{}, err
		}

		created = true
	} else if err != nil {
		return StartUploadResult{}, err
	}

	count, err := h.avatars.CountByUser(ctx, cmd.UserID)
	if err != nil {
		return StartUploadResult{}, err
	}

	if policy.MaxAvatarsPerUser > 0 && count >= policy.MaxAvatarsPerUser {
		return StartUploadResult{}, fmt.Errorf("%w: %d of %d used",
			ErrTooManyAvatars, count, policy.MaxAvatarsPerUser)
	}

	avatarID, err := domain.NewAvatarID()
	if err != nil {
		return StartUploadResult{}, err
	}

	ticket, err := h.tickets.CreateUpload(ctx, CreateUploadParams{
		Namespace:      namespaceAvatar,
		OwnerRef:       cmd.UserID.String(),
		OriginalName:   cmd.FileName,
		DeclaredMime:   declaredMime,
		MaxSizeBytes:   cmd.SizeBytes,
		IdempotencyKey: avatarID.String(),
	})
	if err != nil {
		return StartUploadResult{}, err
	}

	uploadRef, err := domain.ParseUploadRef(ticket.UploadID)
	if err != nil {
		return StartUploadResult{}, err
	}

	avatar, err := domain.NewAvatar(domain.NewAvatarParams{
		ID:       avatarID,
		UserID:   cmd.UserID,
		UploadID: uploadRef,
		Now:      now,
	})
	if err != nil {
		return StartUploadResult{}, err
	}

	if err := h.tx.InTx(ctx, func(ctx context.Context) error {
		if created {
			if err := h.profiles.Create(ctx, &profile); err != nil {
				return err
			}
		}

		return h.avatars.Create(ctx, &avatar)
	}); err != nil {
		return StartUploadResult{}, err
	}

	return StartUploadResult{
		AvatarID:  avatar.ID(),
		Status:    avatar.Status(),
		URL:       ticket.URL,
		Fields:    ticket.Fields,
		ExpiresAt: ticket.ExpiresAt,
	}, nil
}
