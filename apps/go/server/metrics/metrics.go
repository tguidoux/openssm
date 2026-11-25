package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Collector holds all Prometheus metric collectors for the OpenSSM server.
// Each metric is registered with the provided registry on creation.
type Collector struct {
	apiRequestsTotal   *prometheus.CounterVec
	apiErrorsTotal     *prometheus.CounterVec
	apiRequestDuration *prometheus.HistogramVec
	parametersTotal    *prometheus.GaugeVec
	documentsTotal     prometheus.Gauge
	instancesTotal     *prometheus.GaugeVec
	commandsTotal      *prometheus.CounterVec
	invocationsTotal   *prometheus.CounterVec
	sessionsActive     prometheus.Gauge
	sessionsTotal      *prometheus.CounterVec
}

// NewCollector creates and registers all Prometheus metrics with the default
// registry. Panics if registration fails (duplicate registration is a programming error).
func NewCollector() *Collector {
	return NewCollectorWithRegistry(prometheus.DefaultRegisterer)
}

// NewCollectorWithRegistry creates and registers all Prometheus metrics with
// the provided registerer. Use this in tests with a non-default registry to
// avoid duplicate-registration panics.
func NewCollectorWithRegistry(reg prometheus.Registerer) *Collector {
	c := &Collector{
		apiRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "openssm_api_requests_total",
			Help: "Total number of SSM API requests by action.",
		}, []string{"action"}),
		apiErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "openssm_api_errors_total",
			Help: "Total number of SSM API errors by action and error code.",
		}, []string{"action", "code"}),
		apiRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "openssm_api_request_duration_seconds",
			Help:    "SSM API request latency in seconds by action.",
			Buckets: prometheus.DefBuckets,
		}, []string{"action"}),
		parametersTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "openssm_parameters",
			Help: "Number of stored parameters by type and tier.",
		}, []string{"type", "tier"}),
		documentsTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "openssm_documents",
			Help: "Number of user-owned SSM documents.",
		}),
		instancesTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "openssm_managed_instances",
			Help: "Number of managed instances by ping status.",
		}, []string{"ping_status"}),
		commandsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "openssm_commands_total",
			Help: "Total number of commands sent by document name.",
		}, []string{"document"}),
		invocationsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "openssm_command_invocations_total",
			Help: "Total number of finished command invocations by final status.",
		}, []string{"status"}),
		sessionsActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "openssm_sessions_active",
			Help: "Number of currently connected Session Manager sessions.",
		}),
		sessionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "openssm_sessions_total",
			Help: "Total number of sessions started by document name.",
		}, []string{"document"}),
	}

	reg.MustRegister(
		c.apiRequestsTotal,
		c.apiErrorsTotal,
		c.apiRequestDuration,
		c.parametersTotal,
		c.documentsTotal,
		c.instancesTotal,
		c.commandsTotal,
		c.invocationsTotal,
		c.sessionsActive,
		c.sessionsTotal,
	)

	return c
}

// IncAPIRequest increments the API request counter for the given action.
func (c *Collector) IncAPIRequest(action string) {
	c.apiRequestsTotal.WithLabelValues(action).Inc()
}

// IncAPIError increments the API error counter.
func (c *Collector) IncAPIError(action, code string) {
	c.apiErrorsTotal.WithLabelValues(action, code).Inc()
}

// ObserveAPIRequestDuration records the API request duration for the given action.
func (c *Collector) ObserveAPIRequestDuration(action string, durationSeconds float64) {
	c.apiRequestDuration.WithLabelValues(action).Observe(durationSeconds)
}

// SetParameterCounts replaces the parameter gauges. counts is keyed by
// [type, tier].
func (c *Collector) SetParameterCounts(counts map[[2]string]int) {
	c.parametersTotal.Reset()
	for k, v := range counts {
		c.parametersTotal.WithLabelValues(k[0], k[1]).Set(float64(v))
	}
}

// SetDocumentCount sets the number of user documents.
func (c *Collector) SetDocumentCount(n int) {
	c.documentsTotal.Set(float64(n))
}

// SetInstanceCounts replaces the managed instance gauges keyed by ping status.
func (c *Collector) SetInstanceCounts(counts map[string]int) {
	c.instancesTotal.Reset()
	for k, v := range counts {
		c.instancesTotal.WithLabelValues(k).Set(float64(v))
	}
}

// IncCommand records a SendCommand for a document.
func (c *Collector) IncCommand(document string) {
	c.commandsTotal.WithLabelValues(document).Inc()
}

// IncInvocation records a finished command invocation.
func (c *Collector) IncInvocation(status string) {
	c.invocationsTotal.WithLabelValues(status).Inc()
}

// IncSessionsActive increments the active session gauge.
func (c *Collector) IncSessionsActive() { c.sessionsActive.Inc() }

// DecSessionsActive decrements the active session gauge.
func (c *Collector) DecSessionsActive() { c.sessionsActive.Dec() }

// IncSession records a StartSession for a document.
func (c *Collector) IncSession(document string) {
	c.sessionsTotal.WithLabelValues(document).Inc()
}
