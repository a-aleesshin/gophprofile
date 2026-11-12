package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setCommonEnv(t *testing.T) {
	t.Helper()

	t.Setenv("POSTGRES_DSN", "postgres://file:file@localhost:5432/file?sslmode=disable")
	t.Setenv("S3_ENDPOINT", "minio:9000")
	t.Setenv("S3_PUBLIC_BASE", "https://domain.com/s3")
	t.Setenv("S3_ACCESS_KEY", "key")
	t.Setenv("S3_SECRET_KEY", "secret")
	t.Setenv("S3_BUCKET", "gophprofile")
	t.Setenv("POLICIES_PATH", "configs/policies.yaml")
}

func setWorkerEnv(t *testing.T) {
	t.Helper()

	setCommonEnv(t)
	t.Setenv("RABBIT_URL", "amqp://guest:guest@rabbitmq:5672/")
	t.Setenv("KAFKA_BROKERS", "kafka:9092")
	t.Setenv("HEALTH_ADDR", ":8081")
	t.Setenv("METRICS_ADDR", ":9100")
	t.Setenv("PPROF_ADDR", ":6060")
}

func setServerEnv(t *testing.T) {
	t.Helper()

	setCommonEnv(t)
	t.Setenv("LINK_PUBLIC_BASE", "https://domain.com")
	t.Setenv("LINK_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("GRPC_ADDR", ":9090")
	t.Setenv("HTTP_ADDR", ":8080")
	t.Setenv("HEALTH_ADDR", ":8081")
	t.Setenv("METRICS_ADDR", ":9100")
	t.Setenv("PPROF_ADDR", ":6060")
}

func TestLoadWorker(t *testing.T) {
	setWorkerEnv(t)

	cfg, err := LoadWorker()
	require.NoError(t, err)

	assert.Equal(t, "postgres://file:file@localhost:5432/file?sslmode=disable", cfg.Postgres.DSN)
	assert.Equal(t, int32(10), cfg.Postgres.MaxConns)
	assert.Equal(t, time.Hour, cfg.Postgres.MaxConnLifetime, "durations come from the tag default")

	assert.Equal(t, "minio:9000", cfg.S3.Endpoint)
	assert.Equal(t, "us-east-1", cfg.S3.Region, "the default comes from the struct tag")
	assert.Equal(t, "configs/policies.yaml", cfg.Policies.Path)

	assert.Equal(t, "info", cfg.Log.Level)
	assert.Equal(t, "json", cfg.Log.Format)

	assert.Equal(t, "gophprofile.commands", cfg.Rabbit.Exchange)
	assert.Equal(t, 3, cfg.Rabbit.Prefetch)
	assert.Equal(t, []time.Duration{
		time.Second, 4 * time.Second, 16 * time.Second, 64 * time.Second, 256 * time.Second,
	}, cfg.Rabbit.Ladder, "the retry ladder comes from the struct tag")

	assert.Equal(t, []string{"kafka:9092"}, cfg.Kafka.Brokers)
	assert.Equal(t, "gophprofile.file.v1", cfg.Kafka.Topic)

	assert.Equal(t, 100, cfg.Relay.BatchSize)
	assert.Equal(t, 3, cfg.Relay.MaxConsecutiveFailures)

	assert.Equal(t, time.Minute, cfg.Jobs.ReconcileInterval)
	assert.Equal(t, 5*time.Minute, cfg.Jobs.ReapInterval)
	assert.Equal(t, 15*time.Minute, cfg.Jobs.ReapStaleAfter)

	assert.Equal(t, 2*time.Second, cfg.Health.Timeout)
	assert.Equal(t, ":8081", cfg.HealthServer.Addr)
	assert.Equal(t, ":9100", cfg.MetricsServer.Addr)
	assert.Equal(t, ":6060", cfg.PprofServer.Addr)
	assert.Equal(t, 10*time.Second, cfg.HealthServer.ShutdownTimeout)
}

func TestLoadWorkerNeedsNoLinkSecret(t *testing.T) {
	setWorkerEnv(t)

	_, err := LoadWorker()
	require.NoError(t, err,
		"the worker builds no links, so it must not demand the signing secret")
}

func TestLoadServerNeedsNoBrokers(t *testing.T) {
	setServerEnv(t)

	cfg, err := LoadServer()
	require.NoError(t, err,
		"the server talks to no broker, so it must not demand broker addresses")

	assert.Equal(t, "https://domain.com", cfg.Links.PublicBase)
	assert.Equal(t, "0123456789abcdef0123456789abcdef", cfg.Links.Secret)
	assert.Empty(t, cfg.Links.PreviousSecret, "rotation is off until a second secret is set")

	assert.Equal(t, ":9090", cfg.GRPC.Addr)
	assert.Equal(t, 4194304, cfg.GRPC.MaxRecvMsgSize, "the default comes from the struct tag")
	assert.Equal(t, ":8080", cfg.HTTP.Addr)
	assert.Equal(t, 3*time.Second, cfg.Serving.Timeout)
	assert.Equal(t, 2*time.Second, cfg.Health.Timeout)
}

func TestLoadReadsTheSectionPrefix(t *testing.T) {
	setWorkerEnv(t)
	t.Setenv("ENDPOINT", "wrong:9000")
	t.Setenv("S3_ENDPOINT", "right:9000")

	cfg, err := LoadWorker()
	require.NoError(t, err)
	assert.Equal(t, "right:9000", cfg.S3.Endpoint,
		"an unprefixed variable must not leak into a prefixed section")
}

func TestLoadParsesNonStringFields(t *testing.T) {
	setWorkerEnv(t)
	t.Setenv("S3_USE_SSL", "true")
	t.Setenv("RELAY_BATCH_SIZE", "25")
	t.Setenv("RABBIT_RETRY_LADDER", "500ms,2s")
	t.Setenv("KAFKA_BROKERS", "one:9092,two:9092")

	cfg, err := LoadWorker()
	require.NoError(t, err)

	assert.True(t, cfg.S3.UseSSL)
	assert.Equal(t, 25, cfg.Relay.BatchSize)
	assert.Equal(t, []time.Duration{500 * time.Millisecond, 2 * time.Second}, cfg.Rabbit.Ladder)
	assert.Equal(t, []string{"one:9092", "two:9092"}, cfg.Kafka.Brokers)
}

func TestLoadWorkerRequiresEveryMandatoryVariable(t *testing.T) {
	for _, missing := range []string{
		"POSTGRES_DSN",
		"S3_ENDPOINT",
		"S3_PUBLIC_BASE",
		"S3_ACCESS_KEY",
		"S3_SECRET_KEY",
		"S3_BUCKET",
		"POLICIES_PATH",
		"RABBIT_URL",
		"KAFKA_BROKERS",
		"HEALTH_ADDR",
		"METRICS_ADDR",
		"PPROF_ADDR",
	} {
		t.Run(missing, func(t *testing.T) {
			setWorkerEnv(t)
			t.Setenv(missing, "")

			_, err := LoadWorker()
			require.Error(t, err,
				"a missing variable must fail at startup, not at the first request")
		})
	}
}

func TestLoadServerRequiresEveryMandatoryVariable(t *testing.T) {
	for _, missing := range []string{
		"POSTGRES_DSN",
		"S3_BUCKET",
		"POLICIES_PATH",
		"LINK_PUBLIC_BASE",
		"LINK_SECRET",
		"GRPC_ADDR",
		"HTTP_ADDR",
		"HEALTH_ADDR",
		"METRICS_ADDR",
		"PPROF_ADDR",
	} {
		t.Run(missing, func(t *testing.T) {
			setServerEnv(t)
			t.Setenv(missing, "")

			_, err := LoadServer()
			require.Error(t, err)
		})
	}
}
