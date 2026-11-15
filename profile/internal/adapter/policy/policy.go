package policy

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

var ErrInvalidConfig = errors.New("invalid upload policy")

const minSizeBytes = 1024

type Config struct {
	MaxSizeBytes      int64    `env:"MAX_SIZE_BYTES" envDefault:"10485760"`
	AllowedMime       []string `env:"ALLOWED_MIME" envSeparator:"," envDefault:"image/jpeg,image/png,image/webp"`
	MaxAvatarsPerUser int      `env:"MAX_AVATARS_PER_USER" envDefault:"20"`
}

type Provider struct {
	policy usecase.UploadPolicy
}

func New(cfg Config) (*Provider, error) {
	switch {
	case cfg.MaxSizeBytes < minSizeBytes:
		return nil, fmt.Errorf("%w: max size must be at least %d bytes (got %d)",
			ErrInvalidConfig, minSizeBytes, cfg.MaxSizeBytes)
	case len(cfg.AllowedMime) == 0:
		return nil, fmt.Errorf("%w: at least one mime type must be allowed", ErrInvalidConfig)
	case cfg.MaxAvatarsPerUser <= 0:
		return nil, fmt.Errorf("%w: max avatars per user must be positive (got %d)",
			ErrInvalidConfig, cfg.MaxAvatarsPerUser)
	}

	allowed := make([]string, 0, len(cfg.AllowedMime))

	for _, raw := range cfg.AllowedMime {
		mime := strings.ToLower(strings.TrimSpace(raw))

		switch {
		case mime == "":
			return nil, fmt.Errorf("%w: an allowed mime type is empty", ErrInvalidConfig)
		case !strings.HasPrefix(mime, "image/"):
			return nil, fmt.Errorf("%w: %q is not an image type", ErrInvalidConfig, raw)
		case slices.Contains(allowed, mime):
			continue
		}

		allowed = append(allowed, mime)
	}

	return &Provider{policy: usecase.UploadPolicy{
		MaxSizeBytes:      cfg.MaxSizeBytes,
		AllowedMime:       allowed,
		MaxAvatarsPerUser: cfg.MaxAvatarsPerUser,
	}}, nil
}

func (p *Provider) UploadPolicy() usecase.UploadPolicy { return p.policy }
