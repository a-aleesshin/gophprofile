package config

import (
	"time"

	"github.com/a-aleesshin/gophprofile/file/internal/adapter/link"
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/policy"
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/s3"
	"github.com/a-aleesshin/gophprofile/file/internal/transport/serving"
	"github.com/a-aleesshin/gophprofile/internal/broker/kafka"
	"github.com/a-aleesshin/gophprofile/internal/broker/rabbitmq"
	"github.com/a-aleesshin/gophprofile/internal/config"
	"github.com/a-aleesshin/gophprofile/internal/grpcserver"
	"github.com/a-aleesshin/gophprofile/internal/health"
	"github.com/a-aleesshin/gophprofile/internal/httpserver"
	"github.com/a-aleesshin/gophprofile/internal/logging"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

type Common struct {
	Log      logging.Config
	Postgres pgtx.Config
	S3       s3.Config
	Policies policy.Config
}

type Jobs struct {
	ReconcileInterval time.Duration `env:"RECONCILE_INTERVAL" envDefault:"1m"`
	ReconcileLimit    int           `env:"RECONCILE_LIMIT" envDefault:"100"`
	ReapInterval      time.Duration `env:"REAP_INTERVAL" envDefault:"5m"`
	ReapLimit         int           `env:"REAP_LIMIT" envDefault:"100"`
	ReapStaleAfter    time.Duration `env:"REAP_STALE_AFTER" envDefault:"15m"`
}

type Worker struct {
	Common

	Rabbit rabbitmq.Config
	Kafka  kafka.Config
	Relay  outbox.RelayConfig
	Jobs   Jobs

	Health        health.Config
	HealthServer  httpserver.Config
	MetricsServer httpserver.Config
	PprofServer   httpserver.Config
}

func loadCommon(dst *Common) error {
	for _, section := range []func() error{
		func() error { return config.LoadSection(&dst.Log, "LOG_") },
		func() error { return config.LoadSection(&dst.Postgres, "POSTGRES_") },
		func() error { return config.LoadSection(&dst.S3, "S3_") },
		func() error { return config.LoadSection(&dst.Policies, "POLICIES_") },
	} {
		if err := section(); err != nil {
			return err
		}
	}

	return nil
}

type Migrate struct {
	Log      logging.Config
	Postgres pgtx.Config
}

func LoadMigrate() (Migrate, error) {
	var cfg Migrate

	for _, section := range []func() error{
		func() error { return config.LoadSection(&cfg.Log, "LOG_") },
		func() error { return config.LoadSection(&cfg.Postgres, "POSTGRES_") },
	} {
		if err := section(); err != nil {
			return Migrate{}, err
		}
	}

	return cfg, nil
}

func LoadWorker() (Worker, error) {
	var cfg Worker

	if err := loadCommon(&cfg.Common); err != nil {
		return Worker{}, err
	}

	for _, section := range []func() error{
		func() error { return config.LoadSection(&cfg.Rabbit, "RABBIT_") },
		func() error { return config.LoadSection(&cfg.Kafka, "KAFKA_") },
		func() error { return config.LoadSection(&cfg.Relay, "RELAY_") },
		func() error { return config.LoadSection(&cfg.Jobs, "JOBS_") },
		func() error { return config.LoadSection(&cfg.Health, "HEALTH_") },
		func() error { return config.LoadSection(&cfg.HealthServer, "HEALTH_") },
		func() error { return config.LoadSection(&cfg.MetricsServer, "METRICS_") },
		func() error { return config.LoadSection(&cfg.PprofServer, "PPROF_") },
	} {
		if err := section(); err != nil {
			return Worker{}, err
		}
	}

	return cfg, nil
}

type Server struct {
	Common

	Links   link.Config
	Serving serving.Config
	GRPC    grpcserver.Config
	HTTP    httpserver.Config

	Health        health.Config
	HealthServer  httpserver.Config
	MetricsServer httpserver.Config
	PprofServer   httpserver.Config
}

func LoadServer() (Server, error) {
	var cfg Server

	if err := loadCommon(&cfg.Common); err != nil {
		return Server{}, err
	}

	for _, section := range []func() error{
		func() error { return config.LoadSection(&cfg.Links, "LINK_") },
		func() error { return config.LoadSection(&cfg.Serving, "SERVING_") },
		func() error { return config.LoadSection(&cfg.GRPC, "GRPC_") },
		func() error { return config.LoadSection(&cfg.HTTP, "HTTP_") },
		func() error { return config.LoadSection(&cfg.Health, "HEALTH_") },
		func() error { return config.LoadSection(&cfg.HealthServer, "HEALTH_") },
		func() error { return config.LoadSection(&cfg.MetricsServer, "METRICS_") },
		func() error { return config.LoadSection(&cfg.PprofServer, "PPROF_") },
	} {
		if err := section(); err != nil {
			return Server{}, err
		}
	}

	return cfg, nil
}
