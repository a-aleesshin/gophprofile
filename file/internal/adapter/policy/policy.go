package policy

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
)

const (
	AccessPublic = "public"
	AccessSigned = "signed"

	ValidatorImage     = "image"
	ValidatorContainer = "container"

	FitCover  = "cover"
	FitInside = "inside"
)

var (
	ErrInvalidPolicies  = errors.New("invalid policies")
	ErrUnknownNamespace = errors.New("unknown namespace")
)

type Config struct {
	Path string `env:"PATH,required,notEmpty"`
}

type Variant struct {
	Name    string `yaml:"name"`
	Width   int    `yaml:"width"`
	Height  int    `yaml:"height"`
	Fit     string `yaml:"fit"`
	Format  string `yaml:"format"`
	Quality int    `yaml:"quality"`
}

type Namespace struct {
	Access            string        `yaml:"access"`
	Validator         string        `yaml:"validator"`
	UploadTTL         time.Duration `yaml:"upload_ttl"`
	LinkMaxTTL        time.Duration `yaml:"link_max_ttl"`
	MaxSizeBytes      int64         `yaml:"max_size_bytes"`
	ContentTypePrefix string        `yaml:"content_type_prefix"`
	AllowedMime       []string      `yaml:"allowed_mime"`
	MaxPixels         int64         `yaml:"max_pixels"`
	MaxDimension      int           `yaml:"max_dimension"`
	MinDimension      int           `yaml:"min_dimension"`
	ZipMaxEntries     int           `yaml:"zip_max_entries"`
	ZipMaxEntryBytes  int64         `yaml:"zip_max_entry_bytes"`
	ZipMaxTotalBytes  int64         `yaml:"zip_max_total_bytes"`
	ZipMaxRatio       int           `yaml:"zip_max_ratio"`
	CacheControl      string        `yaml:"cache_control"`
	Disposition       string        `yaml:"disposition"`
	AcceptRanges      bool          `yaml:"accept_ranges"`
	Variants          []Variant     `yaml:"variants"`
}

type file struct {
	Namespaces map[string]Namespace `yaml:"namespaces"`
}

type Registry struct {
	namespaces map[string]Namespace
	mime       map[string]domain.MimeSet
	variants   map[string][]domain.VariantName
}

func Load(path string) (*Registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: read %q: %w", ErrInvalidPolicies, path, err)
	}

	var parsed file

	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)

	if err := decoder.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("%w: parse %q: %w", ErrInvalidPolicies, path, err)
	}

	if len(parsed.Namespaces) == 0 {
		return nil, fmt.Errorf("%w: at least one namespace is required", ErrInvalidPolicies)
	}

	registry := &Registry{
		namespaces: make(map[string]Namespace, len(parsed.Namespaces)),
		mime:       make(map[string]domain.MimeSet, len(parsed.Namespaces)),
		variants:   make(map[string][]domain.VariantName, len(parsed.Namespaces)),
	}

	for name, namespace := range parsed.Namespaces {
		if _, err := domain.NewNamespace(name); err != nil {
			return nil, fmt.Errorf("%w: namespace %q: %w", ErrInvalidPolicies, name, err)
		}

		mime, variants, err := validate(name, namespace)
		if err != nil {
			return nil, err
		}

		registry.namespaces[name] = namespace
		registry.mime[name] = mime
		registry.variants[name] = variants
	}

	return registry, nil
}

func validate(name string, ns Namespace) (domain.MimeSet, []domain.VariantName, error) {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: namespace %q: %s", ErrInvalidPolicies, name,
			fmt.Sprintf(format, args...))
	}

	switch {
	case ns.Access != AccessPublic && ns.Access != AccessSigned:
		return domain.MimeSet{}, nil, fail("access must be %q or %q, got %q", AccessPublic, AccessSigned, ns.Access)
	case ns.Validator != ValidatorImage && ns.Validator != ValidatorContainer:
		return domain.MimeSet{}, nil, fail("validator must be %q or %q, got %q",
			ValidatorImage, ValidatorContainer, ns.Validator)
	case ns.MaxSizeBytes <= 0:
		return domain.MimeSet{}, nil, fail("max_size_bytes must be positive")
	case ns.UploadTTL <= 0:
		return domain.MimeSet{}, nil, fail("upload_ttl must be positive")
	case ns.Access == AccessSigned && ns.LinkMaxTTL <= 0:
		return domain.MimeSet{}, nil, fail("a signed namespace needs link_max_ttl")
	case ns.CacheControl == "":
		return domain.MimeSet{}, nil, fail("cache_control is required")
	case len(ns.AllowedMime) == 0:
		return domain.MimeSet{}, nil, fail("allowed_mime must not be empty")
	}

	types := make([]domain.MimeType, 0, len(ns.AllowedMime))

	for _, raw := range ns.AllowedMime {
		parsed, err := domain.NewMimeType(raw)
		if err != nil {
			return domain.MimeSet{}, nil, fail("allowed_mime %q: %v", raw, err)
		}

		if ns.ContentTypePrefix != "" && !strings.HasPrefix(parsed.String(), ns.ContentTypePrefix) {
			return domain.MimeSet{}, nil, fail(
				"allowed_mime %q does not match content_type_prefix %q, so storage would reject it",
				parsed.String(), ns.ContentTypePrefix)
		}

		types = append(types, parsed)
	}

	mime, err := domain.NewMimeSet(types...)
	if err != nil {
		return domain.MimeSet{}, nil, fail("allowed_mime: %v", err)
	}

	variants := make([]domain.VariantName, 0, len(ns.Variants))

	for _, variant := range ns.Variants {
		parsed, err := domain.NewVariantName(variant.Name)
		if err != nil {
			return domain.MimeSet{}, nil, fail("variant %q: %v", variant.Name, err)
		}

		if parsed.IsOriginal() {
			return domain.MimeSet{}, nil, fail("%q is reserved and is written for every file", parsed.String())
		}

		if _, err := domain.NewExtension(variant.Format); err != nil {
			return domain.MimeSet{}, nil, fail("variant %q format %q: %v", variant.Name, variant.Format, err)
		}

		switch {
		case variant.Width <= 0 || variant.Height <= 0:
			return domain.MimeSet{}, nil, fail("variant %q must have positive dimensions", variant.Name)
		case variant.Fit != FitCover && variant.Fit != FitInside:
			return domain.MimeSet{}, nil, fail("variant %q fit must be %q or %q, got %q",
				variant.Name, FitCover, FitInside, variant.Fit)
		case ns.MaxDimension > 0 && (variant.Width > ns.MaxDimension || variant.Height > ns.MaxDimension):
			return domain.MimeSet{}, nil, fail("variant %q is larger than max_dimension %d",
				variant.Name, ns.MaxDimension)
		}

		variants = append(variants, parsed)
	}

	return mime, variants, nil
}

func (r *Registry) Namespace(namespace domain.Namespace) (Namespace, error) {
	found, ok := r.namespaces[namespace.String()]
	if !ok {
		return Namespace{}, fmt.Errorf("%w: %q", ErrUnknownNamespace, namespace.String())
	}

	return found, nil
}

func (r *Registry) UploadPolicy(namespace domain.Namespace) (usecase.UploadPolicy, error) {
	found, err := r.Namespace(namespace)
	if err != nil {
		return usecase.UploadPolicy{}, err
	}

	return usecase.UploadPolicy{
		MaxSizeBytes:      found.MaxSizeBytes,
		AllowedMime:       r.mime[namespace.String()],
		ContentTypePrefix: found.ContentTypePrefix,
		TTL:               found.UploadTTL,
	}, nil
}

func (r *Registry) CompletePolicy(namespace domain.Namespace) (usecase.CompletePolicy, error) {
	if _, err := r.Namespace(namespace); err != nil {
		return usecase.CompletePolicy{}, err
	}

	return usecase.CompletePolicy{
		AllowedMime: r.mime[namespace.String()],
		Variants:    r.variants[namespace.String()],
	}, nil
}

func (r *Registry) AccessPolicy(namespace domain.Namespace) (usecase.AccessPolicy, error) {
	found, err := r.Namespace(namespace)
	if err != nil {
		return usecase.AccessPolicy{}, err
	}

	return usecase.AccessPolicy{
		Signed:     found.Access == AccessSigned,
		MaxLinkTTL: found.LinkMaxTTL,
	}, nil
}

func (r *Registry) ServingPolicy(namespace domain.Namespace) (usecase.ServingPolicy, error) {
	found, err := r.Namespace(namespace)
	if err != nil {
		return usecase.ServingPolicy{}, err
	}

	return usecase.ServingPolicy{
		CacheControl: found.CacheControl,
		Disposition:  found.Disposition,
		AcceptRanges: found.AcceptRanges,
	}, nil
}
