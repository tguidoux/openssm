// Package sessionws serves Session Manager data channels.
//
// Two WebSocket endpoints meet in a per-session relay:
//
//   - Client side (/v1/data-channel/{sessionId}): spoken by the official
//     session-manager-plugin. The first text frame is open_data_channel
//     carrying the StartSession token; then binary ClientMessage frames.
//   - Agent side (/agent/v1/sessions/{sessionId}): opened by openssm-agent
//     with its bearer token, using the same ClientMessage framing.
//
// The relay is a "smart" endpoint on each side: it acknowledges, orders and
// retransmits stream data independently per connection, and forwards
// payloads (handshake, shell I/O, size, flags, exit code) between them. This
// lets either side reconnect (ResumeSession / agent reconnect) without the
// other noticing. The agent drives the handshake exactly like amazon-ssm-agent.
package sessionws

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tguidoux/openssm/pkgs/v1/logger"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/sessions"
	"github.com/tguidoux/openssm/pkgs/v1/ssmmessages"
)

// ClientPathPrefix is the client data channel path (StreamUrl).
const ClientPathPrefix = "/v1/data-channel/"

const (
	openTimeout = 30 * time.Second
	// reconnectGrace keeps a session alive while one side reconnects.
	reconnectGrace = 60 * time.Second
	// bufferLimit bounds payloads queued while a side is disconnected.
	bufferLimit = 4096
)

// Metrics is the subset of the metrics collector used by the relay.
type Metrics interface {
	IncSessionsActive()
	DecSessionsActive()
}

// AgentAuth authenticates an agent request and returns its instance ID.
type AgentAuth func(r *http.Request) (string, error)

// Server hosts all active relays.
type Server struct {
	sessions  *sessions.Service
	agentAuth AgentAuth
	log       logger.LoggerInterface
	metrics   Metrics
	upgrader  websocket.Upgrader

	mu     sync.Mutex
	relays map[string]*relay
}

// New creates the relay server and registers a TerminateSession hook.
func New(svc *sessions.Service, agentAuth AgentAuth, m Metrics, log logger.LoggerInterface) *Server {
	s := &Server{
		sessions: svc, agentAuth: agentAuth, log: log, metrics: m,
		upgrader: websocket.Upgrader{
			ReadBufferSize: 32 << 10, WriteBufferSize: 32 << 10,
			// Clients are CLIs, not browsers; authentication is the session token.
			CheckOrigin: func(*http.Request) bool { return true },
		},
		relays: make(map[string]*relay),
	}
	svc.OnTerminate(func(id string) { s.terminate(id, "") })
	return s
}

// ServeClient handles the session-manager-plugin WebSocket.
func (s *Server) ServeClient(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionId")
	if !s.sessions.IsActive(sessionID) {
		http.Error(w, "session not found or terminated", http.StatusNotFound)
		return
	}
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn := ssmmessages.NewWSConn(ws)
	raw, err := conn.ReadText(openTimeout)
	if err != nil {
		conn.Close()
		return
	}
	var open ssmmessages.OpenDataChannelInput
	if json.Unmarshal(raw, &open) != nil || s.sessions.AuthorizeClient(sessionID, open.TokenValue) != nil {
		s.log.Warningf("rejected data channel for session %s: invalid token", sessionID)
		conn.Close()
		return
	}
	s.log.Info("session client connected", map[string]any{"sessionId": sessionID, "clientId": open.ClientID, "clientVersion": open.ClientVersion})
	s.relayFor(sessionID).attach(sideClient, conn)
}

// ServeAgent handles the openssm-agent WebSocket.
func (s *Server) ServeAgent(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionId")
	instanceID, err := s.agentAuth(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := s.sessions.AuthorizeAgent(sessionID, instanceID); err != nil {
		http.Error(w, "session not found for this instance", http.StatusNotFound)
		return
	}
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.log.Info("session agent connected", map[string]any{"sessionId": sessionID, "instanceId": instanceID})
	s.relayFor(sessionID).attach(sideAgent, ssmmessages.NewWSConn(ws))
}

func (s *Server) relayFor(sessionID string) *relay {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.relays[sessionID]; ok {
		return r
	}
	r := newRelay(s, sessionID)
	s.relays[sessionID] = r
	if s.metrics != nil {
		s.metrics.IncSessionsActive()
	}
	return r
}

// terminate closes a relay (TerminateSession or end of session).
func (s *Server) terminate(sessionID, output string) {
	s.mu.Lock()
	r, ok := s.relays[sessionID]
	s.mu.Unlock()
	if ok {
		r.close(output)
	}
}

func (s *Server) remove(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.relays[sessionID]; ok {
		delete(s.relays, sessionID)
		if s.metrics != nil {
			s.metrics.DecSessionsActive()
		}
	}
}

// Shutdown closes every relay.
func (s *Server) Shutdown() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.relays))
	for id := range s.relays {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.terminate(id, "OpenSSM server is shutting down")
	}
}

type side int

const (
	sideClient side = iota
	sideAgent
)

func (sd side) String() string {
	if sd == sideClient {
		return "client"
	}
	return "agent"
}

// payload is a stream data payload in transit between the two sides.
type payload struct {
	typ  uint32
	data []byte
}

// relay joins one client connection and one agent connection.
type relay struct {
	srv       *Server
	sessionID string

	mu      sync.Mutex
	streams [2]*ssmmessages.Stream
	// last remembers the previous stream per side so a reconnecting peer
	// continues its sequence numbers.
	last [2]*ssmmessages.Stream
	// queued holds payloads destined to a side that is not connected yet.
	queued [2][]payload
	done   chan struct{}
	closed bool
	grace  *time.Timer
}

func newRelay(srv *Server, sessionID string) *relay {
	r := &relay{srv: srv, sessionID: sessionID, done: make(chan struct{})}
	r.grace = time.AfterFunc(reconnectGrace*2, func() { r.expire() })
	return r
}

func (r *relay) attach(sd side, conn ssmmessages.Conn) {
	outType := ssmmessages.OutputStreamData
	if sd == sideAgent {
		outType = ssmmessages.InputStreamData
	}
	stream := ssmmessages.NewStream(conn, outType)

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		_ = stream.SendControl(ssmmessages.NewChannelClosed(r.sessionID, "Session has been terminated."))
		stream.Close()
		return
	}
	// Unpublish the previous stream so concurrent forwards queue up while the
	// new stream is prepared; publishing it only once its state is complete
	// keeps sequence numbers and output ordering intact.
	old := r.streams[sd]
	r.streams[sd] = nil
	prev := old
	if prev == nil {
		prev = r.last[sd]
	}
	r.mu.Unlock()
	if old != nil {
		old.Close()
	}
	if prev != nil && sd == sideClient {
		_ = stream.ResumeFrom(prev)
	}

	var bothUp bool
	for {
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			_ = stream.SendControl(ssmmessages.NewChannelClosed(r.sessionID, "Session has been terminated."))
			stream.Close()
			return
		}
		queued := r.queued[sd]
		r.queued[sd] = nil
		if len(queued) == 0 {
			r.streams[sd] = stream
			r.last[sd] = stream
			bothUp = r.streams[sideClient] != nil && r.streams[sideAgent] != nil
			if bothUp {
				r.grace.Stop()
			}
			r.mu.Unlock()
			break
		}
		r.mu.Unlock()
		for _, p := range queued {
			_ = stream.Send(p.typ, p.data)
		}
	}
	if bothUp {
		_ = r.srv.sessions.SetStatus(r.sessionID, sessions.StatusConnected, "")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if err := stream.RunResender(ctx); errors.Is(err, ssmmessages.ErrResendExhausted) {
			stream.Close()
		}
	}()
	go func() {
		defer cancel()
		r.pump(sd, stream)
	}()
}

// pump reads from one side and forwards payloads to the other.
func (r *relay) pump(sd side, stream *ssmmessages.Stream) {
	for {
		m, err := stream.Receive()
		if err != nil {
			r.detach(sd, stream)
			return
		}
		switch m.MessageType {
		case ssmmessages.InputStreamData, ssmmessages.OutputStreamData:
			r.forward(1-sd, payload{typ: m.PayloadType, data: m.Payload})
			if sd == sideClient && m.PayloadType == ssmmessages.PayloadFlag && isTerminateFlag(m.Payload) {
				// The plugin asks to end the session (~. or Ctrl-C on port sessions).
				go func() {
					_, _ = r.srv.sessions.Terminate(context.Background(), &sessions.TerminateSessionInput{SessionID: r.sessionID})
				}()
			}
		case ssmmessages.ChannelClosed:
			if sd == sideAgent {
				var cc ssmmessages.ChannelClosedContent
				_ = json.Unmarshal(m.Payload, &cc)
				r.finish(cc.Output)
				return
			}
		}
	}
}

func isTerminateFlag(p []byte) bool {
	return len(p) == 4 && uint32(p[0])<<24|uint32(p[1])<<16|uint32(p[2])<<8|uint32(p[3]) == ssmmessages.FlagTerminateSession
}

func (r *relay) forward(to side, p payload) {
	r.mu.Lock()
	stream := r.streams[to]
	if stream == nil {
		if len(r.queued[to]) < bufferLimit {
			r.queued[to] = append(r.queued[to], p)
		}
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	if err := stream.Send(p.typ, p.data); err != nil {
		r.mu.Lock()
		if len(r.queued[to]) < bufferLimit {
			r.queued[to] = append(r.queued[to], p)
		}
		r.mu.Unlock()
	}
}

// detach handles a dropped connection. If the agent disappears the session
// ends; if the client disappears we wait for ResumeSession.
func (r *relay) detach(sd side, stream *ssmmessages.Stream) {
	r.mu.Lock()
	if r.streams[sd] != stream {
		r.mu.Unlock()
		return
	}
	r.streams[sd] = nil
	closed := r.closed
	if !closed {
		r.grace.Reset(reconnectGrace)
	}
	r.mu.Unlock()
	stream.Close()
	if closed {
		return
	}
	r.srv.log.Infof("session %s: %s disconnected", r.sessionID, sd)
	if sd == sideClient {
		_ = r.srv.sessions.SetStatus(r.sessionID, sessions.StatusDisconnected, "")
	}
}

// expire terminates a session whose peers never (re)connected.
func (r *relay) expire() {
	r.mu.Lock()
	bothUp := r.streams[sideClient] != nil && r.streams[sideAgent] != nil
	r.mu.Unlock()
	if bothUp {
		return
	}
	_ = r.srv.sessions.SetStatus(r.sessionID, sessions.StatusTerminated, "Session timed out waiting for reconnection")
	r.close("Session timed out waiting for reconnection.")
}

// finish ends the session after the agent reported the shell exited.
func (r *relay) finish(output string) {
	_ = r.srv.sessions.SetStatus(r.sessionID, sessions.StatusTerminated, "")
	r.close(output)
}

// close sends channel_closed to both sides and tears the relay down.
func (r *relay) close(output string) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	r.grace.Stop()
	streams := r.streams
	r.mu.Unlock()

	for sd, stream := range streams {
		if stream == nil {
			continue
		}
		if side(sd) == sideClient {
			// Let pending output reach the terminal before closing.
			stream.WaitDrained(2 * time.Second)
		}
		_ = stream.SendControl(ssmmessages.NewChannelClosed(r.sessionID, output))
		stream.Close()
	}
	close(r.done)
	r.srv.remove(r.sessionID)
	r.srv.log.Infof("session %s closed", r.sessionID)
}
