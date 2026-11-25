package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tguidoux/openssm/apps/go/server/agentapi"
	"github.com/tguidoux/openssm/apps/go/server/handlers"
	"github.com/tguidoux/openssm/apps/go/server/health"
	"github.com/tguidoux/openssm/apps/go/server/metrics"
	"github.com/tguidoux/openssm/apps/go/server/middleware"
	"github.com/tguidoux/openssm/apps/go/server/sessionws"
	tlsconfig "github.com/tguidoux/openssm/apps/go/server/tls"
	"github.com/tguidoux/openssm/apps/go/server/ui"
	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
	"github.com/tguidoux/openssm/pkgs/v1/config"
	env "github.com/tguidoux/openssm/pkgs/v1/environment"
	"github.com/tguidoux/openssm/pkgs/v1/logger"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/agenthub"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/commands"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/documents"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/instances"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/kms"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/parameters"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/sessions"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

// sweepInterval is how often background maintenance (parameter expiration,
// metrics gauges) runs.
const sweepInterval = 30 * time.Second

// defaultConfigPath resolves the config file: $CONFIG_PATH, then ./config.yaml,
// then the copy bundled in the Bazel runfiles next to the executable (which
// is where it lives inside the container image).
func defaultConfigPath() string {
	if p := os.Getenv(config.DefaultConfigEnvVar); p != "" {
		return p
	}
	candidates := []string{config.DefaultConfigPath}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, exe+".runfiles/_main/apps/go/server/config.yaml")
	}
	candidates = append(candidates, "/apps/go/server/config.yaml")
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return candidates[len(candidates)-1]
}

func main() {
	configPath := defaultConfigPath()
	cfg := config.NewConfig[ServerConfig](configPath).Config

	log := logger.New("openssm-server", logger.UncontextualLoggerType, parseLogLevel(cfg.Log.Level))
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration in %s: %v", configPath, err)
	}
	log.Info("starting OpenSSM server", map[string]any{
		"host":        cfg.Server.Host,
		"port":        cfg.Server.Port,
		"nodeAddress": cfg.SSM.NodeAddress,
		"accountId":   cfg.SSM.AccountID,
		"region":      cfg.SSM.Region,
		"storageType": cfg.SSM.StorageType,
		"environment": cfg.Environment,
	})

	store, err := openStorage(cfg.SSM)
	if err != nil {
		log.Fatalf("failed to open storage: %v", err)
	}
	defer store.close()

	account := types.Account{AccountID: cfg.SSM.AccountID, Region: cfg.SSM.Region}
	masterKey := cfg.KMS.MasterKey
	if masterKey == "" {
		masterKey = cfg.SSM.ServerSecret
	}
	kmsSvc, err := kms.New(store.kv, account, kms.Options{MasterKey: masterKey, AutoCreateKeys: cfg.KMS.AutoCreateKeys})
	if err != nil {
		log.Fatalf("failed to initialize KMS: %v", err)
	}
	tagSvc := tags.New(store.kv)
	paramSvc := parameters.New(store.kv, kmsSvc, tagSvc, account, parameters.Options{
		DefaultTier:           cfg.Parameters.DefaultTier,
		MaxStandardParameters: cfg.Parameters.MaxStandardParameters,
		MaxAdvancedParameters: cfg.Parameters.MaxAdvancedParameters,
	})

	docSvc, err := documents.New(store.kv, tagSvc, account)
	if err != nil {
		log.Fatalf("failed to initialize documents: %v", err)
	}

	seedKMSKeys(kmsSvc, cfg.KMS.Keys, log)
	seedParameters(paramSvc, cfg.Parameters.Startup, log)
	seedInitialCredentials(store.credentials, cfg.Auth, cfg.SSM.Region, cfg.SSM.AccountID, log)

	var metricsCollector *metrics.Collector
	var commandObserver commands.Observer
	var sessionObserver sessions.Observer
	var relayMetrics sessionws.Metrics
	if cfg.Metrics.Enabled {
		metricsCollector = metrics.NewCollector()
		commandObserver, sessionObserver, relayMetrics = metricsCollector, metricsCollector, metricsCollector
	}

	hub := agenthub.New()
	instSvc := instances.New(store.kv, tagSvc, instances.Options{
		PingTimeout:        time.Duration(cfg.Agents.PingTimeoutSeconds) * time.Second,
		LatestAgentVersion: cfg.Agents.LatestAgentVersion,
	})
	cmdSvc := commands.New(store.kv, docSvc, instSvc, hub, commandObserver)
	baseURL := endpointURL(cfg.SSM.NodeAddress, cfg.Server.TLS.Enabled)
	sessSvc := sessions.New(store.kv, docSvc, instSvc, hub, account, sessions.Options{
		StreamURL: func(id string) string { return streamURL(baseURL, cfg.Sessions.StreamURLBase, id) },
	}, sessionObserver)

	apiHandler := handlers.NewHandler(handlers.Services{
		Parameters: paramSvc,
		Documents:  docSvc,
		Instances:  instSvc,
		Commands:   cmdSvc,
		Sessions:   sessSvc,
		Tags:       tagSvc,
	}, metricsCollector, log)
	agentHandler := agentapi.New(instSvc, cmdSvc, sessSvc, hub, log)
	relay := sessionws.New(sessSvc, agentHandler.InstanceFromRequest, relayMetrics, log)

	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Paths under the SigV4-exempt prefixes that did not match an agent or
		// relay route must never fall through to the SSM API.
		if middleware.IsExemptPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		apiHandler.ServeHTTP(w, r)
	}))
	mux.Handle(agentproto.PathPrefix, agentHandler)
	mux.HandleFunc("GET "+agentproto.PathSession+"{sessionId}", relay.ServeAgent)
	mux.HandleFunc("GET "+sessionws.ClientPathPrefix+"{sessionId}", relay.ServeClient)

	var middlewares []middleware.Middleware
	if cfg.RequestLogging.Enabled {
		middlewares = append(middlewares, middleware.RequestLogger(log))
	}
	if cfg.Auth.IsEnabled() {
		middlewares = append(middlewares, middleware.Auth(store.credentials, cfg.SSM.Region, log))
		log.Info("credential authentication enabled")
	} else {
		log.Info("credential authentication disabled")
	}
	if cfg.RateLimit.Enabled {
		if cfg.RateLimit.PerAction {
			mw, cleanup := middleware.PerActionRateLimiter(cfg.RateLimit.RequestsPerSecond, cfg.RateLimit.Burst)
			middlewares = append(middlewares, mw)
			defer cleanup()
		} else {
			middlewares = append(middlewares, middleware.GlobalRateLimiter(cfg.RateLimit.RequestsPerSecond, cfg.RateLimit.Burst))
		}
	}
	var rootHandler http.Handler = mux
	if len(middlewares) > 0 {
		rootHandler = middleware.Chain(middlewares...)(mux)
	}

	apiTLS, err := tlsconfig.LoadFromConfig(cfg.Server.TLS.ToTLSConfig())
	if err != nil {
		log.Fatalf("failed to load SSM server TLS config: %v", err)
	}
	httpServer := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:           rootHandler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if apiTLS != nil {
		httpServer.TLSConfig = apiTLS
	}

	if cfg.Environment != env.LOCAL {
		healthPort := cfg.Health.Port
		if healthPort == 0 {
			healthPort = 8001
		}
		healthTLS, err := tlsconfig.LoadFromConfig(cfg.Health.TLS.ToTLSConfig())
		if err != nil {
			log.Fatalf("failed to load health server TLS config: %v", err)
		}
		healthServer := health.NewServer(healthPort, healthTLS)
		if healthTLS != nil {
			healthServer.SetCertFiles(cfg.Health.TLS.CertFile, cfg.Health.TLS.KeyFile)
		}
		startServerable(log, "health check", healthPort, healthServer)
	}

	var uiServer *ui.Server
	if cfg.UI.Enabled {
		uiPort := cfg.UI.Port
		if uiPort == 0 {
			uiPort = 9331
		}
		uiTLS, err := tlsconfig.LoadFromConfig(cfg.UI.TLS.ToTLSConfig())
		if err != nil {
			log.Fatalf("failed to load UI server TLS config: %v", err)
		}
		uiServer = ui.NewServer(uiPort, ui.Deps{
			Parameters:     paramSvc,
			Documents:      docSvc,
			Instances:      instSvc,
			Commands:       cmdSvc,
			Sessions:       sessSvc,
			KMS:            kmsSvc,
			Tags:           tagSvc,
			Credentials:    store.credentials,
			EndpointURL:    endpointURL(cfg.SSM.NodeAddress, apiTLS != nil),
			Region:         cfg.SSM.Region,
			AccountID:      cfg.SSM.AccountID,
			MetricsEnabled: cfg.Metrics.Enabled,
		}, log, uiTLS)
		if uiTLS != nil {
			uiServer.SetCertFiles(cfg.UI.TLS.CertFile, cfg.UI.TLS.KeyFile)
		}
		startServerable(log, "UI", uiPort, uiServer)
	}

	var metricsServer *metrics.Server
	if cfg.Metrics.Enabled {
		metricsPort := cfg.Metrics.Port
		if metricsPort == 0 {
			metricsPort = 9332
		}
		metricsTLS, err := tlsconfig.LoadFromConfig(cfg.Metrics.TLS.ToTLSConfig())
		if err != nil {
			log.Fatalf("failed to load metrics server TLS config: %v", err)
		}
		metricsServer = metrics.NewServer(metricsPort, metricsTLS)
		if metricsTLS != nil {
			metricsServer.SetCertFiles(cfg.Metrics.TLS.CertFile, cfg.Metrics.TLS.KeyFile)
		}
		startServerable(log, "metrics", metricsPort, metricsServer)
	}

	bgCtx, stopBackground := context.WithCancel(context.Background())
	go runMaintenance(bgCtx, maintenance{params: paramSvc, docs: docSvc, instances: instSvc, commands: cmdSvc, sessions: sessSvc}, metricsCollector, log)

	serverErr := make(chan error, 1)
	go func() {
		log.Infof("starting SSM API server on %s:%d", cfg.Server.Host, cfg.Server.Port)
		var err error
		if apiTLS != nil {
			err = httpServer.ListenAndServeTLS(cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile)
		} else {
			err = httpServer.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-quit:
	case err := <-serverErr:
		log.Errorf("failed to start server: %v", err)
	}

	log.Info("shutting down server...")
	stopBackground()
	relay.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Errorf("failed to shutdown SSM API server: %v", err)
	}
	if uiServer != nil {
		if err := uiServer.Stop(ctx); err != nil {
			log.Errorf("failed to shutdown UI server: %v", err)
		}
	}
	if metricsServer != nil {
		if err := metricsServer.Stop(ctx); err != nil {
			log.Errorf("failed to shutdown metrics server: %v", err)
		}
	}
	log.Info("server stopped")
}

// maintenance groups the services swept by runMaintenance.
type maintenance struct {
	params    *parameters.Service
	docs      *documents.Service
	instances *instances.Service
	commands  *commands.Service
	sessions  *sessions.Service
}

// runMaintenance periodically expires parameters and command invocations
// and refreshes resource gauges until ctx is cancelled.
func runMaintenance(ctx context.Context, svc maintenance, m *metrics.Collector, log logger.LoggerInterface) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		if deleted, err := svc.params.Sweep(ctx); err != nil {
			log.Errorf("parameter expiration sweep failed: %v", err)
		} else if len(deleted) > 0 {
			log.Info("deleted expired parameters", map[string]any{"names": deleted})
		}
		if n, err := svc.commands.Sweep(); err != nil {
			log.Errorf("command timeout sweep failed: %v", err)
		} else if n > 0 {
			log.Infof("timed out %d command invocation(s)", n)
		}
		if n, err := svc.sessions.Sweep(); err != nil {
			log.Errorf("session timeout sweep failed: %v", err)
		} else if n > 0 {
			log.Infof("failed %d session(s) that never connected", n)
		}
		if m != nil {
			if counts, err := svc.params.Counts(); err == nil {
				m.SetParameterCounts(counts)
			}
			if n, err := svc.docs.CountOwned(); err == nil {
				m.SetDocumentCount(n)
			}
			if counts, err := svc.instances.PingCounts(); err == nil {
				m.SetInstanceCounts(counts)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// endpointURL converts a node address into the base URL clients should use.
func endpointURL(nodeAddress string, tls bool) string {
	if strings.HasPrefix(nodeAddress, "http://") || strings.HasPrefix(nodeAddress, "https://") {
		return strings.TrimSuffix(nodeAddress, "/")
	}
	if tls {
		return "https://" + nodeAddress
	}
	return "http://" + nodeAddress
}

// streamURL builds the Session Manager StreamUrl for a session. The base is
// sessions.streamUrlBase when set, otherwise the API endpoint with ws(s).
func streamURL(apiBase, override, sessionID string) string {
	base := strings.TrimSuffix(override, "/")
	if base == "" {
		base = "ws" + strings.TrimPrefix(apiBase, "http")
	}
	return base + sessionws.ClientPathPrefix + sessionID
}

// startServerable starts a server in a goroutine.
func startServerable(log logger.LoggerInterface, name string, port int, s interface {
	Start() error
	Stop(context.Context) error
}) {
	go func() {
		log.Infof("starting %s server on :%d", name, port)
		if err := s.Start(); err != nil && err != http.ErrServerClosed {
			log.Errorf("failed to start %s server: %v", name, err)
		}
	}()
}
