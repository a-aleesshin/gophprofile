package urls

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

var (
	ErrInvalidConfig = errors.New("invalid url config")
	ErrUnsafeTarget  = errors.New("unsafe url target")
)

const uploadPrefix = "/upload/"

type Config struct {
	PublicBase  string `env:"PUBLIC_BASE,required,notEmpty"`
	Placeholder string `env:"PLACEHOLDER,required,notEmpty"`
}

type Builder struct {
	base        string
	placeholder string
}

func New(cfg Config) (*Builder, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.PublicBase), "/")
	placeholder := strings.TrimSpace(cfg.Placeholder)

	if err := absolute(base, "public base"); err != nil {
		return nil, err
	}

	if err := absolute(placeholder, "placeholder"); err != nil {
		return nil, err
	}

	return &Builder{base: base, placeholder: placeholder}, nil
}

func absolute(raw, field string) error {
	if raw == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidConfig, field)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %s %q: %w", ErrInvalidConfig, field, raw, err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: %s must be an absolute http url (got %q)",
			ErrInvalidConfig, field, raw)
	}

	if parsed.Host == "" {
		return fmt.Errorf("%w: %s has no host (got %q)", ErrInvalidConfig, field, raw)
	}

	return nil
}

func (b *Builder) VariantURL(fileID domain.FileRef, name, ext string) (string, error) {
	if fileID.IsZero() {
		return "", fmt.Errorf("%w: file id is required", ErrUnsafeTarget)
	}

	if err := safeSegment(name, "variant name"); err != nil {
		return "", err
	}

	if err := safeSegment(ext, "extension"); err != nil {
		return "", err
	}

	return b.base + uploadPrefix + fileID.String() + "/" + name + "." + ext, nil
}

func (b *Builder) PlaceholderURL() string { return b.placeholder }

func safeSegment(value, field string) error {
	if value == "" {
		return fmt.Errorf("%w: %s is required", ErrUnsafeTarget, field)
	}

	for i := range len(value) {
		symbol := value[i]

		if (symbol < 'a' || symbol > 'z') && (symbol < '0' || symbol > '9') && symbol != '-' {
			return fmt.Errorf("%w: %s must contain only [a-z0-9-] (got %q)",
				ErrUnsafeTarget, field, value)
		}
	}

	return nil
}
