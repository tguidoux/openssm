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

	"github.com/tguidoux/openssm/apps/go/server/handlers"
	"github.com/tguidoux/openssm/apps/go/server/health"
	"github.com/tguidoux/openssm/apps/go/server/metrics"
	"github.com/tguidoux/openssm/apps/go/server/middleware"
	tlsconfig "github.com/tguidoux/openssm/apps/go/server/tls"
	"github.com/tguidoux/openssm/apps/go/server/ui"
	"github.com/tguidoux/openssm/pkgs/v1/config"
	env "github.com/tguidoux/openssm/pkgs/v1/environment"
	"github.com/tguidoux/openssm/pkgs/v1/logger"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/documents"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/kms"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/parameters"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

// sweepInterval is how often background maintenance (parameter expiration,
// metrics gauges) runs.
const sweepInterval = 30 * time.Second

func main() {
	configPath := os.Getenv(config.DefaultConfigEnvVar)
	if configPath == "" {
		if _, err := os.Stat(config.DefaultConfigPath); err == nil {
			configPath = config.DefaultConfigPath
		} else {
			configPath = "/apps/go/server/config.yaml"
		}
	}
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
	if cfg.Metrics.Enabled {
		metricsCollector = metrics.NewCollector()
	}

	apiHandler := handlers.NewHandler(handlers.Services{
		Parameters: paramSvc,
		Documents:  docSvc,
		Tags:       tagSvc,
	}, metricsCollector, log)

	mux := http.NewServeMux()
	mux.Handle("/", apiHandler)

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
	go runMaintenance(bgCtx, paramSvc, docSvc, metricsCollector, log)

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

// runMaintenance periodically deletes expired parameters and refreshes
// resource gauges until ctx is cancelled.
func runMaintenance(ctx context.Context, paramSvc *parameters.Service, docSvc *documents.Service, m *metrics.Collector, log logger.LoggerInterface) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		if deleted, err := paramSvc.Sweep(ctx); err != nil {
			log.Errorf("parameter expiration sweep failed: %v", err)
		} else if len(deleted) > 0 {
			log.Info("deleted expired parameters", map[string]any{"names": deleted})
		}
		if m != nil {
			if counts, err := paramSvc.Counts(); err == nil {
				m.SetParameterCounts(counts)
			}
			if n, err := docSvc.CountOwned(); err == nil {
				m.SetDocumentCount(n)
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
