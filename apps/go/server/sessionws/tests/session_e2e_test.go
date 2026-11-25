package sessionws_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tguidoux/openssm/apps/go/server/agentapi"
	"github.com/tguidoux/openssm/apps/go/server/sessionws"
	"github.com/tguidoux/openssm/pkgs/v1/agent"
	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
	"github.com/tguidoux/openssm/pkgs/v1/kv/memory"
	"github.com/tguidoux/openssm/pkgs/v1/logger"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/agenthub"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/commands"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/documents"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/instances"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/sessions"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
	"github.com/tguidoux/openssm/pkgs/v1/ssmmessages"
)

var log = logger.New("session-test", logger.UncontextualLoggerType)

type stack struct {
	srv      *httptest.Server
	sessions *sessions.Service
	inst     *instances.Service
	agent    *agent.Agent
	docs     *documents.Service
}

func newStack(t *testing.T) *stack {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("PTY sessions require a Unix host")
	}
	store := memory.New()
	tg := tags.New(store)
	docs, err := documents.New(store, tg, types.Account{AccountID: "1", Region: "us-east-1"})
	require.NoError(t, err)
	inst := instances.New(store, tg, instances.Options{})
	hub := agenthub.New()
	cmds := commands.New(store, docs, inst, hub, nil)
	st := &stack{inst: inst, docs: docs}
	var base string
	st.sessions = sessions.New(store, docs, inst, hub, types.Account{AccountID: "1", Region: "us-east-1"}, sessions.Options{
		StreamURL: func(id string) string {
			return "ws" + strings.TrimPrefix(base, "http") + sessionws.ClientPathPrefix + id
		},
	}, nil)
	agentHandler := agentapi.New(inst, cmds, st.sessions, hub, log)
	relay := sessionws.New(st.sessions, agentHandler.InstanceFromRequest, nil, log)
	mux := http.NewServeMux()
	mux.Handle(agentproto.PathPrefix, agentHandler)
	mux.HandleFunc("GET "+agentproto.PathSession+"{sessionId}", relay.ServeAgent)
	mux.HandleFunc("GET "+sessionws.ClientPathPrefix+"{sessionId}", relay.ServeClient)
	st.srv = httptest.NewServer(mux)
	base = st.srv.URL
	t.Cleanup(st.srv.Close)

	act, err := inst.CreateActivation(context.Background(), &instances.CreateActivationInput{IamRole: "r"})
	require.NoError(t, err)
	st.agent = agent.New(agent.Config{
		ServerURL: st.srv.URL, ActivationID: act.ActivationID, ActivationCode: act.ActivationCode,
		PollWait: 2 * time.Second, SessionHandler: &agent.ShellSessionHandler{Shell: "/bin/sh", Log: log},
	}, log)
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, st.agent.Register(ctx))
	done := make(chan struct{})
	go func() { _ = st.agent.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return st
}

// pluginClient mimics session-manager-plugin: it opens the data channel,
// sends open_data_channel, answers the handshake, and exchanges stream data.
type pluginClient struct {
	t        *testing.T
	ws       *websocket.Conn
	writeMu  sync.Mutex
	seq      int64
	expected int64

	mu          sync.Mutex
	output      strings.Builder
	sessionType string
	closed      chan string
	ready       chan struct{}
}

func dialPlugin(t *testing.T, out *sessions.StartSessionOutput) *pluginClient {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial(out.StreamURL, nil)
	require.NoError(t, err)
	open, _ := json.Marshal(ssmmessages.OpenDataChannelInput{MessageSchemaVersion: "1.0", RequestID: "r", TokenValue: out.TokenValue, ClientID: "c", ClientVersion: "1.3.0.0"})
	require.NoError(t, ws.WriteMessage(websocket.TextMessage, open))
	c := &pluginClient{t: t, ws: ws, closed: make(chan string, 1), ready: make(chan struct{})}
	go c.readLoop()
	t.Cleanup(func() { ws.Close() })
	return c
}

func (c *pluginClient) write(m *ssmmessages.Message) {
	b, err := m.Marshal()
	require.NoError(c.t, err)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.ws.WriteMessage(websocket.BinaryMessage, b)
}

func (c *pluginClient) send(payloadType uint32, payload []byte) {
	c.writeMu.Lock()
	seq := c.seq
	c.seq++
	c.writeMu.Unlock()
	c.write(ssmmessages.NewStreamMessage(ssmmessages.InputStreamData, seq, payloadType, payload))
}

func (c *pluginClient) readLoop() {
	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		m, err := ssmmessages.Unmarshal(data)
		if err != nil {
			c.t.Errorf("plugin received invalid frame: %v", err)
			return
		}
		switch m.MessageType {
		case ssmmessages.OutputStreamData:
			c.write(ssmmessages.NewAcknowledge(m))
			if m.SequenceNumber != c.expected {
				continue // duplicate
			}
			c.expected++
			switch m.PayloadType {
			case ssmmessages.PayloadHandshakeRequest:
				var req ssmmessages.HandshakeRequest
				require.NoError(c.t, json.Unmarshal(m.Payload, &req))
				var st ssmmessages.SessionTypeRequest
				require.NoError(c.t, json.Unmarshal(req.RequestedClientActions[0].ActionParameters, &st))
				c.mu.Lock()
				c.sessionType = st.SessionType
				c.mu.Unlock()
				resp, _ := json.Marshal(ssmmessages.HandshakeResponse{ClientVersion: "1.3.0.0", ProcessedClientActions: []ssmmessages.ProcessedClientAction{{ActionType: "SessionType", ActionStatus: 1}}})
				c.send(ssmmessages.PayloadHandshakeResponse, resp)
			case ssmmessages.PayloadHandshakeComplete:
				size, _ := json.Marshal(ssmmessages.SizeData{Cols: 120, Rows: 40})
				c.send(ssmmessages.PayloadSize, size)
				close(c.ready)
			case ssmmessages.PayloadOutput:
				c.mu.Lock()
				c.output.Write(m.Payload)
				c.mu.Unlock()
			}
		case ssmmessages.ChannelClosed:
			var cc ssmmessages.ChannelClosedContent
			_ = json.Unmarshal(m.Payload, &cc)
			c.closed <- cc.Output
			return
		}
	}
}

func (c *pluginClient) out() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.output.String()
}

func (c *pluginClient) waitOutput(t *testing.T, substr string) {
	t.Helper()
	require.Eventually(t, func() bool { return strings.Contains(c.out(), substr) }, 15*time.Second, 20*time.Millisecond, "output so far: %q", c.out())
}

func TestInteractiveShellSession(t *testing.T) {
	st := newStack(t)
	out, err := st.sessions.Start(context.Background(), &sessions.StartSessionInput{Target: st.agent.InstanceID()})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out.SessionID, "openssm-"))

	c := dialPlugin(t, out)
	select {
	case <-c.ready:
	case <-time.After(15 * time.Second):
		t.Fatal("handshake did not complete")
	}
	assert.Equal(t, "Standard_Stream", c.sessionType)

	require.Eventually(t, func() bool {
		active, _ := st.sessions.Describe(context.Background(), &sessions.DescribeSessionsInput{State: "Active"})
		return len(active.Sessions) == 1 && active.Sessions[0].Status == sessions.StatusConnected
	}, 5*time.Second, 20*time.Millisecond)

	c.send(ssmmessages.PayloadOutput, []byte("echo hello-$((40+2))\r"))
	c.waitOutput(t, "hello-42")

	c.send(ssmmessages.PayloadOutput, []byte("stty size\r"))
	c.waitOutput(t, "40 120")

	c.send(ssmmessages.PayloadOutput, []byte("exit\r"))
	select {
	case <-c.closed:
	case <-time.After(15 * time.Second):
		t.Fatal("channel was not closed after exit")
	}
	require.Eventually(t, func() bool {
		hist, _ := st.sessions.Describe(context.Background(), &sessions.DescribeSessionsInput{State: "History"})
		return len(hist.Sessions) == 1 && hist.Sessions[0].Status == sessions.StatusTerminated
	}, 5*time.Second, 20*time.Millisecond)
}

func TestTerminateSessionClosesChannel(t *testing.T) {
	st := newStack(t)
	out, err := st.sessions.Start(context.Background(), &sessions.StartSessionInput{Target: st.agent.InstanceID()})
	require.NoError(t, err)
	c := dialPlugin(t, out)
	<-c.ready
	_, err = st.sessions.Terminate(context.Background(), &sessions.TerminateSessionInput{SessionID: out.SessionID})
	require.NoError(t, err)
	select {
	case <-c.closed:
	case <-time.After(10 * time.Second):
		t.Fatal("client was not notified of termination")
	}
}

func TestNonInteractiveCommandSession(t *testing.T) {
	st := newStack(t)
	out, err := st.sessions.Start(context.Background(), &sessions.StartSessionInput{
		Target: st.agent.InstanceID(), DocumentName: documents.DocStartNonInteractive,
		Parameters: map[string][]string{"command": {"echo non-interactive-ok"}},
	})
	require.NoError(t, err)
	c := dialPlugin(t, out)
	select {
	case msg := <-c.closed:
		assert.Contains(t, msg, "exited with code 0")
	case <-time.After(15 * time.Second):
		t.Fatal("session did not end")
	}
	assert.Contains(t, c.out(), "non-interactive-ok")
}

func TestClientTokenIsRequired(t *testing.T) {
	st := newStack(t)
	out, err := st.sessions.Start(context.Background(), &sessions.StartSessionInput{Target: st.agent.InstanceID()})
	require.NoError(t, err)
	out.TokenValue = "wrong"
	ws, _, err := websocket.DefaultDialer.Dial(out.StreamURL, nil)
	require.NoError(t, err)
	defer ws.Close()
	open, _ := json.Marshal(ssmmessages.OpenDataChannelInput{TokenValue: "wrong"})
	require.NoError(t, ws.WriteMessage(websocket.TextMessage, open))
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = ws.ReadMessage()
	assert.Error(t, err, "connection is closed for invalid tokens")
}

func TestAgentEndpointRejectsOtherInstances(t *testing.T) {
	st := newStack(t)
	out, err := st.sessions.Start(context.Background(), &sessions.StartSessionInput{Target: st.agent.InstanceID()})
	require.NoError(t, err)
	url := "ws" + strings.TrimPrefix(st.srv.URL, "http") + agentproto.PathSession + out.SessionID
	_, resp, err := websocket.DefaultDialer.Dial(url, http.Header{"Authorization": {"Bearer mi-00000000000000000.x"}})
	require.Error(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
