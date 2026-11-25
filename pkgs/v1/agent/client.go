// Package agent implements openssm-agent: it registers a host with an
// OpenSSM hybrid activation, keeps it Online with heartbeats, long-polls for
// Run Command invocations and Session Manager sessions, executes them, and
// reports results.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
)

// Client talks to the OpenSSM agent API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient creates an API client. baseURL is the OpenSSM API endpoint,
// e.g. "http://localhost:9330".
func NewClient(baseURL, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: agentproto.MaxPollWait + 15*time.Second}
	}
	return &Client{baseURL: strings.TrimSuffix(baseURL, "/"), token: token, http: httpClient}
}

// Token returns the instance token.
func (c *Client) Token() string { return c.token }

// BaseURL returns the server base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// Register registers the host and stores the returned token in the client.
func (c *Client) Register(ctx context.Context, req *agentproto.RegisterRequest) (*agentproto.RegisterResponse, error) {
	var out agentproto.RegisterResponse
	if err := c.do(ctx, agentproto.PathRegister, req, &out, false); err != nil {
		return nil, err
	}
	c.token = out.Token
	return &out, nil
}

// Heartbeat reports host info.
func (c *Client) Heartbeat(ctx context.Context, info agentproto.AgentInfo) error {
	return c.do(ctx, agentproto.PathHeartbeat, &agentproto.HeartbeatRequest{AgentInfo: info}, nil, true)
}

// Poll long-polls for work.
func (c *Client) Poll(ctx context.Context, wait time.Duration) (*agentproto.PollResponse, error) {
	var out agentproto.PollResponse
	err := c.do(ctx, agentproto.PathPoll, &agentproto.PollRequest{WaitSeconds: int(wait.Seconds())}, &out, true)
	return &out, err
}

// Report sends a command result.
func (c *Client) Report(ctx context.Context, commandID string, res *agentproto.CommandResult) error {
	return c.do(ctx, agentproto.PathResult+commandID, res, nil, true)
}

// APIError is a non-2xx response from the agent API.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("agent API %d %s: %s", e.Status, e.Code, e.Message)
}

func (c *Client) do(ctx context.Context, path string, in, out any, auth bool) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var body agentproto.Error
		if json.Unmarshal(data, &body) != nil {
			body.Message = strings.TrimSpace(string(data))
		}
		return &APIError{Status: resp.StatusCode, Code: body.Code, Message: body.Message}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}
