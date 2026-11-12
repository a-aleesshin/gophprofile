//go:build integration

package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/a-aleesshin/gophprofile/internal/broker/kafka"
	"github.com/a-aleesshin/gophprofile/internal/health"
	"github.com/a-aleesshin/gophprofile/internal/httpserver"
	"github.com/a-aleesshin/gophprofile/internal/inbox"
	"github.com/a-aleesshin/gophprofile/internal/logging"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
	"github.com/a-aleesshin/gophprofile/profile/internal/adapter/filegrpc"
	"github.com/a-aleesshin/gophprofile/profile/internal/adapter/policy"
	"github.com/a-aleesshin/gophprofile/profile/internal/adapter/urls"
	"github.com/a-aleesshin/gophprofile/profile/internal/config"
	"github.com/a-aleesshin/gophprofile/profile/internal/transport/rest"
	"github.com/a-aleesshin/gophprofile/profile/migrations"
)

const (
	testFileToken = "0123456789abcdef0123456789abcdef"

	factTopic  = "gophprofile.file.v1"
	eventTopic = "gophprofile.profile.v1"
)

var (
	postgresDSN string
	kafkaHosts  []string
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	postgres, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("profile"),
		tcpostgres.WithUsername("profile"),
		tcpostgres.WithPassword("profile"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run postgres: %v\n", err)
		os.Exit(1)
	}

	postgresDSN, err = postgres.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres dsn: %v\n", err)
		os.Exit(1)
	}

	if err := applyMigrations(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}

	broker, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1")
	if err != nil {
		fmt.Fprintf(os.Stderr, "run kafka: %v\n", err)
		os.Exit(1)
	}

	kafkaHosts, err = broker.Brokers(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kafka brokers: %v\n", err)
		os.Exit(1)
	}

	// The broker does not create topics on demand, and compose has kafka-init
	// for exactly this reason: without them the relay only logs a failure.
	if err := createTopics(ctx, factTopic, eventTopic); err != nil {
		fmt.Fprintf(os.Stderr, "create topics: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	for _, stop := range []func() error{
		func() error { return postgres.Terminate(ctx) },
		func() error { return broker.Terminate(ctx) },
	} {
		if err := stop(); err != nil {
			fmt.Fprintf(os.Stderr, "terminate: %v\n", err)
		}
	}

	os.Exit(code)
}

func createTopics(ctx context.Context, topics ...string) error {
	client, err := kgo.NewClient(kgo.SeedBrokers(kafkaHosts...))
	if err != nil {
		return fmt.Errorf("open kafka client: %w", err)
	}

	defer client.Close()

	if _, err := kadm.NewClient(client).CreateTopics(ctx, 1, 1, nil, topics...); err != nil {
		return fmt.Errorf("create %v: %w", topics, err)
	}

	return nil
}

func applyMigrations(ctx context.Context) error {
	pool, err := pgxpool.New(ctx, postgresDSN)
	if err != nil {
		return fmt.Errorf("new pool: %w", err)
	}

	defer pool.Close()

	return migrations.Up(ctx, pool)
}

func freePort(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	return addr
}

func testServers(t *testing.T) httpserver.Config {
	t.Helper()

	return httpserver.Config{
		Addr:            freePort(t),
		ReadTimeout:     time.Second,
		WriteTimeout:    time.Second,
		IdleTimeout:     time.Second,
		ShutdownTimeout: time.Second,
	}
}

func testCommon() config.Common {
	return config.Common{
		Log: logging.Config{Level: "error", Format: logging.FormatJSON},
		Postgres: pgtx.Config{
			DSN:             postgresDSN,
			MaxConns:        5,
			MinConns:        1,
			MaxConnLifetime: time.Hour,
			MaxConnIdleTime: 30 * time.Minute,
			ConnectTimeout:  5 * time.Second,
		},
		URLs: urls.Config{
			PublicBase:  "https://domain.com",
			Placeholder: "https://domain.com/static/placeholder.png",
		},
		Policy: policy.Config{
			MaxSizeBytes:      10 << 20,
			AllowedMime:       []string{"image/jpeg", "image/png", "image/webp"},
			MaxAvatarsPerUser: 20,
		},
	}
}

func testObservability(t *testing.T) config.Observability {
	t.Helper()

	return config.Observability{
		Health:        health.Config{Timeout: 2 * time.Second},
		HealthServer:  testServers(t),
		MetricsServer: testServers(t),
		PprofServer:   testServers(t),
	}
}

func serverConfig(t *testing.T) config.Server {
	t.Helper()

	return config.Server{
		Common:        testCommon(),
		Observability: testObservability(t),
		// The client is lazy, so an address nobody listens on is enough to
		// assemble the server: it only matters once an upload is started.
		File: filegrpc.Config{
			Addr: freePort(t), Token: testFileToken, Timeout: time.Second,
		},
		REST: rest.Config{Timeout: 2 * time.Second},
		HTTP: testServers(t),
	}
}

func workerConfig(t *testing.T) config.Worker {
	t.Helper()

	return config.Worker{
		Common:        testCommon(),
		Observability: testObservability(t),
		File: filegrpc.Config{
			Addr: freePort(t), Token: testFileToken, Timeout: time.Second,
		},
		Kafka: kafka.Config{
			Brokers:      kafkaHosts,
			Topic:        factTopic,
			Group:        "profile.worker.test",
			RetryBackoff: 100 * time.Millisecond,
		},
		Relay: outbox.RelayConfig{
			Interval:               100 * time.Millisecond,
			BatchSize:              10,
			Lease:                  5 * time.Second,
			MinBackoff:             100 * time.Millisecond,
			MaxBackoff:             time.Second,
			MaxConsecutiveFailures: 3,
		},
		Jobs: config.Jobs{
			BacklogInterval:   200 * time.Millisecond,
			WatchdogInterval:  200 * time.Millisecond,
			WatchdogLimit:     10,
			RecoverStaleAfter: 10 * time.Minute,
			FailStuckAfter:    30 * time.Minute,
			PruneInterval:     time.Hour,
		},
		OutboxPrune: outbox.PrunerConfig{Retention: time.Hour, BatchSize: 100},
		InboxPrune:  inbox.PrunerConfig{Retention: time.Hour, BatchSize: 100},
	}
}
