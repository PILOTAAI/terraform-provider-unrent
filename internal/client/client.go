// Package client talks to the Unrent public API (https://api.unrent.dev/v1, docs/api.md) for the Terraform /
// OpenTofu provider: bearer-token requests through the generated operation table (ops_gen.go), errors as the API
// words them, jobs held for passkey approvals (202 approval_required) waited for — never bypassed — and jobs
// followed until the server reports them final. Secret values are sealed here, to the server's public age
// recipient, before they are sent (seal.go); the API never returns them.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Operation is one route of the API (generated from its OpenAPI document).
type Operation struct {
	ID, Method, Path, Scope string
	// Approval: the operation can answer 202 approval_required.
	Approval bool
}

// Params are an operation's path parameters ({org} is filled in from the client).
type Params map[string]string

// Client is configured once per provider instance.
type Client struct {
	// API is the API origin, e.g. https://api.unrent.dev (requests go to API + "/v1").
	API string
	// Org is the organisation every token belongs to (org_…).
	Org   string
	Token string
	// Refresh, when set, gets a new token after a 401 (a GitHub Actions token lasts ten minutes).
	Refresh         func(ctx context.Context) (string, error)
	HTTP            *http.Client
	UserAgent       string
	ApprovalTimeout time.Duration
	JobTimeout      time.Duration
	Poll            time.Duration
	// Log receives progress lines (approval pages to open, jobs waited for).
	Log func(ctx context.Context, msg string)
	// Now and Sleep are swappable in tests.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
}

// Error is an answer of the API that is not a success.
type Error struct {
	Op      string
	Status  int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("Unrent API %s: %d %s", e.Op, e.Status, e.Message)
}

// IsNotFound reports whether err is a 404 of the API (the resource is gone, or belongs to another organisation).
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// JobRef is one job an operation issued.
type JobRef struct {
	JobID   string `json:"job_id"`
	AgentID string `json:"agent_id,omitempty"`
	Kind    string `json:"kind,omitempty"`
	State   string `json:"state,omitempty"`
}

// Approval is a 202 approval_required answer: the job waits for a person's passkey in the console.
type Approval struct {
	Status       string   `json:"status"`
	JobID        string   `json:"job_id"`
	DeploymentID string   `json:"deployment_id,omitempty"`
	Kind         string   `json:"kind"`
	Class        string   `json:"class"`
	Resource     string   `json:"resource"`
	ExpiresAt    string   `json:"expires_at"`
	Digest       string   `json:"digest"`
	ApproveURL   string   `json:"approve_url"`
	Message      string   `json:"message"`
	Jobs         []JobRef `json:"jobs"`
	Approvals    struct {
		Required int `json:"required"`
	} `json:"approvals"`
}

// JobIDs are every job the approval covers (one per server for a delete, else the one).
func (a *Approval) JobIDs() []string {
	if len(a.Jobs) == 0 {
		return []string{a.JobID}
	}
	ids := make([]string, 0, len(a.Jobs))
	for _, j := range a.Jobs {
		ids = append(ids, j.JobID)
	}
	return ids
}

// ApprovalError: a held job was not approved (in time, or at all). The provider fails with the approval page.
type ApprovalError struct {
	Approval Approval
	Reason   string
}

func (e *ApprovalError) Error() string {
	a := e.Approval
	return fmt.Sprintf("%s (%s %s) needs %d approver passkey(s): %s. Approve it at %s (the passkey signs digest %s; the job expires at %s), then run apply again.",
		a.Kind, a.Class, a.Resource, max(a.Approvals.Required, 1), e.Reason, a.ApproveURL, a.Digest, a.ExpiresAt)
}

// JobError: a job the server ran did not succeed.
type JobError struct {
	JobID, Kind, State, Detail string
}

func (e *JobError) Error() string {
	d := ""
	if e.Detail != "" {
		d = ": " + e.Detail
	}
	return fmt.Sprintf("job %s (%s) %s%s", e.JobID, e.Kind, e.State, d)
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) log(ctx context.Context, f string, a ...any) {
	if c.Log != nil {
		c.Log(ctx, fmt.Sprintf(f, a...))
	}
}

func (c *Client) poll() time.Duration {
	if c.Poll > 0 {
		return c.Poll
	}
	return 3 * time.Second
}

// URL is the absolute URL of an operation with its path parameters filled in (each one escaped).
func (c *Client) URL(op Operation, p Params, q url.Values) (string, error) {
	var b strings.Builder
	b.WriteString(strings.TrimRight(c.API, "/"))
	b.WriteString("/v1")
	rest := op.Path
	for {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			b.WriteString(rest)
			break
		}
		j := strings.IndexByte(rest[i:], '}')
		if j < 0 {
			return "", fmt.Errorf("%s: malformed path %s", op.ID, op.Path)
		}
		name := rest[i+1 : i+j]
		v := p[name]
		if name == "org" && v == "" {
			v = c.Org
		}
		if v == "" {
			return "", fmt.Errorf("%s: missing path parameter %s", op.ID, name)
		}
		b.WriteString(rest[:i])
		b.WriteString(url.PathEscape(v))
		rest = rest[i+j+1:]
	}
	if len(q) > 0 {
		b.WriteString("?" + q.Encode())
	}
	return b.String(), nil
}

// raw sends one request: 429 waits Retry-After (three times), a GET is retried on 5xx and network errors, a 401
// refreshes the token once when it can. The body is returned with the status whatever it is.
func (c *Client) raw(ctx context.Context, op Operation, p Params, q url.Values, body any) (int, []byte, error) {
	u, err := c.URL(op, p, q)
	if err != nil {
		return 0, nil, err
	}
	var payload []byte
	if body != nil {
		if payload, err = json.Marshal(body); err != nil {
			return 0, nil, err
		}
	}
	h := c.HTTP
	if h == nil {
		h = &http.Client{Timeout: 60 * time.Second}
	}
	refreshed := false
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, op.Method, u, bytes.NewReader(payload))
		if err != nil {
			return 0, nil, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.Token)
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}
		res, err := h.Do(req)
		if err != nil {
			if op.Method == http.MethodGet && attempt < 3 && ctx.Err() == nil {
				if e := c.sleep(ctx, time.Duration(attempt+1)*time.Second); e != nil {
					return 0, nil, e
				}
				continue
			}
			return 0, nil, fmt.Errorf("Unrent API %s: %w", op.ID, err)
		}
		b, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
		res.Body.Close()
		if err != nil {
			return 0, nil, err
		}
		switch {
		case res.StatusCode == http.StatusTooManyRequests && attempt < 3:
			wait, _ := strconv.Atoi(res.Header.Get("Retry-After"))
			if wait <= 0 || wait > 120 {
				wait = 60
			}
			c.log(ctx, "rate limited: waiting %ds", wait)
			if e := c.sleep(ctx, time.Duration(wait)*time.Second); e != nil {
				return 0, nil, e
			}
			continue
		case res.StatusCode >= 500 && op.Method == http.MethodGet && attempt < 3:
			if e := c.sleep(ctx, time.Duration(attempt+1)*time.Second); e != nil {
				return 0, nil, e
			}
			continue
		case res.StatusCode == http.StatusUnauthorized && c.Refresh != nil && !refreshed:
			refreshed = true
			t, err := c.Refresh(ctx)
			if err != nil {
				return 0, nil, fmt.Errorf("the token expired and could not be renewed: %w", err)
			}
			c.Token = t
			continue
		}
		return res.StatusCode, b, nil
	}
}

func apiError(op Operation, status int, b []byte) error {
	var e struct {
		Error  string `json:"error"`
		Detail string `json:"detail"`
	}
	msg := strings.TrimSpace(string(b))
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		msg = e.Error
		// A conflict's code comes with why (P21: a sandbox server that runs production names it).
		if e.Detail != "" {
			msg += ": " + e.Detail
		}
	}
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return &Error{Op: op.ID, Status: status, Message: msg}
}

// Get calls a read operation and decodes its answer into out.
func (c *Client) Get(ctx context.Context, op Operation, p Params, q url.Values, out any) error {
	status, b, err := c.raw(ctx, op, p, q, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return apiError(op, status, b)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

// Do calls a change. A 202 approval_required answer is reported (the approval page, the digest the passkey signs)
// and waited for, up to ApprovalTimeout: approved, Do returns the approval (its jobs are now on their way; the
// answer body is not decoded); refused or expired, an ApprovalError. Otherwise the answer is decoded into out and
// the approval is nil.
func (c *Client) Do(ctx context.Context, op Operation, p Params, body any, out any) (*Approval, error) {
	status, b, err := c.raw(ctx, op, p, nil, body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusAccepted {
		var a Approval
		if json.Unmarshal(b, &a) == nil && a.Status == "approval_required" && a.JobID != "" {
			return &a, c.awaitApproval(ctx, &a)
		}
	}
	if status < 200 || status >= 300 {
		return nil, apiError(op, status, b)
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return nil, fmt.Errorf("Unrent API %s: %w", op.ID, err)
		}
	}
	return nil, nil
}

// Job is GET /jobs/{job} (the fields the provider reads).
type Job struct {
	JobID      string          `json:"job_id"`
	Kind       string          `json:"kind"`
	Class      string          `json:"class"`
	State      string          `json:"state"`
	Resource   string          `json:"resource"`
	ExpiresAt  string          `json:"expires_at"`
	Digest     string          `json:"digest"`
	Finished   bool            `json:"finished"`
	ApproveURL string          `json:"approve_url"`
	Result     json.RawMessage `json:"result"`
	Approvals  struct {
		Required int `json:"required"`
	} `json:"approvals"`
}

// WaitApproval waits for a job that is held for passkeys (found while following a deployment) to be approved.
func (c *Client) WaitApproval(ctx context.Context, id string) error {
	j, err := c.job(ctx, id)
	if err != nil || j.State != "pending_approval" {
		return err
	}
	a := Approval{Status: "approval_required", JobID: id, Kind: j.Kind, Class: j.Class, Resource: j.Resource, ExpiresAt: j.ExpiresAt, Digest: j.Digest, ApproveURL: j.ApproveURL}
	a.Approvals.Required = j.Approvals.Required
	return c.awaitApproval(ctx, &a)
}

// Tick sleeps one poll interval in a wait loop (iteration i) and errors once the loop has run past JobTimeout.
func (c *Client) Tick(ctx context.Context, i int) error {
	if time.Duration(i)*c.poll() >= c.JobTimeout {
		return fmt.Errorf("not done within job_timeout (%s)", c.JobTimeout)
	}
	return c.sleep(ctx, c.poll())
}

func (c *Client) job(ctx context.Context, id string) (*Job, error) {
	var j Job
	if err := c.Get(ctx, OpGetJob, Params{"job": id}, nil, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

func (c *Client) awaitApproval(ctx context.Context, a *Approval) error {
	timeout := c.ApprovalTimeout
	c.log(ctx, "%s %s needs approval: open %s and approve it with your passkey (digest %s); waiting up to %s", a.Kind, a.Resource, a.ApproveURL, a.Digest, timeout)
	if timeout <= 0 {
		return &ApprovalError{Approval: *a, Reason: "not approved yet (approval_timeout is 0: the provider does not wait)"}
	}
	deadline := c.now().Add(timeout)
	for {
		j, err := c.job(ctx, a.JobID)
		if err != nil {
			return err
		}
		switch j.State {
		case "pending_approval":
		case "expired", "rejected", "cancelled":
			return &ApprovalError{Approval: *a, Reason: "the job was " + j.State + " without enough approvals"}
		default:
			c.log(ctx, "%s %s approved", a.Kind, a.Resource)
			return nil
		}
		if !c.now().Before(deadline) {
			return &ApprovalError{Approval: *a, Reason: fmt.Sprintf("not approved within %s; it keeps waiting in the console", timeout)}
		}
		if err := c.sleep(ctx, c.poll()); err != nil {
			return err
		}
	}
}

// WaitJobs follows jobs until the server reports each final, up to JobTimeout; any state but succeeded is an error.
func (c *Client) WaitJobs(ctx context.Context, ids []string) error {
	deadline := c.now().Add(c.JobTimeout)
	for _, id := range ids {
		if id == "" {
			continue
		}
		for {
			j, err := c.job(ctx, id)
			if err != nil {
				return err
			}
			if j.State == "pending_approval" {
				// Asked again while an earlier request still waits (the API reuses it): wait for that approval.
				if err := c.WaitApproval(ctx, id); err != nil {
					return err
				}
				continue
			}
			if j.Finished {
				if j.State != "succeeded" {
					return &JobError{JobID: id, Kind: j.Kind, State: j.State, Detail: resultDetail(j.Result)}
				}
				break
			}
			if c.JobTimeout <= 0 || !c.now().Before(deadline) {
				return &JobError{JobID: id, Kind: j.Kind, State: j.State, Detail: fmt.Sprintf("not final within job_timeout (%s): is the server connected?", c.JobTimeout)}
			}
			if err := c.sleep(ctx, c.poll()); err != nil {
				return err
			}
		}
	}
	return nil
}

func resultDetail(raw json.RawMessage) string {
	var r map[string]any
	if json.Unmarshal(raw, &r) != nil {
		return ""
	}
	for _, k := range []string{"error", "detail", "reason", "message"} {
		if s, ok := r[k].(string); ok && s != "" {
			if len(s) > 300 {
				s = s[:300]
			}
			return s
		}
	}
	return ""
}

// Me is GET /me.
type Me struct {
	Kind        string   `json:"kind"`
	Role        string   `json:"role"`
	Scopes      []string `json:"scopes"`
	Environment *string  `json:"environment"`
	Org         struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"org"`
}

// Me asks who the token acts for (and so its organisation).
func (c *Client) Me(ctx context.Context) (*Me, error) {
	var m Me
	if err := c.Get(ctx, OpGetMe, nil, nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
