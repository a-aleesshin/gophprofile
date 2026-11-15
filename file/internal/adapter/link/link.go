package link

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
)

const (
	minSecretLength = 32
	pathPrefix      = "/upload/"
)

var (
	ErrInvalidConfig = errors.New("invalid link config")
	ErrInvalidTarget = errors.New("invalid link target")
	ErrBadSignature  = errors.New("link signature does not match")
	ErrExpired       = errors.New("link has expired")
)

type Config struct {
	PublicBase     string `env:"PUBLIC_BASE,required,notEmpty"`
	Secret         string `env:"SECRET,required,notEmpty"`
	PreviousSecret string `env:"PREVIOUS_SECRET"`
}

type Clock interface {
	Now() time.Time
}

type Builder struct {
	base     string
	signing  []byte
	accepted [][]byte
	clock    Clock
}

func NewBuilder(cfg Config, clock Clock) (*Builder, error) {
	if clock == nil {
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidConfig)
	}

	base, err := url.Parse(strings.TrimRight(cfg.PublicBase, "/"))

	switch {
	case err != nil:
		return nil, fmt.Errorf("%w: public base: %w", ErrInvalidConfig, err)
	case base.Scheme != "http" && base.Scheme != "https":
		return nil, fmt.Errorf("%w: public base must be http or https, got %q",
			ErrInvalidConfig, cfg.PublicBase)
	case base.Host == "":
		return nil, fmt.Errorf("%w: public base has no host", ErrInvalidConfig)
	case base.RawQuery != "" || base.Fragment != "":
		return nil, fmt.Errorf("%w: public base must not carry a query or fragment",
			ErrInvalidConfig)
	case len(cfg.Secret) < minSecretLength:
		return nil, fmt.Errorf("%w: secret must be at least %d bytes",
			ErrInvalidConfig, minSecretLength)
	}

	accepted := [][]byte{[]byte(cfg.Secret)}

	if cfg.PreviousSecret != "" {
		if len(cfg.PreviousSecret) < minSecretLength {
			return nil, fmt.Errorf("%w: previous secret must be at least %d bytes",
				ErrInvalidConfig, minSecretLength)
		}

		if subtle.ConstantTimeCompare([]byte(cfg.PreviousSecret), []byte(cfg.Secret)) == 1 {
			return nil, fmt.Errorf("%w: previous secret must differ from the current one",
				ErrInvalidConfig)
		}

		accepted = append(accepted, []byte(cfg.PreviousSecret))
	}

	return &Builder{
		base:     base.String(),
		signing:  []byte(cfg.Secret),
		accepted: accepted,
		clock:    clock,
	}, nil
}

func (b *Builder) BuildLink(_ context.Context, params usecase.BuildLinkParams) (string, error) {
	if err := validateTarget(params.FileID, params.Variant, params.Ext); err != nil {
		return "", err
	}

	link := b.base + pathPrefix + params.FileID + "/" + params.Variant + "." + params.Ext

	if params.ExpiresAt.IsZero() {
		return link, nil
	}

	expires := strconv.FormatInt(params.ExpiresAt.UTC().Unix(), 10)

	signature := sign(b.signing, params.FileID, params.Variant, params.Ext, expires)

	return link + "?exp=" + expires + "&sig=" + signature, nil
}

type VerifyParams struct {
	FileID  string
	Variant string
	Ext     string
	Expires string
	Sig     string
}

func (b *Builder) Verify(params VerifyParams) error {
	if err := validateTarget(params.FileID, params.Variant, params.Ext); err != nil {
		return fmt.Errorf("%w: %w", ErrBadSignature, err)
	}

	if params.Sig == "" || params.Expires == "" {
		return fmt.Errorf("%w: link carries no signature", ErrBadSignature)
	}

	provided, err := base64.RawURLEncoding.DecodeString(params.Sig)
	if err != nil {
		return fmt.Errorf("%w: signature is not base64url", ErrBadSignature)
	}

	matched := false

	for _, secret := range b.accepted {
		expected := mac(secret, params.FileID, params.Variant, params.Ext, params.Expires)
		if hmac.Equal(provided, expected) {
			matched = true
		}
	}

	if !matched {
		return ErrBadSignature
	}

	seconds, err := strconv.ParseInt(params.Expires, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: exp is not a unix timestamp", ErrBadSignature)
	}

	expiresAt := time.Unix(seconds, 0).UTC()

	if b.clock.Now().UTC().After(expiresAt) {
		return fmt.Errorf("%w: at %s", ErrExpired, expiresAt.Format(time.RFC3339))
	}

	return nil
}

func sign(secret []byte, fileID, variant, ext, expires string) string {
	return base64.RawURLEncoding.EncodeToString(mac(secret, fileID, variant, ext, expires))
}

func mac(secret []byte, fileID, variant, ext, expires string) []byte {
	digest := hmac.New(sha256.New, secret)

	digest.Write([]byte(fileID))
	digest.Write([]byte("\n"))
	digest.Write([]byte(variant))
	digest.Write([]byte("."))
	digest.Write([]byte(ext))
	digest.Write([]byte("\n"))
	digest.Write([]byte(expires))

	return digest.Sum(nil)
}

func validateTarget(fileID, variant, ext string) error {
	parts := map[string]string{"file id": fileID, "variant": variant, "extension": ext}

	for name, value := range parts {
		if value == "" {
			return fmt.Errorf("%w: %s is required", ErrInvalidTarget, name)
		}

		if strings.ContainsAny(value, "\n\r/?&#%.") {
			return fmt.Errorf(
				"%w: %s must not contain a separator, it would shift the signed message",
				ErrInvalidTarget, name)
		}
	}

	return nil
}
