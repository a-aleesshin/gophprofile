//go:build integration

package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.opentelemetry.io/otel"
)

type jaegerTrace struct {
	Spans []struct {
		OperationName string `json:"operationName"`
		ProcessID     string `json:"processID"`
	} `json:"spans"`
	Processes map[string]struct {
		ServiceName string `json:"serviceName"`
		Tags        []struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		} `json:"tags"`
	} `json:"processes"`
}

type jaegerTraces struct {
	Data []jaegerTrace `json:"data"`
}

func runJaeger(t *testing.T) (otlp string, query string) {
	t.Helper()

	ctx := context.Background()

	container, err := testcontainers.GenericContainer(ctx,
		testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        "jaegertracing/jaeger:2.12.0",
				ExposedPorts: []string{"4317/tcp", "16686/tcp"},
				WaitingFor: wait.ForHTTP("/").
					WithPort("16686/tcp").
					WithStartupTimeout(time.Minute),
			},
			Started: true,
		})
	require.NoError(t, err)

	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	otlpPort, err := container.MappedPort(ctx, "4317")
	require.NoError(t, err)

	queryPort, err := container.MappedPort(ctx, "16686")
	require.NoError(t, err)

	host, err := container.Host(ctx)
	require.NoError(t, err)

	return fmt.Sprintf("%s:%s", host, otlpPort.Port()),
		fmt.Sprintf("http://%s:%s", host, queryPort.Port())
}

func fetchTraces(t require.TestingT, query, service, operation string) jaegerTraces {
	url := fmt.Sprintf("%s/api/traces?service=%s&operation=%s&limit=10",
		query, service, operation)

	response, err := http.Get(url)
	require.NoError(t, err)

	defer func() { _ = response.Body.Close() }()

	require.Equal(t, http.StatusOK, response.StatusCode)

	var traces jaegerTraces
	require.NoError(t, json.NewDecoder(response.Body).Decode(&traces))

	return traces
}

func TestTracingDeliversASpanWithItsIdentity(t *testing.T) {
	// Arrange
	otlp, query := runJaeger(t)

	operation := "probe-" + uuid.NewString()

	flush, err := StartTracing(context.Background(), TracingConfig{
		Enabled:     true,
		Endpoint:    otlp,
		Insecure:    true,
		SampleRatio: 1,
		Timeout:     5 * time.Second,
		Version:     "test",
	}, Identity{Service: "file", Component: "worker"})
	require.NoError(t, err)

	// Act
	_, span := otel.Tracer("observability-test").Start(context.Background(), operation)
	span.End()

	require.NoError(t, flush(context.Background()),
		"shutdown has to flush the batch, or the last spans of a process are lost")

	// Assert
	var found jaegerTrace

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		traces := fetchTraces(c, query, "file", operation)

		if !assert.Len(c, traces.Data, 1) {
			return
		}

		found = traces.Data[0]
	}, 20*time.Second, 300*time.Millisecond,
		"the span must reach the collector through the exporter this package builds")

	require.Len(t, found.Spans, 1)
	assert.Equal(t, operation, found.Spans[0].OperationName)

	process, known := found.Processes[found.Spans[0].ProcessID]
	require.True(t, known)

	assert.Equal(t, "file", process.ServiceName,
		"service.name is what Jaeger groups by and what the dependency map draws")

	tags := make(map[string]any, len(process.Tags))
	for _, tag := range process.Tags {
		tags[tag.Key] = tag.Value
	}

	assert.Equal(t, "worker", tags["component"],
		"without component a server and a worker of one service are indistinguishable")
	assert.Equal(t, "test", tags["service.version"])
}
