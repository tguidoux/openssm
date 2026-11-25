package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
	"github.com/tguidoux/openssm/pkgs/v1/logger"
	"github.com/tguidoux/openssm/pkgs/v1/ssmmessages"
)

const (
	// outputChunk matches the 1024-byte stream payload size used by SSM.
	outputChunk = 1024
	// handshakeTimeout bounds how long the agent waits for a client.
	handshakeTimeout = 2 * time.Minute
)

// Process is a running session process (PTY shell or command).
type Process interface {
	io.ReadWriter
	Resize(cols, rows uint16) error
	Wait() (int, error)
	Kill() error
	Close() error
}

// ShellSessionHandler serves Standard_Stream, InteractiveCommands and
// NonInteractiveCommands sessions by spawning a local process and relaying
// its terminal over the Session Manager data channel.
type ShellSessionHandler struct {
	// Shell overrides the login shell (default $SHELL, then /bin/sh).
	Shell string
	Log   logger.LoggerInterface
	// Dialer overrides the WebSocket dialer (tests).
	Dialer *websocket.Dialer
	// Start overrides process creation (tests); defaults to startProcess.
	Start func(p agentproto.SessionPayload, shell string) (Process, error)
}

// Serve implements SessionHandler.
func (h *ShellSessionHandler) Serve(ctx context.Context, client *Client, s agentproto.SessionPayload) error {
	conn, err := h.dial(ctx, client, s.SessionID)
	if err != nil {
		return err
	}
	stream := ssmmessages.NewStream(conn, ssmmessages.OutputStreamData)
	defer stream.Close()

	sessCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = stream.RunResender(sessCtx) }()
	// Unblock pending reads when the agent stops or the session ends.
	go func() {
		<-sessCtx.Done()
		stream.Close()
	}()

	handshakeTimer := time.AfterFunc(handshakeTimeout, func() { stream.Close() })
	err = h.handshake(stream, s)
	handshakeTimer.Stop()
	if err != nil {
		return fmt.Errorf("session handshake: %w", err)
	}

	start := h.Start
	if start == nil {
		start = startProcess
	}
	proc, err := start(s, h.shell(s))
	if err != nil {
		_ = stream.Send(ssmmessages.PayloadOutput, []byte(fmt.Sprintf("openssm-agent: cannot start session: %v\r\n", err)))
		stream.WaitDrained(2 * time.Second)
		_ = stream.SendControl(ssmmessages.NewChannelClosed(s.SessionID, err.Error()))
		return err
	}
	defer proc.Close()

	var lastInput sync.Mutex
	last := time.Now()
	touch := func() { lastInput.Lock(); last = time.Now(); lastInput.Unlock() }

	// Process output -> client.
	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		buf := make([]byte, outputChunk)
		for {
			n, err := proc.Read(buf)
			if n > 0 {
				if sendErr := stream.Send(ssmmessages.PayloadOutput, append([]byte(nil), buf[:n]...)); sendErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Client input -> process.
	inputErr := make(chan error, 1)
	go func() {
		for {
			m, err := stream.Receive()
			if err != nil {
				inputErr <- err
				return
			}
			if m.MessageType == ssmmessages.ChannelClosed {
				inputErr <- errors.New("channel closed by server")
				return
			}
			switch m.PayloadType {
			case ssmmessages.PayloadOutput:
				touch()
				if _, err := proc.Write(m.Payload); err != nil {
					inputErr <- err
					return
				}
			case ssmmessages.PayloadSize:
				var size ssmmessages.SizeData
				if json.Unmarshal(m.Payload, &size) == nil && size.Cols > 0 && size.Rows > 0 {
					_ = proc.Resize(uint16(size.Cols), uint16(size.Rows))
				}
			case ssmmessages.PayloadFlag:
				if len(m.Payload) == 4 && m.Payload[3] == byte(ssmmessages.FlagTerminateSession) {
					inputErr <- errors.New("terminated by client")
					return
				}
			}
		}
	}()

	idle := idleTimeout(s)
	maxDur := maxDuration(s)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	started := time.Now()
	exited := make(chan struct{})
	var exitCode int
	go func() {
		exitCode, _ = proc.Wait()
		close(exited)
	}()

	output := ""
	for done := false; !done; {
		select {
		case <-exited:
			// Flush remaining output before closing.
			select {
			case <-outputDone:
			case <-time.After(2 * time.Second):
			}
			done = true
		case err := <-inputErr:
			h.Log.Debugf("session %s input ended: %v", s.SessionID, err)
			_ = proc.Kill()
			<-exited
			done = true
		case <-ctx.Done():
			output = "openssm-agent is shutting down."
			_ = proc.Kill()
			<-exited
			done = true
		case <-ticker.C:
			lastInput.Lock()
			idleFor := time.Since(last)
			lastInput.Unlock()
			if idle > 0 && idleFor > idle {
				output = "Your session timed out due to inactivity and has been terminated."
				_ = proc.Kill()
			}
			if maxDur > 0 && time.Since(started) > maxDur {
				output = "Your session has reached the maximum session duration and has been terminated."
				_ = proc.Kill()
			}
		}
	}
	if s.SessionType == ssmmessages.SessionTypeNonInteractive && output == "" {
		output = "Command exited with code " + strconv.Itoa(exitCode)
	}
	stream.WaitDrained(3 * time.Second)
	_ = stream.SendControl(ssmmessages.NewChannelClosed(s.SessionID, output))
	return nil
}

// handshake performs the agent side of the Session Manager handshake:
// HandshakeRequest -> HandshakeResponse -> HandshakeComplete.
func (h *ShellSessionHandler) handshake(stream *ssmmessages.Stream, s agentproto.SessionPayload) error {
	params, _ := json.Marshal(ssmmessages.SessionTypeRequest{SessionType: s.SessionType, Properties: map[string]any{}})
	req, _ := json.Marshal(ssmmessages.HandshakeRequest{
		AgentVersion:           Version,
		RequestedClientActions: []ssmmessages.RequestedClientAction{{ActionType: "SessionType", ActionParameters: params}},
	})
	start := time.Now()
	if err := stream.Send(ssmmessages.PayloadHandshakeRequest, req); err != nil {
		return err
	}
	for {
		m, err := stream.Receive()
		if err != nil {
			return err
		}
		if m.MessageType == ssmmessages.ChannelClosed {
			return errors.New("channel closed during handshake")
		}
		if m.PayloadType != ssmmessages.PayloadHandshakeResponse {
			continue
		}
		var resp ssmmessages.HandshakeResponse
		if err := json.Unmarshal(m.Payload, &resp); err != nil {
			return fmt.Errorf("invalid handshake response: %w", err)
		}
		if len(resp.Errors) > 0 {
			return fmt.Errorf("client rejected handshake: %s", strings.Join(resp.Errors, "; "))
		}
		break
	}
	complete, _ := json.Marshal(ssmmessages.HandshakeComplete{HandshakeTimeToComplete: time.Since(start)})
	return stream.Send(ssmmessages.PayloadHandshakeComplete, complete)
}

func (h *ShellSessionHandler) dial(ctx context.Context, client *Client, sessionID string) (*ssmmessages.WSConn, error) {
	url := client.BaseURL() + agentproto.PathSession + sessionID
	url = "ws" + strings.TrimPrefix(url, "http")
	dialer := h.Dialer
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	header := http.Header{"Authorization": {"Bearer " + client.Token()}}
	ws, resp, err := dialer.DialContext(ctx, url, header)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("opening data channel (%s): %w", resp.Status, err)
		}
		return nil, fmt.Errorf("opening data channel: %w", err)
	}
	return ssmmessages.NewWSConn(ws), nil
}

func (h *ShellSessionHandler) shell(s agentproto.SessionPayload) string {
	switch {
	case s.Shell != "":
		return s.Shell
	case h.Shell != "":
		return h.Shell
	case os.Getenv("SHELL") != "":
		return os.Getenv("SHELL")
	default:
		return "/bin/sh"
	}
}

func idleTimeout(s agentproto.SessionPayload) time.Duration {
	if m, err := strconv.Atoi(s.Parameters["idleSessionTimeout"]); err == nil && m > 0 {
		return time.Duration(m) * time.Minute
	}
	return 0
}

func maxDuration(s agentproto.SessionPayload) time.Duration {
	if m, err := strconv.Atoi(s.Parameters["maxSessionDuration"]); err == nil && m > 0 {
		return time.Duration(m) * time.Minute
	}
	return 0
}
