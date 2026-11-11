package config

import (
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/s3"
	"github.com/a-aleesshin/gophprofile/internal/config"
)

type Config struct {
	S3 s3.Config
}

func Load() (Config, error) {
	var cfg Config

	if err := config.LoadSection(&cfg.S3, "S3_"); err != nil {
		return Config{}, err
	}

	return cfg, nil
}
