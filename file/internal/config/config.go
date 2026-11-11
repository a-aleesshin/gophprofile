package config

import (
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/link"
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/policy"
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/s3"
	"github.com/a-aleesshin/gophprofile/internal/config"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

type Config struct {
	Postgres pgtx.Config
	S3       s3.Config
	Policies policy.Config
	Links    link.Config
}

func Load() (Config, error) {
	var cfg Config

	for _, section := range []func() error{
		func() error { return config.LoadSection(&cfg.Postgres, "POSTGRES_") },
		func() error { return config.LoadSection(&cfg.S3, "S3_") },
		func() error { return config.LoadSection(&cfg.Policies, "POLICIES_") },
		func() error { return config.LoadSection(&cfg.Links, "LINK_") },
	} {
		if err := section(); err != nil {
			return Config{}, err
		}
	}

	return cfg, nil
}
