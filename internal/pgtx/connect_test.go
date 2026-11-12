package pgtx

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validConfig() Config {
	return Config{
		DSN:             "postgres://file:file@localhost:5432/file?sslmode=disable",
		MaxConns:        10,
		MinConns:        2,
		MaxConnLifetime: time.Hour,
		MaxConnIdleTime: 30 * time.Minute,
		ConnectTimeout:  5 * time.Second,
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	t.Run("accepts a complete configuration", func(t *testing.T) {
		t.Parallel()

		assert.NoError(t, validConfig().validate())
	})

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "no dsn", mutate: func(c *Config) { c.DSN = "" }},
		{name: "zero max conns", mutate: func(c *Config) { c.MaxConns = 0 }},
		{name: "negative min conns", mutate: func(c *Config) { c.MinConns = -1 }},
		{name: "min above max", mutate: func(c *Config) { c.MinConns = c.MaxConns + 1 }},
		{name: "zero lifetime", mutate: func(c *Config) { c.MaxConnLifetime = 0 }},
		{name: "zero idle time", mutate: func(c *Config) { c.MaxConnIdleTime = 0 }},
		{
			name:   "zero connect timeout",
			mutate: func(c *Config) { c.ConnectTimeout = 0 },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			cfg := validConfig()
			test.mutate(&cfg)

			// Act
			err := cfg.validate()

			// Assert
			require.ErrorIs(t, err, ErrInvalidConfig,
				"a zero value must name itself, not surface as a mystery deadline")
		})
	}
}

func TestConnectRefusesABrokenConfig(t *testing.T) {
	t.Parallel()

	// Arrange
	cfg := validConfig()
	cfg.ConnectTimeout = 0

	// Act
	pool, err := Connect(t.Context(), cfg)

	// Assert
	require.ErrorIs(t, err, ErrInvalidConfig)
	assert.Nil(t, pool)
}
