package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

const minSecretLength = 32

var (
	ErrInvalidConfig = errors.New("invalid identity config")
	ErrBadToken      = errors.New("identity token does not verify")
	ErrExpired       = errors.New("identity token has expired")
	ErrBadSecret     = errors.New("wrong login secret")
)

type Config struct {
	Secret         string        `env:"SECRET,required,notEmpty"`
	PreviousSecret string        `env:"PREVIOUS_SECRET"`
	LoginSecret    string        `env:"LOGIN_SECRET,required,notEmpty"`
	TTL            time.Duration `env:"TTL" envDefault:"1h"`
}

type Clock interface {
	Now() time.Time
}

type Signer struct {
	signing  []byte
	accepted [][]byte
	login    []byte
	ttl      time.Duration
	clock    Clock
}

func New(cfg Config, clock Clock) (*Signer, error) {
	switch {
	case clock == nil:
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidConfig)
	case len(cfg.Secret) < minSecretLength:
		return nil, fmt.Errorf("%w: secret must be at least %d bytes",
			ErrInvalidConfig, minSecretLength)
	case cfg.PreviousSecret != "" && len(cfg.PreviousSecret) < minSecretLength:
		return nil, fmt.Errorf("%w: previous secret must be at least %d bytes",
			ErrInvalidConfig, minSecretLength)
	case len(cfg.LoginSecret) < minSecretLength:
		return nil, fmt.Errorf("%w: login secret must be at least %d bytes",
			ErrInvalidConfig, minSecretLength)
	case cfg.LoginSecret == cfg.Secret:
		return nil, fmt.Errorf(
			"%w: the login secret reaches the browser and the signing secret must not",
			ErrInvalidConfig)
	case cfg.TTL <= 0:
		return nil, fmt.Errorf("%w: ttl must be positive", ErrInvalidConfig)
	}

	signing := []byte(cfg.Secret)
	accepted := [][]byte{signing}

	if cfg.PreviousSecret != "" {
		accepted = append(accepted, []byte(cfg.PreviousSecret))
	}

	return &Signer{
		signing:  signing,
		accepted: accepted,
		login:    []byte(cfg.LoginSecret),
		ttl:      cfg.TTL,
		clock:    clock,
	}, nil
}

func (s *Signer) Login(secret string) error {
	if subtle.ConstantTimeCompare([]byte(secret), s.login) != 1 {
		return ErrBadSecret
	}

	return nil
}

func (s *Signer) Issue(user domain.UserID) (string, time.Time, error) {
	if user.IsZero() {
		return "", time.Time{}, fmt.Errorf("%w: user is required", ErrBadToken)
	}

	expires := s.clock.Now().Add(s.ttl).UTC()

	payload := base64.RawURLEncoding.EncodeToString(
		[]byte(user.String() + "\n" + strconv.FormatInt(expires.Unix(), 10)))

	return payload + "." + sign(s.signing, payload), expires, nil
}

func (s *Signer) Verify(token string) (domain.UserID, error) {
	payload, signature, found := strings.Cut(token, ".")
	if !found || payload == "" || signature == "" {
		return domain.UserID{}, fmt.Errorf("%w: expected a payload and a signature", ErrBadToken)
	}

	if !s.accepts(payload, signature) {
		return domain.UserID{}, ErrBadToken
	}

	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return domain.UserID{}, fmt.Errorf("%w: payload is not base64url", ErrBadToken)
	}

	name, unix, found := strings.Cut(string(raw), "\n")
	if !found {
		return domain.UserID{}, fmt.Errorf("%w: payload carries no expiry", ErrBadToken)
	}

	seconds, err := strconv.ParseInt(unix, 10, 64)
	if err != nil {
		return domain.UserID{}, fmt.Errorf("%w: expiry is not a number", ErrBadToken)
	}

	if !s.clock.Now().Before(time.Unix(seconds, 0)) {
		return domain.UserID{}, ErrExpired
	}

	user, err := domain.RestoreUserID(name)
	if err != nil {
		return domain.UserID{}, fmt.Errorf("%w: %w", ErrBadToken, err)
	}

	return user, nil
}

func (s *Signer) accepts(payload, signature string) bool {
	for _, secret := range s.accepted {
		if hmac.Equal([]byte(signature), []byte(sign(secret, payload))) {
			return true
		}
	}

	return false
}

func sign(secret []byte, payload string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))

	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
