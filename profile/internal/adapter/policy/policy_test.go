package policy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testConfig() Config {
	return Config{
		MaxSizeBytes:      10 << 20,
		AllowedMime:       []string{"image/jpeg", "image/png"},
		MaxAvatarsPerUser: 20,
	}
}

func TestProviderNormalisesTheAllowedTypes(t *testing.T) {
	t.Parallel()

	// Arrange
	cfg := testConfig()
	cfg.AllowedMime = []string{" IMAGE/JPEG ", "image/png", "image/jpeg"}

	// Act
	provider, err := New(cfg)
	require.NoError(t, err)

	policy := provider.UploadPolicy()

	// Assert
	assert.Equal(t, []string{"image/jpeg", "image/png"}, policy.AllowedMime,
		"the comparison in the use case is exact, so the list is lowered and deduped here")
	assert.Equal(t, int64(10<<20), policy.MaxSizeBytes)
	assert.Equal(t, 20, policy.MaxAvatarsPerUser)
}

func TestNewRefusesAPolicyThatCannotWork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "no size", mutate: func(c *Config) { c.MaxSizeBytes = 0 }},
		{name: "size below a kilobyte", mutate: func(c *Config) { c.MaxSizeBytes = 512 }},
		{name: "no types", mutate: func(c *Config) { c.AllowedMime = nil }},
		{
			name:   "an empty type",
			mutate: func(c *Config) { c.AllowedMime = []string{"image/jpeg", "  "} },
		},
		{
			name:   "a type that is not an image",
			mutate: func(c *Config) { c.AllowedMime = []string{"application/pdf"} },
		},
		{name: "no avatars allowed", mutate: func(c *Config) { c.MaxAvatarsPerUser = 0 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg := testConfig()
			test.mutate(&cfg)

			_, err := New(cfg)
			require.ErrorIs(t, err, ErrInvalidConfig)
		})
	}
}
