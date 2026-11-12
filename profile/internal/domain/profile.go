package domain

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidProfile  = errors.New("invalid profile")
	ErrProfileNotFound = errors.New("profile not found")
	ErrEmailTaken      = errors.New("email belongs to another profile")
)

type Profile struct {
	userID          UserID
	email           Email
	emailHash       EmailHash
	currentAvatarID AvatarID
	createdAt       time.Time
	updatedAt       time.Time
}

func NewProfile(userID UserID, now time.Time) (Profile, error) {
	switch {
	case userID.IsZero():
		return Profile{}, fmt.Errorf("%w: user id is required", ErrInvalidProfile)
	case now.IsZero():
		return Profile{}, fmt.Errorf("%w: creation time is required", ErrInvalidProfile)
	}

	return Profile{userID: userID, createdAt: now.UTC(), updatedAt: now.UTC()}, nil
}

type RestoreProfileParams struct {
	UserID          UserID
	Email           Email
	EmailHash       EmailHash
	CurrentAvatarID AvatarID
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func RestoreProfile(p RestoreProfileParams) (Profile, error) {
	if p.UserID.IsZero() {
		return Profile{}, fmt.Errorf("%w: user id is required", ErrInvalidProfile)
	}

	if p.Email.IsZero() != p.EmailHash.IsZero() {
		return Profile{}, fmt.Errorf(
			"%w: email and its hash must be set together", ErrInvalidProfile)
	}

	if !p.Email.IsZero() && !p.Email.Hash().Equal(p.EmailHash) {
		return Profile{}, fmt.Errorf(
			"%w: the stored hash does not match the stored email", ErrInvalidProfile)
	}

	return Profile{
		userID:          p.UserID,
		email:           p.Email,
		emailHash:       p.EmailHash,
		currentAvatarID: p.CurrentAvatarID,
		createdAt:       p.CreatedAt,
		updatedAt:       p.UpdatedAt,
	}, nil
}

func (p *Profile) UserID() UserID { return p.userID }

func (p *Profile) Email() Email { return p.email }

func (p *Profile) EmailHash() EmailHash { return p.emailHash }

func (p *Profile) CurrentAvatarID() AvatarID { return p.currentAvatarID }

func (p *Profile) CreatedAt() time.Time { return p.createdAt }

func (p *Profile) UpdatedAt() time.Time { return p.updatedAt }

func (p *Profile) HasEmail() bool { return !p.email.IsZero() }

func (p *Profile) SetEmail(email Email, now time.Time) error {
	switch {
	case email.IsZero():
		return fmt.Errorf("%w: email is required", ErrInvalidProfile)
	case now.IsZero():
		return fmt.Errorf("%w: update time is required", ErrInvalidProfile)
	}

	p.email = email
	p.emailHash = email.Hash()
	p.updatedAt = now.UTC()

	return nil
}

func (p *Profile) SetCurrentAvatar(id AvatarID, now time.Time) error {
	switch {
	case id.IsZero():
		return fmt.Errorf("%w: avatar id is required", ErrInvalidProfile)
	case now.IsZero():
		return fmt.Errorf("%w: update time is required", ErrInvalidProfile)
	}

	p.currentAvatarID = id
	p.updatedAt = now.UTC()

	return nil
}

func (p *Profile) ClearCurrentAvatar(now time.Time) error {
	if now.IsZero() {
		return fmt.Errorf("%w: update time is required", ErrInvalidProfile)
	}

	p.currentAvatarID = AvatarID{}
	p.updatedAt = now.UTC()

	return nil
}

func (p *Profile) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.Profile: serialize via DTO, not directly")
}
