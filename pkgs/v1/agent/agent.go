package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
	"github.com/tguidoux/openssm/pkgs/v1/logger"
)

// Config configures the agent.
type Config struct {
	// ServerURL is the OpenSSM API endpoint (e.g. http://localhost:9330).
	ServerURL string
	// ActivationID and ActivationCode register the agent on first start.
	ActivationID   string
	ActivationCode string
	// StateFile persists the instance ID and token across restarts.
	StateFile string
	// HeartbeatInterval controls how often host info is reported.
	HeartbeatInterval time.Duration
	// PollWait is the long-poll duration.
	PollWait time.Duration
	// Executor runs command steps.
	Executor *Executor
	// SessionHandler runs Session Manager sessions; nil disables sessions.
	SessionHandler SessionHandler
	// HTTPClient overrides the default HTTP client.
	HTTPClient *http.Client
}

// SessionHandler serves a Session Manager session for the agent.
type SessionHandler interface {
	Serve(ctx context.Context, client *Client, s agentproto.SessionPayload) error
}

// State is persisted to StateFile after registration.
type State struct {
	InstanceID string `json:"instanceId"`
	Token      string `json:"token"`
	ServerURL  string `json:"serverUrl"`
}

// Agent is a running openssm-agent.
type Agent struct {
	cfg    Config
	client *Client
	log    logger.LoggerInterface
	state  State

	mu      sync.Mutex
	running map[string]context.CancelFunc
	wg      sync.WaitGroup
}

// New creates an agent. Call Start to register (if needed) and run.
func New(cfg Config, log logger.LoggerInterface) *Agent {
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 30 * time.Second
	}
	if cfg.PollWait <= 0 {
		cfg.PollWait = 20 * time.Second
	}
	if cfg.Executor == nil {
		cfg.Executor = &Executor{}
	}
	return &Agent{
		cfg:     cfg,
		client:  NewClient(cfg.ServerURL, "", cfg.HTTPClient),
		log:     log,
		running: make(map[string]context.CancelFunc),
	}
}

// InstanceID returns the registered managed instance ID.
func (a *Agent) InstanceID() string { return a.state.InstanceID }

// Register loads persisted state or registers with the activation.
func (a *Agent) Register(ctx context.Context) error {
	if st, err := loadState(a.cfg.StateFile); err == nil && st.Token != "" && (st.ServerURL == "" || st.ServerURL == a.cfg.ServerURL) {
		a.state = *st
		a.client.token = st.Token
		if err := a.client.Heartbeat(ctx, HostInfo()); err == nil {
			a.log.Infof("resumed as managed instance %s", st.InstanceID)
			return nil
		} else if apiErr := (*APIError)(nil); !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
			return fmt.Errorf("contacting server: %w", err)
		}
		a.log.Warning("stored agent token was rejected; registering again")
	}
	if a.cfg.ActivationID == "" || a.cfg.ActivationCode == "" {
		return errors.New("agent is not registered: an activation ID and code are required")
	}
	out, err := a.client.Register(ctx, &agentproto.RegisterRequest{
		ActivationID: a.cfg.ActivationID, ActivationCode: a.cfg.ActivationCode, AgentInfo: HostInfo(),
	})
	if err != nil {
		return fmt.Errorf("registering with activation %s: %w", a.cfg.ActivationID, err)
	}
	a.state = State{InstanceID: out.InstanceID, Token: out.Token, ServerURL: a.cfg.ServerURL}
	if err := saveState(a.cfg.StateFile, &a.state); err != nil {
		a.log.Warningf("could not persist agent state: %v", err)
	}
	a.log.Infof("registered as managed instance %s", out.InstanceID)
	return nil
}

// Run polls for work until ctx is cancelled. Register must be called first.
func (a *Agent) Run(ctx context.Context) error {
	go a.heartbeatLoop(ctx)
	backoff := time.Second
	for ctx.Err() == nil {
		resp, err := a.client.Poll(ctx, a.cfg.PollWait)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			a.log.Warningf("poll failed: %v (retrying in %s)", err, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		for _, id := range resp.Cancellations {
			a.cancel(id)
		}
		for _, cmd := range resp.Commands {
			a.startCommand(ctx, cmd)
		}
		for _, s := range resp.Sessions {
			a.startSession(ctx, s)
		}
	}
	a.mu.Lock()
	for _, cancel := range a.running {
		cancel()
	}
	a.mu.Unlock()
	a.wg.Wait()
	return nil
}

func (a *Agent) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(a.cfg.HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := a.client.Heartbeat(ctx, HostInfo()); err != nil && ctx.Err() == nil {
				a.log.Warningf("heartbeat failed: %v", err)
			}
		}
	}
}

func (a *Agent) startCommand(ctx context.Context, cmd agentproto.CommandPayload) {
	cmdCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.running[cmd.CommandID] = cancel
	a.mu.Unlock()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer func() {
			a.mu.Lock()
			delete(a.running, cmd.CommandID)
			a.mu.Unlock()
			cancel()
		}()
		a.log.Info("running command", map[string]any{"commandId": cmd.CommandID, "document": cmd.DocumentName})
		res := a.cfg.Executor.Run(cmdCtx, cmd)
		// Report with a fresh context so results are delivered even when the
		// command itself was cancelled.
		reportCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		if err := a.client.Report(reportCtx, cmd.CommandID, &res); err != nil {
			a.log.Errorf("failed to report command %s: %v", cmd.CommandID, err)
			return
		}
		a.log.Info("command finished", map[string]any{"commandId": cmd.CommandID, "status": res.Status})
	}()
}

func (a *Agent) cancel(commandID string) {
	a.mu.Lock()
	cancel, ok := a.running[commandID]
	a.mu.Unlock()
	if ok {
		a.log.Infof("cancelling command %s", commandID)
		cancel()
	}
}

func (a *Agent) startSession(ctx context.Context, s agentproto.SessionPayload) {
	if a.cfg.SessionHandler == nil {
		a.log.Warningf("ignoring session %s: sessions are disabled", s.SessionID)
		return
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.log.Info("starting session", map[string]any{"sessionId": s.SessionID, "type": s.SessionType})
		if err := a.cfg.SessionHandler.Serve(ctx, a.client, s); err != nil && ctx.Err() == nil {
			a.log.Errorf("session %s ended with error: %v", s.SessionID, err)
			return
		}
		a.log.Infof("session %s ended", s.SessionID)
	}()
}

func loadState(path string) (*State, error) {
	if path == "" {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func saveState(path string, st *State) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
