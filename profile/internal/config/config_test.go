package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setCommonEnv(t *testing.T) {
	t.Helper()

	t.Setenv("POSTGRES_DSN",
		"postgres://profile:profile@localhost:5432/profile?sslmode=disable")
	t.Setenv("CONTENT_PUBLIC_BASE", "https://domain.com")
	t.Setenv("CONTENT_PLACEHOLDER", "https://domain.com/static/placeholder.png")
	t.Setenv("FILE_ADDR", "file:9090")
	t.Setenv("FILE_TOKEN", "0123456789abcdef0123456789abcdef")
	t.Setenv("HEALTH_ADDR", ":8081")
	t.Setenv("METRICS_ADDR", ":9100")
	t.Setenv("PPROF_ADDR", ":6060")
}

func setServerEnv(t *testing.T) {
	t.Helper()

	setCommonEnv(t)
	t.Setenv("HTTP_ADDR", ":8080")
}

func setWorkerEnv(t *testing.T) {
	t.Helper()

	setCommonEnv(t)
	t.Setenv("KAFKA_BROKERS", "kafka:9092")
	t.Setenv("KAFKA_GROUP", "profile.worker")
}

func TestLoadServerReadsItsSections(t *testing.T) {
	// Arrange
	setServerEnv(t)
	t.Setenv("REST_TIMEOUT", "7s")
	t.Setenv("LOG_LEVEL", "debug")

	// Act
	cfg, err := LoadServer()

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "debug", cfg.Log.Level)
	assert.Equal(t, ":8080", cfg.HTTP.Addr)
	assert.Equal(t, "file:9090", cfg.File.Addr)
	assert.Equal(t, "https://domain.com", cfg.URLs.PublicBase)
	assert.Equal(t, 7*time.Second, cfg.REST.Timeout)
	assert.Equal(t, ":9100", cfg.MetricsServer.Addr)
}

func TestLoadServerAppliesTheDefaults(t *testing.T) {
	// Arrange
	setServerEnv(t)

	// Act
	cfg, err := LoadServer()

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "info", cfg.Log.Level)
	assert.Equal(t, 5*time.Second, cfg.REST.Timeout)
	assert.Equal(t, 5*time.Second, cfg.File.Timeout)
	assert.Equal(t, int64(10<<20), cfg.Policy.MaxSizeBytes)
	assert.Equal(t, []string{"image/jpeg", "image/png", "image/webp"}, cfg.Policy.AllowedMime)
	assert.Equal(t, 20, cfg.Policy.MaxAvatarsPerUser)
	assert.Positive(t, cfg.Postgres.MaxConns)
}

func TestLoadWorkerReadsItsSections(t *testing.T) {
	// Arrange
	setWorkerEnv(t)
	t.Setenv("JOBS_WATCHDOG_INTERVAL", "90s")
	t.Setenv("JOBS_FAIL_STUCK_AFTER", "45m")
	t.Setenv("RELAY_BATCH_SIZE", "25")

	// Act
	cfg, err := LoadWorker()

	// Assert
	require.NoError(t, err)
	assert.Equal(t, []string{"kafka:9092"}, cfg.Kafka.Brokers)
	assert.Equal(t, "profile.worker", cfg.Kafka.Group)
	assert.Equal(t, 90*time.Second, cfg.Jobs.WatchdogInterval)
	assert.Equal(t, 45*time.Minute, cfg.Jobs.FailStuckAfter)
	assert.Equal(t, 25, cfg.Relay.BatchSize)
}

func TestLoadWorkerConsumesTheFactTopicByDefault(t *testing.T) {
	// Arrange
	setWorkerEnv(t)

	// Act
	cfg, err := LoadWorker()

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "gophprofile.file.v1", cfg.Kafka.Topic,
		"profile consumes the facts of file; it publishes its own by destination")
}

func TestTheWatchdogGivesUpLaterThanFileDoes(t *testing.T) {
	// Arrange
	setWorkerEnv(t)

	// Act
	cfg, err := LoadWorker()

	// Assert
	require.NoError(t, err)
	assert.Greater(t, cfg.Jobs.FailStuckAfter, 20*time.Minute,
		"file rejects a stale file after 15m and its job ticks every 5m, so giving up "+
			"earlier would overwrite the answer that is still on its way")
	assert.Less(t, cfg.Jobs.RecoverStaleAfter, cfg.Jobs.FailStuckAfter,
		"an abandoned upload can be finished, a stuck one can only be given up on")
}

func TestLoadRefusesAMissingRequiredVariable(t *testing.T) {
	required := []string{
		"POSTGRES_DSN",
		"CONTENT_PUBLIC_BASE",
		"CONTENT_PLACEHOLDER",
		"FILE_ADDR",
		"FILE_TOKEN",
	}

	for _, name := range required {
		t.Run(name, func(t *testing.T) {
			setServerEnv(t)
			t.Setenv(name, "")

			_, err := LoadServer()
			require.Error(t, err, "%s must not default to something silently wrong", name)
		})
	}

	t.Run("KAFKA_BROKERS", func(t *testing.T) {
		setWorkerEnv(t)
		t.Setenv("KAFKA_BROKERS", "")

		_, err := LoadWorker()
		require.Error(t, err)
	})
}

func TestLoadMigrateNeedsOnlyTheDatabase(t *testing.T) {
	// Arrange
	t.Setenv("POSTGRES_DSN",
		"postgres://profile:profile@localhost:5432/profile?sslmode=disable")

	// Act
	cfg, err := LoadMigrate()

	// Assert
	require.NoError(t, err,
		"the migration job runs before anything else exists, so it must not ask for more")
	assert.NotEmpty(t, cfg.Postgres.DSN)
	assert.Equal(t, "info", cfg.Log.Level)
}

// See the same pair in file: addresses are validated by httpserver.New now, and
// pprof is allowed to be unset on purpose.
func TestPprofIsOptionalAndOffByDefault(t *testing.T) {
	// Arrange
	setServerEnv(t)
	t.Setenv("PPROF_ADDR", "")

	// Act
	cfg, err := LoadServer()

	// Assert
	require.NoError(t, err)
	assert.True(t, cfg.PprofServer.Disabled())
	assert.False(t, cfg.MetricsServer.Disabled(), "metrics stay on")
}

func TestPprofTurnsOnWhenAnAddressIsGiven(t *testing.T) {
	// Arrange
	setServerEnv(t)
	t.Setenv("PPROF_ADDR", "127.0.0.1:6060")

	// Act
	cfg, err := LoadServer()

	// Assert
	require.NoError(t, err)
	assert.False(t, cfg.PprofServer.Disabled())
	assert.Equal(t, "127.0.0.1:6060", cfg.PprofServer.Addr)
}
