package domain

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

const (
	maxVariantNameLength = 32
	maxExtensionLength   = 8
	maxMimeTypeLength    = 255
)

var ErrInvalidVariant = errors.New("invalid variant")

type Variant struct {
	name     string
	ext      string
	mimeType string
	width    int32
	height   int32
}

type NewVariantParams struct {
	Name     string
	Ext      string
	MimeType string
	Width    int32
	Height   int32
}

func NewVariant(p NewVariantParams) (Variant, error) {
	name := strings.ToLower(strings.TrimSpace(p.Name))
	ext := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(p.Ext, ".")))
	mimeType := strings.ToLower(strings.TrimSpace(p.MimeType))

	switch {
	case name == "" || len(name) > maxVariantNameLength:
		return Variant{}, fmt.Errorf("%w: name %q", ErrInvalidVariant, p.Name)
	case ext == "" || len(ext) > maxExtensionLength:
		return Variant{}, fmt.Errorf("%w: extension %q", ErrInvalidVariant, p.Ext)
	case mimeType == "" || len(mimeType) > maxMimeTypeLength:
		return Variant{}, fmt.Errorf("%w: mime type %q", ErrInvalidVariant, p.MimeType)
	case p.Width <= 0 || p.Height <= 0:
		return Variant{}, fmt.Errorf("%w: %s is %dx%d", ErrInvalidVariant, name, p.Width, p.Height)
	}

	if err := validateSlug(name, "name"); err != nil {
		return Variant{}, err
	}

	if err := validateSlug(ext, "extension"); err != nil {
		return Variant{}, err
	}

	return Variant{name: name, ext: ext, mimeType: mimeType, width: p.Width, height: p.Height}, nil
}

func validateSlug(value, field string) error {
	for i := range len(value) {
		symbol := value[i]

		if (symbol < 'a' || symbol > 'z') && (symbol < '0' || symbol > '9') && symbol != '-' {
			return fmt.Errorf("%w: %s must contain only [a-z0-9-] (got %q)",
				ErrInvalidVariant, field, value[i:i+1])
		}
	}

	return nil
}

func (v Variant) Name() string { return v.name }

func (v Variant) Ext() string { return v.ext }

func (v Variant) MimeType() string { return v.mimeType }

func (v Variant) Width() int32 { return v.width }

func (v Variant) Height() int32 { return v.height }

func (v Variant) IsZero() bool { return v.name == "" }

func (v Variant) Path() string { return v.name + "." + v.ext }

func (v Variant) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.Variant: serialize via DTO, not directly")
}

var ErrInvalidSnapshot = errors.New("invalid file snapshot")

type FileSnapshot struct {
	fileName  string
	mimeType  string
	sizeBytes int64
	width     int32
	height    int32
}

type NewFileSnapshotParams struct {
	FileName  string
	MimeType  string
	SizeBytes int64
	Width     int32
	Height    int32
}

func NewFileSnapshot(p NewFileSnapshotParams) (FileSnapshot, error) {
	fileName := strings.TrimSpace(p.FileName)
	mimeType := strings.ToLower(strings.TrimSpace(p.MimeType))

	switch {
	case mimeType == "" || len(mimeType) > maxMimeTypeLength:
		return FileSnapshot{}, fmt.Errorf("%w: mime type %q", ErrInvalidSnapshot, p.MimeType)
	case p.SizeBytes <= 0:
		return FileSnapshot{}, fmt.Errorf("%w: size must be positive (got %d)",
			ErrInvalidSnapshot, p.SizeBytes)
	case p.Width < 0 || p.Height < 0:
		return FileSnapshot{}, fmt.Errorf("%w: dimensions must not be negative (got %dx%d)",
			ErrInvalidSnapshot, p.Width, p.Height)
	case int64(p.Width) > math.MaxInt32 || int64(p.Height) > math.MaxInt32:
		return FileSnapshot{}, fmt.Errorf("%w: dimensions are out of range", ErrInvalidSnapshot)
	}

	return FileSnapshot{
		fileName:  fileName,
		mimeType:  mimeType,
		sizeBytes: p.SizeBytes,
		width:     p.Width,
		height:    p.Height,
	}, nil
}

func (s FileSnapshot) FileName() string { return s.fileName }

func (s FileSnapshot) MimeType() string { return s.mimeType }

func (s FileSnapshot) SizeBytes() int64 { return s.sizeBytes }

func (s FileSnapshot) Width() int32 { return s.width }

func (s FileSnapshot) Height() int32 { return s.height }

func (s FileSnapshot) IsZero() bool { return s.mimeType == "" }

func (s FileSnapshot) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.FileSnapshot: serialize via DTO, not directly")
}
