package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setS3Env(t *testing.T) {
	t.Helper()

	t.Setenv("S3_ENDPOINT", "minio:9000")
	t.Setenv("S3_PUBLIC_BASE", "https://domain.com/s3")
	t.Setenv("S3_ACCESS_KEY", "key")
	t.Setenv("S3_SECRET_KEY", "secret")
	t.Setenv("S3_BUCKET", "gophprofile")
}

func TestLoad(t *testing.T) {
	setS3Env(t)

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "minio:9000", cfg.S3.Endpoint)
	assert.Equal(t, "https://domain.com/s3", cfg.S3.PublicBase)
	assert.Equal(t, "gophprofile", cfg.S3.Bucket)
	assert.Equal(t, "us-east-1", cfg.S3.Region, "the default comes from the struct tag")
	assert.False(t, cfg.S3.UseSSL)
}

func TestLoad_ReadsTheSectionPrefix(t *testing.T) {
	setS3Env(t)
	t.Setenv("ENDPOINT", "wrong:9000")
	t.Setenv("S3_ENDPOINT", "right:9000")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "right:9000", cfg.S3.Endpoint,
		"an unprefixed variable must not leak into a prefixed section")
}

func TestLoad_ParsesNonStringFields(t *testing.T) {
	setS3Env(t)
	t.Setenv("S3_USE_SSL", "true")
	t.Setenv("S3_REGION", "eu-central-1")

	cfg, err := Load()
	require.NoError(t, err)
	assert.True(t, cfg.S3.UseSSL)
	assert.Equal(t, "eu-central-1", cfg.S3.Region)
}

func TestLoad_RequiresEveryMandatoryVariable(t *testing.T) {
	for _, missing := range []string{
		"S3_ENDPOINT",
		"S3_PUBLIC_BASE",
		"S3_ACCESS_KEY",
		"S3_SECRET_KEY",
		"S3_BUCKET",
	} {
		t.Run(missing, func(t *testing.T) {
			setS3Env(t)
			t.Setenv(missing, "")

			_, err := Load()
			require.Error(t, err,
				"a missing variable must fail at startup, not at the first request")
			assert.Contains(t, err.Error(), "S3_")
		})
	}
}
