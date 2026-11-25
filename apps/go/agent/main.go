// Command openssm-agent registers a host with an OpenSSM server and runs
// Run Command invocations and Session Manager sessions on it.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tguidoux/openssm/pkgs/v1/agent"
	"github.com/tguidoux/openssm/pkgs/v1/logger"
)

func main() {
	home, _ := os.UserHomeDir()
	var (
		server       = flag.String("server", envOr("OPENSSM_SERVER", "http://localhost:9330"), "OpenSSM API endpoint (env OPENSSM_SERVER)")
		activationID = flag.String("activation-id", os.Getenv("OPENSSM_ACTIVATION_ID"), "hybrid activation ID (env OPENSSM_ACTIVATION_ID)")
		code         = flag.String("activation-code", os.Getenv("OPENSSM_ACTIVATION_CODE"), "hybrid activation code (env OPENSSM_ACTIVATION_CODE)")
		stateFile    = flag.String("state-file", envOr("OPENSSM_STATE_FILE", filepath.Join(home, ".openssm", "agent.json")), "where the instance identity is stored (env OPENSSM_STATE_FILE)")
		shell        = flag.String("shell", envOr("OPENSSM_SHELL", ""), "shell for aws:runShellScript and sessions (default /bin/sh, sessions prefer $SHELL)")
		workDir      = flag.String("work-dir", envOr("OPENSSM_WORK_DIR", ""), "default working directory for commands")
		heartbeat    = flag.Duration("heartbeat", 30*time.Second, "heartbeat interval")
		noSessions   = flag.Bool("disable-sessions", os.Getenv("OPENSSM_DISABLE_SESSIONS") == "true", "refuse Session Manager sessions")
		debug        = flag.Bool("debug", os.Getenv("OPENSSM_DEBUG") == "true", "enable debug logging")
		version      = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()
	if *version {
		fmt.Println("openssm-agent", agent.Version)
		return
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := logger.New("openssm-agent", logger.UncontextualLoggerType, level)

	cfg := agent.Config{
		ServerURL:         *server,
		ActivationID:      *activationID,
		ActivationCode:    *code,
		StateFile:         *stateFile,
		HeartbeatInterval: *heartbeat,
		Executor:          &agent.Executor{Shell: *shell, WorkDir: *workDir},
	}
	_ = noSessions
	a := agent.New(cfg, log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	for {
		err := a.Register(ctx)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return
		}
		log.Errorf("%v (retrying in 10s)", err)
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			return
		}
	}
	log.Infof("openssm-agent %s running as %s against %s", agent.Version, a.InstanceID(), *server)
	if err := a.Run(ctx); err != nil {
		log.Fatalf("agent stopped: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
