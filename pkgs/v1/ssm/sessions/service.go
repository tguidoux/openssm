// Package sessions implements Session Manager session lifecycle:
// StartSession, ResumeSession, TerminateSession and DescribeSessions, data
// channel tokens, and the queue that hands new sessions to agents. The data
// channel relay itself lives in apps/go/server/sessionws.
package sessions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
	"github.com/tguidoux/openssm/pkgs/v1/kv"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/agenthub"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/documents"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/instances"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
	"github.com/tguidoux/openssm/pkgs/v1/ssmmessages"
)

// Session statuses.
const (
	StatusConnecting   = "Connecting"
	StatusConnected    = "Connected"
	StatusDisconnected = "Disconnected"
	StatusTerminating  = "Terminating"
	StatusTerminated   = "Terminated"
	StatusFailed       = "Failed"
)

const (
	bucketSessions = "sessions"
	// bucketQueue holds sessions not yet delivered to an agent: "<instance>\x00<session>".
	bucketQueue = "session_queue"
	// connectTimeout fails sessions whose agent or client never connects.
	connectTimeout = 5 * time.Minute
)

var sessionUserRe = regexp.MustCompile(`[^a-zA-Z0-9_.@-]`)

type session struct {
	SessionID    string              `json:"sessionId"`
	Target       string              `json:"target"`
	DocumentName string              `json:"documentName"`
	SessionType  string              `json:"sessionType"`
	Parameters   map[string][]string `json:"parameters,omitempty"`
	Owner        string              `json:"owner"`
	Reason       string              `json:"reason,omitempty"`
	Status       string              `json:"status"`
	Details      string              `json:"details,omitempty"`
	StartDate    time.Time           `json:"startDate"`
	EndDate      time.Time           `json:"endDate,omitempty"`
	TokenHash    string              `json:"tokenHash"`
	MaxDuration  string              `json:"maxDuration,omitempty"`
	IdleTimeout  string              `json:"idleTimeout,omitempty"`
	Shell        string              `json:"shell,omitempty"`
	Command      string              `json:"command,omitempty"`
	RunAsUser    string              `json:"runAsUser,omitempty"`
}

func (s *session) active() bool {
	return s.Status != StatusTerminated && s.Status != StatusFailed
}

// --- API shapes ---

// StartSessionInput is the StartSession request.
type StartSessionInput struct {
	Target       string              `json:"Target"`
	DocumentName string              `json:"DocumentName,omitempty"`
	Reason       string              `json:"Reason,omitempty"`
	Parameters   map[string][]string `json:"Parameters,omitempty"`
}

// StartSessionOutput is the StartSession/ResumeSession response.
type StartSessionOutput struct {
	SessionID  string `json:"SessionId"`
	TokenValue string `json:"TokenValue"`
	StreamURL  string `json:"StreamUrl"`
}

// ResumeSessionInput is the ResumeSession request.
type ResumeSessionInput struct {
	SessionID string `json:"SessionId"`
}

// TerminateSessionInput is the TerminateSession request.
type TerminateSessionInput struct {
	SessionID string `json:"SessionId"`
}

// TerminateSessionOutput is the TerminateSession response.
type TerminateSessionOutput struct {
	SessionID string `json:"SessionId"`
}

// SessionFilter is the AWS SessionFilter shape.
type SessionFilter struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// DescribeSessionsInput is the DescribeSessions request.
type DescribeSessionsInput struct {
	State      string          `json:"State"`
	MaxResults int             `json:"MaxResults,omitempty"`
	NextToken  string          `json:"NextToken,omitempty"`
	Filters    []SessionFilter `json:"Filters,omitempty"`
}

// SessionManagerOutputURL is the AWS OutputUrl shape.
type SessionManagerOutputURL struct {
	S3OutputURL         string `json:"S3OutputUrl,omitempty"`
	CloudWatchOutputURL string `json:"CloudWatchOutputUrl,omitempty"`
}

// Session is the AWS Session shape.
type Session struct {
	SessionID          string                   `json:"SessionId"`
	Target             string                   `json:"Target"`
	Status             string                   `json:"Status"`
	StartDate          *types.Timestamp         `json:"StartDate,omitempty"`
	EndDate            *types.Timestamp         `json:"EndDate,omitempty"`
	DocumentName       string                   `json:"DocumentName"`
	Owner              string                   `json:"Owner"`
	Reason             string                   `json:"Reason,omitempty"`
	Details            string                   `json:"Details,omitempty"`
	OutputURL          *SessionManagerOutputURL `json:"OutputUrl,omitempty"`
	MaxSessionDuration string                   `json:"MaxSessionDuration,omitempty"`
}

// DescribeSessionsOutput is the DescribeSessions response.
type DescribeSessionsOutput struct {
	Sessions  []*Session `json:"Sessions"`
	NextToken string     `json:"NextToken,omitempty"`
}

// Observer receives session lifecycle events (metrics).
type Observer interface {
	IncSession(document string)
}

// TerminateHook is called when a session is terminated through the API so
// the relay can close live connections.
type TerminateHook func(sessionID string)

// Options configures the session service.
type Options struct {
	// StreamURL builds the data channel URL for a session ID.
	StreamURL func(sessionID string) string
}

// Service implements Session Manager APIs.
type Service struct {
	store     kv.Store
	docs      *documents.Service
	instances *instances.Service
	hub       *agenthub.Hub
	account   types.Account
	opts      Options
	observer  Observer

	mu    sync.Mutex
	hooks []TerminateHook
}

// New creates the session service.
func New(store kv.Store, docs *documents.Service, inst *instances.Service, hub *agenthub.Hub, account types.Account, opts Options, observer Observer) *Service {
	return &Service{store: store, docs: docs, instances: inst, hub: hub, account: account, opts: opts, observer: observer}
}

// OnTerminate registers a hook invoked after TerminateSession.
func (s *Service) OnTerminate(h TerminateHook) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks = append(s.hooks, h)
}

// Start creates a session and queues it for the target's agent (StartSession).
func (s *Service) Start(ctx context.Context, in *StartSessionInput) (*StartSessionOutput, error) {
	if in.Target == "" {
		return nil, types.NewValidationError("1 validation error detected: Value null at 'target' failed to satisfy constraint: Member must not be null")
	}
	docName := in.DocumentName
	if docName == "" {
		docName = documents.DocSessionShell
	}
	doc, err := s.docs.Resolve(docName, "")
	if err != nil {
		return nil, err
	}
	if doc.DocumentType != documents.TypeSession {
		return nil, types.NewError(types.ErrInvalidDocument, "Document %s is not a Session document.", docName)
	}
	if doc.Content.SessionType == ssmmessages.SessionTypePort {
		return nil, types.NewValidationError("Port forwarding sessions are not supported by OpenSSM yet.")
	}
	params, err := documents.ResolveParameters(doc.Content, in.Parameters)
	if err != nil {
		return nil, err
	}

	owner := callerName(ctx)
	sess := &session{
		SessionID: fmt.Sprintf("%s-%s", sessionUserRe.ReplaceAllString(owner, ""), randomHex(9)),
		Target:    in.Target, DocumentName: doc.Name, SessionType: doc.Content.SessionType, Parameters: in.Parameters,
		Owner: s.account.UserARN(owner), Reason: in.Reason, Status: StatusConnecting, StartDate: types.Now(),
	}
	applyDocumentSettings(sess, doc.Content, params)
	token := randomToken()
	sess.TokenHash = hashToken(token)

	err = s.store.Update(func(tx kv.Tx) error {
		inst, err := s.instances.GetTx(tx, in.Target)
		if err != nil {
			return err
		}
		if inst == nil {
			return types.NewError(types.ErrInvalidInstanceID, "Instance %s is not registered with OpenSSM.", in.Target)
		}
		if s.instances.PingStatus(inst) != instances.PingOnline {
			return types.NewError(types.ErrTargetNotConnected, "%s is not connected.", in.Target)
		}
		if err := kv.PutJSON(tx, bucketSessions, sess.SessionID, sess); err != nil {
			return err
		}
		return tx.Put(bucketQueue, in.Target+"\x00"+sess.SessionID, nil)
	})
	if err != nil {
		return nil, err
	}
	s.hub.Notify(in.Target)
	if s.observer != nil {
		s.observer.IncSession(doc.Name)
	}
	return &StartSessionOutput{SessionID: sess.SessionID, TokenValue: token, StreamURL: s.opts.StreamURL(sess.SessionID)}, nil
}

// Resume issues a new data channel token for an active session (ResumeSession).
func (s *Service) Resume(ctx context.Context, in *ResumeSessionInput) (*StartSessionOutput, error) {
	token := randomToken()
	err := s.store.Update(func(tx kv.Tx) error {
		sess, err := s.load(tx, in.SessionID)
		if err != nil {
			return err
		}
		if !sess.active() {
			return types.NewDoesNotExist("Session %s has already been terminated.", in.SessionID)
		}
		sess.TokenHash = hashToken(token)
		return kv.PutJSON(tx, bucketSessions, sess.SessionID, sess)
	})
	if err != nil {
		return nil, err
	}
	return &StartSessionOutput{SessionID: in.SessionID, TokenValue: token, StreamURL: s.opts.StreamURL(in.SessionID)}, nil
}

// Terminate ends a session (TerminateSession). Terminating an already
// terminated session succeeds, like AWS.
func (s *Service) Terminate(ctx context.Context, in *TerminateSessionInput) (*TerminateSessionOutput, error) {
	if in.SessionID == "" {
		return nil, types.NewValidationError("1 validation error detected: Value null at 'sessionId' failed to satisfy constraint: Member must not be null")
	}
	changed := false
	err := s.store.Update(func(tx kv.Tx) error {
		sess, err := s.load(tx, in.SessionID)
		if err != nil {
			return err
		}
		if !sess.active() {
			return nil
		}
		changed = true
		if err := tx.Delete(bucketQueue, sess.Target+"\x00"+sess.SessionID); err != nil {
			return err
		}
		finish(sess, StatusTerminated, "")
		return kv.PutJSON(tx, bucketSessions, sess.SessionID, sess)
	})
	if err != nil {
		return nil, err
	}
	if changed {
		s.mu.Lock()
		hooks := slices.Clone(s.hooks)
		s.mu.Unlock()
		for _, h := range hooks {
			h(in.SessionID)
		}
	}
	return &TerminateSessionOutput{SessionID: in.SessionID}, nil
}

// Describe lists sessions (DescribeSessions).
func (s *Service) Describe(ctx context.Context, in *DescribeSessionsInput) (*DescribeSessionsOutput, error) {
	if in.State != "Active" && in.State != "History" {
		return nil, types.NewValidationError("1 validation error detected: Value '%s' at 'state' failed to satisfy constraint: Member must satisfy enum value set: [Active, History]", in.State)
	}
	var all []*Session
	err := s.store.View(func(r kv.Reader) error {
		list, err := kv.ListJSON[session](r, bucketSessions, "")
		if err != nil {
			return err
		}
		sort.Slice(list, func(i, j int) bool { return list[i].StartDate.After(list[j].StartDate) })
		for i := range list {
			sess := &list[i]
			if (in.State == "Active") != sess.active() {
				continue
			}
			ok, err := matchFilters(sess, in.Filters)
			if err != nil {
				return err
			}
			if ok {
				all = append(all, toSession(sess))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	page, next, err := types.Paginate(all, in.NextToken, in.MaxResults, 20, 200)
	if err != nil {
		return nil, err
	}
	if page == nil {
		page = []*Session{}
	}
	return &DescribeSessionsOutput{Sessions: page, NextToken: next}, nil
}

// --- relay / agent integration ---

// ErrInvalidToken is returned when a data channel credential is wrong.
var ErrInvalidToken = errors.New("sessions: invalid token")

// AuthorizeClient validates the token sent in open_data_channel.
func (s *Service) AuthorizeClient(sessionID, token string) error {
	sess, err := s.get(sessionID)
	if err != nil || !sess.active() {
		return ErrInvalidToken
	}
	if subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(sess.TokenHash)) != 1 {
		return ErrInvalidToken
	}
	return nil
}

// AuthorizeAgent checks that instanceID is the target of an active session.
func (s *Service) AuthorizeAgent(sessionID, instanceID string) error {
	sess, err := s.get(sessionID)
	if err != nil || !sess.active() || sess.Target != instanceID {
		return ErrInvalidToken
	}
	return nil
}

// PendingFor claims undelivered sessions for an agent (agentapi.SessionSource).
func (s *Service) PendingFor(instanceID string) ([]agentproto.SessionPayload, error) {
	var out []agentproto.SessionPayload
	err := s.store.Update(func(tx kv.Tx) error {
		entries, err := tx.List(bucketQueue, instanceID+"\x00")
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := tx.Delete(bucketQueue, e.Key); err != nil {
				return err
			}
			sess, err := s.load(tx, strings.TrimPrefix(e.Key, instanceID+"\x00"))
			if err != nil || !sess.active() {
				continue
			}
			out = append(out, agentproto.SessionPayload{
				SessionID: sess.SessionID, DocumentName: sess.DocumentName, SessionType: sess.SessionType,
				Shell: sess.Shell, Command: sess.Command, RunAsUser: sess.RunAsUser,
				Parameters: map[string]string{"idleSessionTimeout": sess.IdleTimeout, "maxSessionDuration": sess.MaxDuration},
			})
		}
		return nil
	})
	return out, err
}

// SetStatus records relay connection state changes.
func (s *Service) SetStatus(sessionID, status, details string) error {
	return s.store.Update(func(tx kv.Tx) error {
		sess, err := s.load(tx, sessionID)
		if err != nil {
			return err
		}
		if !sess.active() {
			return nil
		}
		if status == StatusTerminated || status == StatusFailed {
			finish(sess, status, details)
		} else {
			sess.Status = status
		}
		return kv.PutJSON(tx, bucketSessions, sess.SessionID, sess)
	})
}

// IsActive reports whether the session can still carry data.
func (s *Service) IsActive(sessionID string) bool {
	sess, err := s.get(sessionID)
	return err == nil && sess.active()
}

// Sweep fails sessions that never connected within connectTimeout.
func (s *Service) Sweep() (int, error) {
	n := 0
	err := s.store.Update(func(tx kv.Tx) error {
		list, err := kv.ListJSON[session](tx, bucketSessions, "")
		if err != nil {
			return err
		}
		now := types.Now()
		for i := range list {
			sess := &list[i]
			if sess.Status != StatusConnecting || now.Sub(sess.StartDate) < connectTimeout {
				continue
			}
			if err := tx.Delete(bucketQueue, sess.Target+"\x00"+sess.SessionID); err != nil {
				return err
			}
			finish(sess, StatusFailed, "Session did not connect in time")
			if err := kv.PutJSON(tx, bucketSessions, sess.SessionID, sess); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// CountActive returns the number of active sessions.
func (s *Service) CountActive() (int, error) {
	n := 0
	err := s.store.View(func(r kv.Reader) error {
		list, err := kv.ListJSON[session](r, bucketSessions, "")
		for i := range list {
			if list[i].active() {
				n++
			}
		}
		return err
	})
	return n, err
}

// --- internals ---

func finish(sess *session, status, details string) {
	sess.Status = status
	sess.Details = details
	sess.EndDate = types.Now()
}

func (s *Service) get(sessionID string) (*session, error) {
	var sess *session
	err := s.store.View(func(r kv.Reader) error {
		var err error
		sess, err = s.load(r, sessionID)
		return err
	})
	return sess, err
}

func (s *Service) load(r kv.Reader, sessionID string) (*session, error) {
	var sess session
	err := kv.GetJSON(r, bucketSessions, sessionID, &sess)
	if errors.Is(err, kv.ErrNotFound) {
		return nil, types.NewDoesNotExist("Session %s does not exist.", sessionID)
	}
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

// applyDocumentSettings copies shell/command/timeouts from the document.
func applyDocumentSettings(sess *session, c *documents.Content, params map[string][]string) {
	str := func(m map[string]any, k string) string {
		if v, ok := m[k]; ok && v != nil {
			return fmt.Sprint(v)
		}
		return ""
	}
	if c.Inputs != nil {
		sess.IdleTimeout = str(c.Inputs, "idleSessionTimeout")
		sess.MaxDuration = str(c.Inputs, "maxSessionDuration")
		if enabled, _ := c.Inputs["runAsEnabled"].(bool); enabled {
			sess.RunAsUser = str(c.Inputs, "runAsDefaultUser")
		}
		if profile, ok := c.Inputs["shellProfile"].(map[string]any); ok {
			sess.Shell = str(profile, "linux")
		}
	}
	if c.Properties != nil {
		if props, ok := documents.Interpolate(c.Properties, params).(map[string]any); ok {
			if linux, ok := props["linux"].(map[string]any); ok {
				sess.Command = str(linux, "commands")
			}
		}
	}
}

func toSession(s *session) *Session {
	return &Session{
		SessionID: s.SessionID, Target: s.Target, Status: s.Status, StartDate: types.NewTimestamp(s.StartDate),
		EndDate: types.NewTimestamp(s.EndDate), DocumentName: s.DocumentName, Owner: s.Owner, Reason: s.Reason,
		Details: s.Details, OutputURL: &SessionManagerOutputURL{}, MaxSessionDuration: s.MaxDuration,
	}
}

func matchFilters(s *session, filters []SessionFilter) (bool, error) {
	for _, f := range filters {
		switch f.Key {
		case "InvokedAfter", "InvokedBefore":
			t, err := time.Parse(time.RFC3339, f.Value)
			if err != nil {
				return false, types.NewError("InvalidFilterValue", "Invalid timestamp %q", f.Value)
			}
			if f.Key == "InvokedAfter" && s.StartDate.Before(t) || f.Key == "InvokedBefore" && s.StartDate.After(t) {
				return false, nil
			}
		case "Target":
			if s.Target != f.Value {
				return false, nil
			}
		case "Owner":
			if s.Owner != f.Value && !strings.HasSuffix(s.Owner, "/"+f.Value) {
				return false, nil
			}
		case "Status":
			if s.Status != f.Value {
				return false, nil
			}
		case "SessionId":
			if s.SessionID != f.Value {
				return false, nil
			}
		default:
			return false, types.NewError(types.ErrInvalidFilterKey, "The specified key is not valid: %s", f.Key)
		}
	}
	return true, nil
}

func callerName(ctx context.Context) string {
	if c, ok := types.CallerFrom(ctx); ok && c.Name != "" {
		return c.Name
	}
	return "openssm"
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:2*n-1]
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}
