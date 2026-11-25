package metrics_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tguidoux/openssm/apps/go/server/metrics"
)

// newTestCollector creates a Collector with a private registry for testing.
func newTestCollector() (*metrics.Collector, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	c := metrics.NewCollectorWithRegistry(reg)
	return c, reg
}

// scrapeMetrics gathers all metrics from the registry and returns them as text.
func scrapeMetrics(t *testing.T, reg *prometheus.Registry) string {
	t.Helper()
	mfs, err := reg.Gather()
	require.NoError(t, err)

	var sb strings.Builder
	for _, mf := range mfs {
		// Write metric family name
		sb.WriteString(mf.GetName())
		sb.WriteString("\n")
		for _, m := range mf.GetMetric() {
			// Write labels
			labels := make([]string, 0, len(m.GetLabel()))
			for _, lp := range m.GetLabel() {
				labels = append(labels, lp.GetName()+"=\""+lp.GetValue()+"\"")
			}
			labelStr := ""
			if len(labels) > 0 {
				labelStr = "{" + strings.Join(labels, ",") + "}"
			}

			if m.GetCounter() != nil {
				sb.WriteString(mf.GetName())
				sb.WriteString(labelStr)
				sb.WriteString(" ")
				sb.WriteString(formatFloat(m.GetCounter().GetValue()))
				sb.WriteString("\n")
			}
			if m.GetGauge() != nil {
				sb.WriteString(mf.GetName())
				sb.WriteString(labelStr)
				sb.WriteString(" ")
				sb.WriteString(formatFloat(m.GetGauge().GetValue()))
				sb.WriteString("\n")
			}
			if m.GetHistogram() != nil {
				sb.WriteString(mf.GetName())
				sb.WriteString("_count")
				sb.WriteString(labelStr)
				sb.WriteString(" ")
				sb.WriteString(formatFloat(float64(m.GetHistogram().GetSampleCount())))
				sb.WriteString("\n")
			}
		}
	}
	return sb.String()
}

func formatFloat(f float64) string {
	// Simple formatting: if whole number, show as integer
	if f == float64(int64(f)) {
		return formatInt(int64(f))
	}
	// Fallback for non-whole numbers
	return "0"
}

func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// metricContains checks if a specific metric+labels combination exists in the text output.
func metricContains(text, metricName string, labels map[string]string) bool {
	// Build expected label string
	labelParts := make([]string, 0, len(labels))
	for k, v := range labels {
		labelParts = append(labelParts, k+"=\""+v+"\"")
	}
	expected := metricName + "{" + strings.Join(labelParts, ",") + "}"
	return strings.Contains(text, expected)
}

// --- Collector Tests ---

func TestCollector_APIRequest(t *testing.T) {
	c, reg := newTestCollector()

	c.IncAPIRequest("GetParameter")
	c.IncAPIRequest("GetParameter")
	c.IncAPIRequest("PutParameter")

	text := scrapeMetrics(t, reg)
	assert.Contains(t, text, `openssm_api_requests_total{action="GetParameter"} 2`)
	assert.Contains(t, text, `openssm_api_requests_total{action="PutParameter"} 1`)
}

func TestCollector_APIErrors(t *testing.T) {
	c, reg := newTestCollector()

	c.IncAPIError("GetParameter", "ParameterNotFound")

	text := scrapeMetrics(t, reg)
	assert.Contains(t, text, `openssm_api_errors_total{action="GetParameter",code="ParameterNotFound"} 1`)
}

func TestCollector_APIRequestDuration(t *testing.T) {
	c, reg := newTestCollector()

	c.ObserveAPIRequestDuration("GetParameter", 0.05)
	c.ObserveAPIRequestDuration("GetParameter", 0.15)

	text := scrapeMetrics(t, reg)
	assert.Contains(t, text, `openssm_api_request_duration_seconds_count{action="GetParameter"} 2`)
}

func TestCollector_ResourceGauges(t *testing.T) {
	c, reg := newTestCollector()

	c.SetParameterCounts(map[[2]string]int{{"String", "Standard"}: 3})
	c.SetParameterCounts(map[[2]string]int{{"SecureString", "Advanced"}: 1})
	c.SetInstanceCounts(map[string]int{"Online": 2})
	c.SetDocumentCount(4)
	c.IncSessionsActive()
	c.IncSessionsActive()
	c.DecSessionsActive()

	text := scrapeMetrics(t, reg)
	assert.NotContains(t, text, `openssm_parameters{tier="Standard",type="String"}`)
	assert.Contains(t, text, `openssm_parameters{tier="Advanced",type="SecureString"} 1`)
	assert.Contains(t, text, `openssm_managed_instances{ping_status="Online"} 2`)
	assert.Contains(t, text, `openssm_documents 4`)
	assert.Contains(t, text, `openssm_sessions_active 1`)
}

// --- Server Tests ---

func TestServer_MetricsEndpoint(t *testing.T) {
	reg := prometheus.NewRegistry()
	collector := metrics.NewCollectorWithRegistry(reg)

	// Record some metrics
	collector.IncAPIRequest("GetParameter")

	// Create a test server with the custom registry
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	bodyStr := string(body)
	assert.Contains(t, bodyStr, "openssm_api_requests_total")
	assert.Contains(t, bodyStr, "GetParameter")
}

func TestServer_GracefulShutdown(t *testing.T) {
	// Use port 0 to let the OS pick an ephemeral port, avoiding conflicts.
	srv := metrics.NewServer(0, nil)

	go func() {
		_ = srv.Start()
	}()

	// Give the server a brief moment to bind.
	time.Sleep(50 * time.Millisecond)

	// Graceful shutdown should succeed.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := srv.Stop(ctx)
	assert.NoError(t, err)
}

// TestCollector_AllMetricsRegistered verifies that all expected metric names
// are present when the collector is created and metrics are initialized.
func TestCollector_AllMetricsRegistered(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := metrics.NewCollectorWithRegistry(reg)

	// Initialize all metrics by using them at least once (Prometheus lazily
	// creates metric instances on first use)
	c.IncAPIRequest("init")
	c.IncAPIError("init", "init")
	c.ObserveAPIRequestDuration("init", 0)
	c.SetParameterCounts(map[[2]string]int{{"String", "Standard"}: 0})
	c.SetDocumentCount(0)
	c.SetInstanceCounts(map[string]int{"Online": 0})
	c.IncCommand("init")
	c.IncInvocation("Success")
	c.IncSessionsActive()
	c.IncSession("init")

	mfs, err := reg.Gather()
	require.NoError(t, err)

	names := make(map[string]bool)
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}

	expectedMetrics := []string{
		"openssm_api_requests_total",
		"openssm_api_errors_total",
		"openssm_api_request_duration_seconds",
		"openssm_parameters",
		"openssm_documents",
		"openssm_managed_instances",
		"openssm_commands_total",
		"openssm_command_invocations_total",
		"openssm_sessions_active",
		"openssm_sessions_total",
	}

	for _, name := range expectedMetrics {
		assert.True(t, names[name], "expected metric %s to be registered", name)
	}
}

// TestServer_MetricsContentType verifies the /metrics endpoint returns
// the correct content type.
func TestServer_MetricsContentType(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = metrics.NewCollectorWithRegistry(reg)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	assert.True(t,
		strings.HasPrefix(ct, "text/plain"),
		"expected text/plain content type, got: %s", ct)
}
