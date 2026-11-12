package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWritesJSON(t *testing.T) {
	t.Parallel()

	// Arrange
	var out bytes.Buffer

	logger, err := New(Config{Level: "info", Format: FormatJSON}, &out)
	require.NoError(t, err)

	// Act
	logger.Info("started", "component", "worker")

	// Assert
	var entry map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &entry))
	assert.Equal(t, "started", entry["msg"])
	assert.Equal(t, "worker", entry["component"])
}

func TestNewWritesText(t *testing.T) {
	t.Parallel()

	// Arrange
	var out bytes.Buffer

	logger, err := New(Config{Level: "debug", Format: FormatText}, &out)
	require.NoError(t, err)

	// Act
	logger.Debug("looking around")

	// Assert
	assert.Contains(t, out.String(), "msg=\"looking around\"")
}

func TestNewRespectsTheLevel(t *testing.T) {
	t.Parallel()

	// Arrange
	var out bytes.Buffer

	logger, err := New(Config{Level: "WARN", Format: FormatJSON}, &out)
	require.NoError(t, err)

	// Act
	logger.Info("ignored")
	logger.Warn("kept")

	// Assert
	assert.NotContains(t, out.String(), "ignored")
	assert.Contains(t, out.String(), "kept")
}

func TestNewRejectsUnknownSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "unknown level", cfg: Config{Level: "chatty", Format: FormatJSON}},
		{name: "unknown format", cfg: Config{Level: "info", Format: "yaml"}},
		{name: "empty level", cfg: Config{Format: FormatJSON}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Act
			logger, err := New(test.cfg, &bytes.Buffer{})

			// Assert
			require.ErrorIs(t, err, ErrInvalidConfig)
			assert.Nil(t, logger)
		})
	}
}

func TestNewAcceptsEveryLevelName(t *testing.T) {
	t.Parallel()

	expected := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}

	for name, level := range expected {
		// Act
		logger, err := New(Config{Level: name, Format: FormatJSON}, &bytes.Buffer{})

		// Assert
		require.NoError(t, err, name)
		assert.True(t, logger.Enabled(t.Context(), level))
	}
}
