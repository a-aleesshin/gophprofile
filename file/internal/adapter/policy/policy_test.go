package policy

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
)

const shippedPolicies = "../../../configs/policies.yaml"

func testMime(t *testing.T, raw string) domain.MimeType {
	t.Helper()

	value, err := domain.NewMimeType(raw)
	require.NoError(t, err)

	return value
}

func testNamespace(t *testing.T, name string) domain.Namespace {
	t.Helper()

	namespace, err := domain.NewNamespace(name)
	require.NoError(t, err)

	return namespace
}

func writePolicies(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "policies.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	return path
}

func TestLoad_ShippedFileIsValid(t *testing.T) {
	t.Parallel()

	registry, err := Load(shippedPolicies)
	require.NoError(t, err, "the file we ship must pass our own validation")

	avatar, err := registry.Namespace(testNamespace(t, "avatar"))
	require.NoError(t, err)
	assert.Equal(t, AccessPublic, avatar.Access)
	assert.Len(t, avatar.Variants, 3)

	document, err := registry.Namespace(testNamespace(t, "document"))
	require.NoError(t, err)
	assert.Equal(t, AccessSigned, document.Access)
	assert.Empty(t, document.Variants)
}

func TestRegistry_MapsEachPort(t *testing.T) {
	t.Parallel()

	registry, err := Load(shippedPolicies)
	require.NoError(t, err)

	avatar := testNamespace(t, "avatar")

	upload, err := registry.UploadPolicy(avatar)
	require.NoError(t, err)
	assert.Equal(t, int64(10485760), upload.MaxSizeBytes)
	assert.Equal(t, 15*time.Minute, upload.TTL)
	assert.Equal(t, "image/", upload.ContentTypePrefix)
	assert.Equal(t, 3, upload.AllowedMime.Len())
	assert.True(t, upload.AllowedMime.Contains(testMime(t, "image/jpeg")))
	assert.False(t, upload.AllowedMime.Contains(testMime(t, "image/gif")))

	complete, err := registry.CompletePolicy(avatar)
	require.NoError(t, err)
	require.Len(t, complete.Variants, 3)
	assert.Equal(t, "100x100", complete.Variants[0].String())

	access, err := registry.AccessPolicy(avatar)
	require.NoError(t, err)
	assert.False(t, access.Signed)

	serving, err := registry.ServingPolicy(avatar)
	require.NoError(t, err)
	assert.Equal(t, "public, max-age=31536000, immutable", serving.CacheControl)
	assert.Equal(t, "inline", serving.Disposition)
	assert.False(t, serving.AcceptRanges)

	document := testNamespace(t, "document")

	access, err = registry.AccessPolicy(document)
	require.NoError(t, err)
	assert.True(t, access.Signed)
	assert.Equal(t, 15*time.Minute, access.MaxLinkTTL)

	serving, err = registry.ServingPolicy(document)
	require.NoError(t, err)
	assert.Equal(t, "private, no-store", serving.CacheControl)
	assert.Equal(t, "attachment", serving.Disposition)
	assert.True(t, serving.AcceptRanges)
}

func TestRegistry_UnknownNamespace(t *testing.T) {
	t.Parallel()

	registry, err := Load(shippedPolicies)
	require.NoError(t, err)

	banner := testNamespace(t, "banner")

	_, err = registry.UploadPolicy(banner)
	require.ErrorIs(t, err, ErrUnknownNamespace)

	_, err = registry.CompletePolicy(banner)
	require.ErrorIs(t, err, ErrUnknownNamespace)

	_, err = registry.AccessPolicy(banner)
	require.ErrorIs(t, err, ErrUnknownNamespace)

	_, err = registry.ServingPolicy(banner)
	require.ErrorIs(t, err, ErrUnknownNamespace)
}

const validAvatar = `
namespaces:
  avatar:
    access: public
    validator: image
    upload_ttl: 15m
    max_size_bytes: 1048576
    content_type_prefix: "image/"
    allowed_mime: [image/jpeg]
    max_dimension: 10000
    cache_control: "public, max-age=60"
    disposition: inline
    variants:
      - {name: 300x300, width: 300, height: 300, fit: cover, format: webp, quality: 82}
`

func TestLoad_Rejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "no namespaces", body: "namespaces: {}"},
		{name: "unknown field", body: validAvatar + "    surprise: 1\n"},
		{name: "bad namespace name", body: `
namespaces:
  Avatar!:
    access: public
    validator: image
    upload_ttl: 15m
    max_size_bytes: 1
    allowed_mime: [image/jpeg]
    cache_control: "x"
`},
		{name: "bad access", body: `
namespaces:
  avatar:
    access: maybe
    validator: image
    upload_ttl: 15m
    max_size_bytes: 1
    allowed_mime: [image/jpeg]
    cache_control: "x"
`},
		{name: "signed without link ttl", body: `
namespaces:
  document:
    access: signed
    validator: container
    upload_ttl: 15m
    max_size_bytes: 1
    allowed_mime: [application/pdf]
    cache_control: "x"
`},
		{name: "empty allowed mime", body: `
namespaces:
  avatar:
    access: public
    validator: image
    upload_ttl: 15m
    max_size_bytes: 1
    allowed_mime: []
    cache_control: "x"
`},
		{name: "duplicate mime", body: `
namespaces:
  avatar:
    access: public
    validator: image
    upload_ttl: 15m
    max_size_bytes: 1
    allowed_mime: [image/jpeg, image/jpeg]
    cache_control: "x"
`},
		{name: "mime outside the prefix", body: `
namespaces:
  avatar:
    access: public
    validator: image
    upload_ttl: 15m
    max_size_bytes: 1
    content_type_prefix: "image/"
    allowed_mime: [image/jpeg, application/pdf]
    cache_control: "x"
`},
		{name: "original declared as a variant", body: `
namespaces:
  avatar:
    access: public
    validator: image
    upload_ttl: 15m
    max_size_bytes: 1
    allowed_mime: [image/jpeg]
    cache_control: "x"
    variants:
      - {name: original, width: 1, height: 1, fit: cover, format: webp}
`},
		{name: "variant larger than max dimension", body: `
namespaces:
  avatar:
    access: public
    validator: image
    upload_ttl: 15m
    max_size_bytes: 1
    allowed_mime: [image/jpeg]
    cache_control: "x"
    max_dimension: 100
    variants:
      - {name: 300x300, width: 300, height: 300, fit: cover, format: webp}
`},
		{name: "unknown fit", body: `
namespaces:
  avatar:
    access: public
    validator: image
    upload_ttl: 15m
    max_size_bytes: 1
    allowed_mime: [image/jpeg]
    cache_control: "x"
    variants:
      - {name: 300x300, width: 300, height: 300, fit: squeeze, format: webp}
`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Load(writePolicies(t, tt.body))
			require.ErrorIs(t, err, ErrInvalidPolicies)
		})
	}
}

func TestLoad_MissingFile(t *testing.T) {
	t.Parallel()

	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	require.ErrorIs(t, err, ErrInvalidPolicies,
		"a missing policy file must stop the service at startup")
}

func TestRegistry_SatisfiesTheUseCasePorts(t *testing.T) {
	t.Parallel()

	registry, err := Load(shippedPolicies)
	require.NoError(t, err)

	assert.NotNil(t, usecase.NewCreateUploadHandler(registry, nil, nil, nil, nil))
	assert.NotNil(t, usecase.NewCompleteUploadHandler(registry, nil, nil, nil, nil, nil, nil))
	assert.NotNil(t, usecase.NewCreateAccessLinkHandler(registry, nil, nil, nil))
	assert.NotNil(t, usecase.NewServeVariantHandler(registry, nil, nil))
}
