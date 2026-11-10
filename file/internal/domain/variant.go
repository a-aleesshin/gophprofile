package domain

import (
	"errors"
	"fmt"
	"time"
)

var ErrInvalidVariant = errors.New("invalid variant")

type Variant struct {
	fileID     FileID
	name       VariantName
	ext        Extension
	mimeType   MimeType
	dimensions Dimensions
	size       Size
	checksum   Checksum
	location   Location
	createdAt  time.Time
}

type NewVariantParams struct {
	FileID     FileID
	Name       VariantName
	Ext        Extension
	MimeType   MimeType
	Dimensions Dimensions
	Size       Size
	Checksum   Checksum
	Now        time.Time
}

func NewVariant(p NewVariantParams) (Variant, error) {
	switch {
	case p.FileID.IsZero():
		return Variant{}, fmt.Errorf("%w: file id is required", ErrInvalidVariant)
	case p.Name.IsZero():
		return Variant{}, fmt.Errorf("%w: name is required", ErrInvalidVariant)
	case p.Ext.IsZero():
		return Variant{}, fmt.Errorf("%w: extension is required", ErrInvalidVariant)
	case p.MimeType.IsZero():
		return Variant{}, fmt.Errorf("%w: mime type is required", ErrInvalidVariant)
	case p.Size.IsZero():
		return Variant{}, fmt.Errorf("%w: size is required", ErrInvalidVariant)
	case p.Checksum.IsZero():
		return Variant{}, fmt.Errorf("%w: checksum is required", ErrInvalidVariant)
	case p.Now.IsZero():
		return Variant{}, fmt.Errorf("%w: now is required", ErrInvalidVariant)
	}

	location, err := variantLocation(p.FileID, p.Name, p.Ext)
	if err != nil {
		return Variant{}, err
	}

	return Variant{
		fileID:     p.FileID,
		name:       p.Name,
		ext:        p.Ext,
		mimeType:   p.MimeType,
		dimensions: p.Dimensions,
		size:       p.Size,
		checksum:   p.Checksum,
		location:   location,
		createdAt:  p.Now.UTC(),
	}, nil
}

type RestoreVariantParams struct {
	FileID     FileID
	Name       VariantName
	Ext        Extension
	MimeType   MimeType
	Dimensions Dimensions
	Size       Size
	Checksum   Checksum
	Location   Location
	CreatedAt  time.Time
}

func RestoreVariant(p RestoreVariantParams) (Variant, error) {
	switch {
	case p.FileID.IsZero():
		return Variant{}, fmt.Errorf("%w: file id is required", ErrInvalidVariant)
	case p.Name.IsZero():
		return Variant{}, fmt.Errorf("%w: name is required", ErrInvalidVariant)
	case p.Ext.IsZero():
		return Variant{}, fmt.Errorf("%w: extension is required", ErrInvalidVariant)
	case p.Location.IsZero():
		return Variant{}, fmt.Errorf("%w: location is required", ErrInvalidVariant)
	case p.CreatedAt.IsZero():
		return Variant{}, fmt.Errorf("%w: created at is required", ErrInvalidVariant)
	}

	return Variant{
		fileID:     p.FileID,
		name:       p.Name,
		ext:        p.Ext,
		mimeType:   p.MimeType,
		dimensions: p.Dimensions,
		size:       p.Size,
		checksum:   p.Checksum,
		location:   p.Location,
		createdAt:  p.CreatedAt.UTC(),
	}, nil
}

func variantLocation(id FileID, name VariantName, ext Extension) (Location, error) {
	if name.IsOriginal() {
		return NewOriginalLocation(id)
	}

	return NewVariantLocation(id, name, ext)
}

func (v *Variant) FileID() FileID { return v.fileID }

func (v *Variant) Name() VariantName { return v.name }

func (v *Variant) Ext() Extension { return v.ext }

func (v *Variant) MimeType() MimeType { return v.mimeType }

func (v *Variant) Dimensions() Dimensions { return v.dimensions }

func (v *Variant) Size() Size { return v.size }

func (v *Variant) Checksum() Checksum { return v.checksum }

func (v *Variant) Location() Location { return v.location }

func (v *Variant) CreatedAt() time.Time { return v.createdAt }

func (v *Variant) IsOriginal() bool { return v.name.IsOriginal() }

func (v *Variant) MatchesExt(ext Extension) bool { return v.ext.Equal(ext) }

func (v *Variant) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.Variant: serialize via DTO, not directly")
}
