//go:build integration

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcrabbit "github.com/testcontainers/testcontainers-go/modules/rabbitmq"

	"github.com/a-aleesshin/gophprofile/file/internal/adapter/policy"
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/s3"
	"github.com/a-aleesshin/gophprofile/file/internal/config"
	"github.com/a-aleesshin/gophprofile/file/migrations"
	"github.com/a-aleesshin/gophprofile/internal/broker/kafka"
	"github.com/a-aleesshin/gophprofile/internal/broker/rabbitmq"
	"github.com/a-aleesshin/gophprofile/internal/health"
	"github.com/a-aleesshin/gophprofile/internal/httpserver"
	"github.com/a-aleesshin/gophprofile/internal/logging"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

const testBucket = "gophprofile"

var (
	postgresDSN string
	minioHost   string
	minioUser   string
	minioPass   string
	rabbitURL   string
	kafkaHosts  []string
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	postgres, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("file"),
		tcpostgres.WithUsername("file"),
		tcpostgres.WithPassword("file"),
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

	store, err := tcminio.Run(ctx, "minio/minio:latest")
	if err != nil {
		fmt.Fprintf(os.Stderr, "run minio: %v\n", err)
		os.Exit(1)
	}

	minioHost, err = store.ConnectionString(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "minio endpoint: %v\n", err)
		os.Exit(1)
	}

	minioUser, minioPass = store.Username, store.Password

	if err := applyMigrations(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}

	if err := createBucket(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "create bucket: %v\n", err)
		os.Exit(1)
	}

	rabbit, err := tcrabbit.Run(ctx, "rabbitmq:4-management-alpine")
	if err != nil {
		fmt.Fprintf(os.Stderr, "run rabbitmq: %v\n", err)
		os.Exit(1)
	}

	rabbitURL, err = rabbit.AmqpURL(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amqp url: %v\n", err)
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

	code := m.Run()

	for _, stop := range []func() error{
		func() error { return postgres.Terminate(ctx) },
		func() error { return store.Terminate(ctx) },
		func() error { return rabbit.Terminate(ctx) },
		func() error { return broker.Terminate(ctx) },
	} {
		if err := stop(); err != nil {
			fmt.Fprintf(os.Stderr, "terminate container: %v\n", err)
		}
	}

	os.Exit(code)
}

func applyMigrations(ctx context.Context) error {
	pool, err := pgxpool.New(ctx, postgresDSN)
	if err != nil {
		return err
	}

	defer pool.Close()

	return migrations.Up(ctx, pool)
}

func createBucket(ctx context.Context) error {
	client, err := minio.New(minioHost, &minio.Options{
		Creds: credentials.NewStaticV4(minioUser, minioPass, ""),
	})
	if err != nil {
		return err
	}

	return client.MakeBucket(ctx, testBucket, minio.MakeBucketOptions{})
}

func freePort(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	return addr
}

func workerConfig(t *testing.T) config.Worker {
	t.Helper()

	servers := func() httpserver.Config {
		return httpserver.Config{
			Addr:            freePort(t),
			ReadTimeout:     time.Second,
			WriteTimeout:    time.Second,
			IdleTimeout:     time.Second,
			ShutdownTimeout: time.Second,
		}
	}

	return config.Worker{
		Common: config.Common{
			Log: logging.Config{Level: "error", Format: logging.FormatJSON},
			Postgres: pgtx.Config{
				DSN:             postgresDSN,
				MaxConns:        5,
				MinConns:        1,
				MaxConnLifetime: time.Hour,
				MaxConnIdleTime: 30 * time.Minute,
				ConnectTimeout:  5 * time.Second,
			},
			S3: s3.Config{
				Endpoint:   minioHost,
				PublicBase: "http://" + minioHost,
				AccessKey:  minioUser,
				SecretKey:  minioPass,
				Bucket:     testBucket,
				Region:     "us-east-1",
			},
			Policies: policy.Config{Path: "../../configs/policies.yaml"},
		},
		Rabbit: rabbitmq.Config{
			URL:      rabbitURL,
			Exchange: "gophprofile.commands",
			Prefetch: 3,
			Ladder:   []time.Duration{time.Second, 4 * time.Second},
		},
		Kafka: kafka.Config{
			Brokers:      kafkaHosts,
			Topic:        "gophprofile.file.v1",
			Group:        "file",
			RetryBackoff: time.Second,
		},
		Relay: outbox.RelayConfig{
			BatchSize:              10,
			Interval:               200 * time.Millisecond,
			MinBackoff:             time.Second,
			MaxBackoff:             30 * time.Second,
			MaxConsecutiveFailures: 3,
			Lease:                  30 * time.Second,
		},
		Jobs: config.Jobs{
			ReconcileInterval: time.Minute,
			ReconcileLimit:    10,
			ReapInterval:      time.Minute,
			ReapLimit:         10,
			ReapStaleAfter:    15 * time.Minute,
		},
		Health:        health.Config{Timeout: 2 * time.Second},
		HealthServer:  servers(),
		MetricsServer: servers(),
		PprofServer:   servers(),
	}
}

func TestBuildWorkerStartsAndReportsReady(t *testing.T) {
	// Arrange
	cfg := workerConfig(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithCancel(context.Background())

	runner, shutdown, err := BuildWorker(ctx, cfg, logger)
	require.NoError(t, err, "the whole worker must assemble from configuration alone")

	defer shutdown()

	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	// Act
	var body struct {
		Status string `json:"status"`
		Checks map[string]struct {
			Status   string `json:"status"`
			Critical bool   `json:"critical"`
			Error    string `json:"error"`
		} `json:"checks"`
	}

	require.Eventually(t, func() bool {
		response, getErr := http.Get("http://" + cfg.HealthServer.Addr + "/health/ready")
		if getErr != nil {
			return false
		}

		defer func() { _ = response.Body.Close() }()

		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			return false
		}

		return response.StatusCode == http.StatusOK
	}, 30*time.Second, 200*time.Millisecond)

	// Assert
	assert.Equal(t, "ok", body.Status)
	assert.Equal(t, "ok", body.Checks["postgres"].Status)
	assert.Equal(t, "ok", body.Checks["s3"].Status)
	assert.Equal(t, "ok", body.Checks["rabbitmq"].Status)
	assert.Equal(t, "ok", body.Checks["kafka"].Status)

	response, err := http.Get("http://" + cfg.MetricsServer.Addr + "/metrics")
	require.NoError(t, err)

	defer func() { _ = response.Body.Close() }()

	metrics, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.Contains(t, string(metrics), "go_goroutines")

	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled,
			"cancelling the context must stop every runner")
	case <-time.After(30 * time.Second):
		t.Fatal("the worker did not stop after the context was cancelled")
	}
}
