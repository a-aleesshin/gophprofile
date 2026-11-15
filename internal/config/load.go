package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

func LoadSection[T any](dst *T, prefix string) error {
	if err := env.ParseWithOptions(dst, env.Options{Prefix: prefix}); err != nil {
		return fmt.Errorf("parse %q config: %w", prefix, err)
	}

	return nil
}
