package rabbitmq

import (
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validConfig() Config {
	return Config{
		URL:      "amqp://guest:guest@localhost:5672/",
		Exchange: "gophprofile.commands",
		Prefetch: 3,
		Ladder:   []time.Duration{time.Second, 4 * time.Second},
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	t.Run("accepts a growing ladder", func(t *testing.T) {
		t.Parallel()

		assert.NoError(t, validConfig().validate())
	})

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "no url", mutate: func(c *Config) { c.URL = "" }},
		{name: "no exchange", mutate: func(c *Config) { c.Exchange = "" }},
		{name: "zero prefetch", mutate: func(c *Config) { c.Prefetch = 0 }},
		{name: "negative prefetch", mutate: func(c *Config) { c.Prefetch = -1 }},
		{name: "empty ladder", mutate: func(c *Config) { c.Ladder = nil }},
		{
			name:   "a step that is not positive",
			mutate: func(c *Config) { c.Ladder = []time.Duration{0} },
		},
		{
			name: "a ladder that does not grow",
			mutate: func(c *Config) {
				c.Ladder = []time.Duration{4 * time.Second, time.Second}
			},
		},
		{
			name: "a ladder that repeats a step",
			mutate: func(c *Config) {
				c.Ladder = []time.Duration{time.Second, time.Second}
			},
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
			require.ErrorIs(t, err, ErrInvalidConfig)
		})
	}
}

func TestConfigNames(t *testing.T) {
	t.Parallel()

	cfg := validConfig()

	// Assert
	assert.Equal(t, "gophprofile.commands.dlx", cfg.deadLetterExchange())
	assert.Equal(t, "gophprofile.commands.dlq", cfg.deadLetterQueue())
	assert.Equal(t, "gophprofile.commands.file.process", cfg.commandQueue("file.process"))
	assert.Equal(t, "gophprofile.commands.retry.0", cfg.retryExchange(0))
	assert.Equal(t, "gophprofile.commands.retry.1", cfg.retryQueue(1))
}

func TestAttemptOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		headers amqp.Table
		want    int
	}{
		{name: "no headers", want: 0},
		{name: "no counter", headers: amqp.Table{"other": 1}, want: 0},
		{name: "int32 as the broker returns it", headers: amqp.Table{headerAttempt: int32(2)}, want: 2},
		{name: "int64", headers: amqp.Table{headerAttempt: int64(3)}, want: 3},
		{name: "int", headers: amqp.Table{headerAttempt: 4}, want: 4},
		{
			name:    "a value of an unexpected type",
			headers: amqp.Table{headerAttempt: "two"},
			want:    0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Act
			got := attemptOf(test.headers)

			// Assert
			assert.Equal(t, test.want, got)
		})
	}
}

func TestCloneHeadersDoesNotTouchTheOriginal(t *testing.T) {
	t.Parallel()

	// Arrange
	original := amqp.Table{headerAttempt: int32(1)}

	// Act
	cloned := cloneHeaders(original)
	cloned[headerAttempt] = int32(2)
	cloned[headerDropReason] = "because"

	// Assert
	assert.Equal(t, int32(1), original[headerAttempt])
	assert.NotContains(t, original, headerDropReason)
}
