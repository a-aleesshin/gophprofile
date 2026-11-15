package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

var ErrInvalidConfig = errors.New("invalid logging config")

const (
	FormatJSON = "json"
	FormatText = "text"
)

type Config struct {
	Level  string `env:"LEVEL" envDefault:"info"`
	Format string `env:"FORMAT" envDefault:"json"`
}

var levels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

func New(cfg Config, out io.Writer) (*slog.Logger, error) {
	level, known := levels[strings.ToLower(strings.TrimSpace(cfg.Level))]
	if !known {
		return nil, fmt.Errorf("%w: unknown level %q", ErrInvalidConfig, cfg.Level)
	}

	options := &slog.HandlerOptions{Level: level}

	switch strings.ToLower(strings.TrimSpace(cfg.Format)) {
	case FormatJSON:
		return slog.New(slog.NewJSONHandler(out, options)), nil
	case FormatText:
		return slog.New(slog.NewTextHandler(out, options)), nil
	default:
		return nil, fmt.Errorf("%w: unknown format %q", ErrInvalidConfig, cfg.Format)
	}
}
