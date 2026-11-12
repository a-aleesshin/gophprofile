//go:build integration

package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	filev1 "github.com/a-aleesshin/gophprofile/file/gen/file/v1"
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/link"
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/policy"
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/s3"
	"github.com/a-aleesshin/gophprofile/file/internal/config"
	"github.com/a-aleesshin/gophprofile/file/internal/transport/serving"
	"github.com/a-aleesshin/gophprofile/internal/grpcserver"
	"github.com/a-aleesshin/gophprofile/internal/health"
	"github.com/a-aleesshin/gophprofile/internal/httpserver"
	"github.com/a-aleesshin/gophprofile/internal/logging"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

func serverConfig(t *testing.T) config.Server {
	t.Helper()

	httpConfig := func() httpserver.Config {
		return httpserver.Config{
			Addr:            freePort(t),
			ReadTimeout:     time.Second,
			WriteTimeout:    time.Second,
			IdleTimeout:     time.Second,
			ShutdownTimeout: time.Second,
		}
	}

	return config.Server{
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
		Links: link.Config{
			PublicBase: "https://domain.com",
			Secret:     "0123456789abcdef0123456789abcdef",
		},
		Serving:       serving.Config{Timeout: 2 * time.Second},
		GRPC:          grpcserver.Config{Addr: freePort(t), ShutdownTimeout: time.Second, MaxRecvMsgSize: 1 << 20},
		HTTP:          httpConfig(),
		Health:        health.Config{Timeout: 2 * time.Second},
		HealthServer:  httpConfig(),
		MetricsServer: httpConfig(),
		PprofServer:   httpConfig(),
	}
}

func TestBuildServerStartsAndAnswers(t *testing.T) {
	// Arrange
	cfg := serverConfig(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithCancel(context.Background())

	runner, shutdown, err := BuildServer(ctx, cfg, logger)
	require.NoError(t, err, "the server must assemble without touching a broker")

	defer shutdown()

	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	conn, err := grpc.NewClient(cfg.GRPC.Addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	// Act
	var body struct {
		Status string `json:"status"`
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

	t.Run("grpc answers from the database", func(t *testing.T) {
		client := filev1.NewFileServiceClient(conn)

		_, err := client.GetFile(t.Context(), &filev1.GetFileRequest{
			FileId: "0193abcd-ef01-7a23-8b45-0123456789ab",
		})

		assert.Equal(t, codes.NotFound, status.Code(err),
			"an unknown file must come back as NOT_FOUND, not as an internal error")
	})

	t.Run("grpc health reports serving", func(t *testing.T) {
		client := healthpb.NewHealthClient(conn)

		require.Eventually(t, func() bool {
			response, checkErr := client.Check(t.Context(), &healthpb.HealthCheckRequest{
				Service: filev1.FileService_ServiceDesc.ServiceName,
			})

			return checkErr == nil &&
				response.GetStatus() == healthpb.HealthCheckResponse_SERVING
		}, 15*time.Second, 200*time.Millisecond)
	})

	t.Run("the public endpoint answers from the database", func(t *testing.T) {
		response, err := http.Get("http://" + cfg.HTTP.Addr +
			"/upload/0193abcd-ef01-7a23-8b45-0123456789ab/300x300.jpg")
		require.NoError(t, err)

		defer func() { _ = response.Body.Close() }()

		assert.Equal(t, http.StatusNotFound, response.StatusCode)
		assert.Equal(t, "nosniff", response.Header.Get("X-Content-Type-Options"))
	})

	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled,
			"cancelling the context must stop every runner")
	case <-time.After(30 * time.Second):
		t.Fatal("the server did not stop after the context was cancelled")
	}
}
