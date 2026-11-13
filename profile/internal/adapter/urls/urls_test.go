package urls

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

const testFileID = "0193abcd-ef01-7a23-8b45-0123456789ab"

func testBuilder(t *testing.T, base string) *Builder {
	t.Helper()

	builder, err := New(Config{
		PublicBase:  base,
		Placeholder: "https://domain.com/static/placeholder.png",
	})
	require.NoError(t, err)

	return builder
}

func testRef(t *testing.T) domain.FileRef {
	t.Helper()

	value, err := domain.ParseFileRef(testFileID)
	require.NoError(t, err)

	return value
}

func TestVariantURLIsTheCanonicalContentURL(t *testing.T) {
	t.Parallel()

	// Arrange
	builder := testBuilder(t, "https://domain.com")

	// Act
	url, err := builder.VariantURL(testRef(t), "300x300", "jpg")

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "https://domain.com/upload/"+testFileID+"/300x300.jpg", url,
		"this is the url file serves, built without asking file anything")
}

func TestATrailingSlashInTheBaseDoesNotDoubleUp(t *testing.T) {
	t.Parallel()

	builder := testBuilder(t, "https://domain.com/")

	url, err := builder.VariantURL(testRef(t), "web", "jpg")

	require.NoError(t, err)
	assert.Equal(t, "https://domain.com/upload/"+testFileID+"/web.jpg", url)
}

func TestPlaceholderURLIsHandedOutAsConfigured(t *testing.T) {
	t.Parallel()

	builder := testBuilder(t, "https://domain.com")

	assert.Equal(t, "https://domain.com/static/placeholder.png", builder.PlaceholderURL())
}

func TestVariantURLRefusesAnUnsafeTarget(t *testing.T) {
	t.Parallel()

	builder := testBuilder(t, "https://domain.com")

	tests := []struct {
		name    string
		fileID  domain.FileRef
		variant string
		ext     string
	}{
		{name: "no file", variant: "300x300", ext: "jpg"},
		{name: "no variant", fileID: testRef(t), ext: "jpg"},
		{name: "no extension", fileID: testRef(t), variant: "300x300"},
		{
			name: "a variant that escapes the path", fileID: testRef(t),
			variant: "../../etc/passwd", ext: "jpg",
		},
		{
			name: "a variant with a query", fileID: testRef(t),
			variant: "300x300?sig=x", ext: "jpg",
		},
		{name: "a slash in the extension", fileID: testRef(t), variant: "web", ext: "jpg/x"},
		{name: "an uppercase variant", fileID: testRef(t), variant: "Web", ext: "jpg"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := builder.VariantURL(test.fileID, test.variant, test.ext)

			require.ErrorIs(t, err, ErrUnsafeTarget,
				"a url is handed to a browser, so nothing unchecked may reach the path")
		})
	}
}

func TestNewRefusesAnAddressABrowserCannotFollow(t *testing.T) {
	t.Parallel()

	full := Config{
		PublicBase:  "https://domain.com",
		Placeholder: "https://domain.com/static/placeholder.png",
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "no base", mutate: func(c *Config) { c.PublicBase = "" }},
		{name: "no placeholder", mutate: func(c *Config) { c.Placeholder = "" }},
		{name: "a relative base", mutate: func(c *Config) { c.PublicBase = "/upload" }},
		{name: "a base without a host", mutate: func(c *Config) { c.PublicBase = "https://" }},
		{name: "a base with no scheme", mutate: func(c *Config) { c.PublicBase = "domain.com" }},
		{
			name:   "a placeholder with no scheme",
			mutate: func(c *Config) { c.Placeholder = "/static/placeholder.png" },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg := full
			test.mutate(&cfg)

			_, err := New(cfg)
			require.ErrorIs(t, err, ErrInvalidConfig)
		})
	}

	_, err := New(full)
	require.NoError(t, err)
}

func TestVariantURLDoesNotCareWhichUUIDItGets(t *testing.T) {
	t.Parallel()

	builder := testBuilder(t, "http://localhost:8080")

	ref, err := domain.ParseFileRef(uuid.Must(uuid.NewV7()).String())
	require.NoError(t, err)

	url, err := builder.VariantURL(ref, "100x100", "webp")

	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8080/upload/"+ref.String()+"/100x100.webp", url)
}
