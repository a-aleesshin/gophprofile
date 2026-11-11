package config

import (
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/s3"
	"github.com/a-aleesshin/gophprofile/internal/config"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

type Config struct {
	Postgres pgtx.Config
	S3       s3.Config
}

func Load() (Config, error) {
	var cfg Config

	for _, section := range []func() error{
		func() error { return config.LoadSection(&cfg.Postgres, "POSTGRES_") },
		func() error { return config.LoadSection(&cfg.S3, "S3_") },
	} {
		if err := section(); err != nil {
			return Config{}, err
		}
	}

	return cfg, nil
}
