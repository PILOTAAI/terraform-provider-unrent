package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"filippo.io/age/armor"

	"unrent.dev/terraform-provider-unrent/internal/client/opsgen"
)

// fakeAPI answers like the Unrent API for the paths a test scripts.
type fakeAPI struct {
	mu      sync.Mutex
	t       *testing.T
	routes  map[string]func(w http.ResponseWriter, r *http.Request, body map[string]any)
	calls   []string
	bearers []string
}

func newFake(t *testing.T) (*fakeAPI, *Client) {
	f := &fakeAPI{t: t, routes: map[string]func(http.ResponseWriter, *http.Request, map[string]any){}}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		key := r.Method + " " + r.URL.EscapedPath()
		f.calls = append(f.calls, key)
		f.bearers = append(f.bearers, r.Header.Get("Authorization"))
		h := f.routes[key]
		f.mu.Unlock()
		var body map[string]any
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			if err := json.Unmarshal(b, &body); err != nil {
				t.Errorf("%s: body is not JSON: %s", key, b)
			}
		}
		if h == nil {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		h(w, r, body)
	}))
	t.Cleanup(s.Close)
	var slept time.Duration
	now := time.Unix(1_800_000_000, 0)
	c := &Client{API: s.URL, Org: "org_aaaaaaaaaaaaaaaa", Token: "unr_pat_test", ApprovalTimeout: time.Minute, JobTimeout: time.Minute, Poll: time.Second,
		Now:   func() time.Time { return now.Add(slept) },
		Sleep: func(_ context.Context, d time.Duration) error { slept += d; return nil },
	}
	return f, c
}

func reply(status int, v any) func(http.ResponseWriter, *http.Request, map[string]any) {
	return func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
}

const org = "/v1/orgs/org_aaaaaaaaaaaaaaaa"
const job = "job_0123456789ABCDEFGHJKMNPQRS"

func approval() map[string]any {
	return map[string]any{"status": "approval_required", "job_id": job, "kind": "app.delete", "class": "destructive", "resource": "app:production/web", "expires_at": "2026-11-16T12:00:00Z",
		"approvals": map[string]any{"required": 1}, "digest": "dGhlLWRpZ2VzdA", "approve_url": "https://app.unrent.dev/org_aaaaaaaaaaaaaaaa/approvals/" + job, "jobs": []any{map[string]any{"job_id": job, "state": "pending_approval"}}}
}

func TestURLEscapesEveryParameterAndFillsTheOrg(t *testing.T) {
	c := &Client{API: "https://api.unrent.dev/", Org: "org_aaaaaaaaaaaaaaaa"}
	u, err := c.URL(OpRemoveDomain, Params{"host": "a/../b?x"}, nil)
	if err != nil || u != "https://api.unrent.dev/v1/orgs/org_aaaaaaaaaaaaaaaa/domains/a%2F..%2Fb%3Fx" {
		t.Fatalf("%s %v", u, err)
	}
	if _, err := c.URL(OpGetService, Params{"env": "production"}, nil); err == nil || !strings.Contains(err.Error(), "missing path parameter name") {
		t.Fatalf("a missing parameter must be refused: %v", err)
	}
}

func TestApprovalRequiredIsWaitedForUntilAPersonApproves(t *testing.T) {
	f, c := newFake(t)
	var logs []string
	c.Log = func(_ context.Context, m string) { logs = append(logs, m) }
	f.routes["DELETE "+org+"/services/production/web"] = func(w http.ResponseWriter, r *http.Request, b map[string]any) {
		if b["confirm"] != "web" {
			t.Errorf("confirm: %v", b)
		}
		reply(202, approval())(w, r, b)
	}
	polls := 0
	f.routes["GET "+org+"/jobs/"+job] = func(w http.ResponseWriter, r *http.Request, b map[string]any) {
		polls++
		state, finished := "pending_approval", false
		if polls >= 3 {
			state = "queued"
		}
		if polls >= 5 {
			state, finished = "succeeded", true
		}
		reply(200, map[string]any{"job_id": job, "kind": "app.delete", "state": state, "finished": finished})(w, r, b)
	}
	a, err := c.Do(context.Background(), OpDeleteService, Params{"env": "production", "name": "web"}, map[string]any{"confirm": "web"}, nil)
	if err != nil || a == nil || a.ApproveURL == "" || len(a.JobIDs()) != 1 {
		t.Fatalf("approval %+v %v", a, err)
	}
	if polls != 3 {
		t.Fatalf("polled %d times before the approval", polls)
	}
	if len(logs) == 0 || !strings.Contains(logs[0], a.ApproveURL) || !strings.Contains(logs[0], "dGhlLWRpZ2VzdA") {
		t.Fatalf("the approval page and digest must be shown: %q", logs)
	}
	if err := c.WaitJobs(context.Background(), a.JobIDs()); err != nil {
		t.Fatal(err)
	}
	// Nothing but the API was asked: no approval call, no bypass.
	for _, k := range f.calls {
		if strings.Contains(k, "approve") {
			t.Fatalf("the provider must never approve: %s", k)
		}
	}
}

func TestApprovalNotGivenFailsWithTheApprovalPage(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		timeout     time.Duration
		want        string
	}{
		{"timeout", "pending_approval", 10 * time.Second, "not approved within 10s"},
		{"expired", "expired", time.Minute, "was expired"},
		{"no wait", "pending_approval", 0, "does not wait"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newFake(t)
			c.ApprovalTimeout = tc.timeout
			f.routes["DELETE "+org+"/databases/production/shop"] = reply(202, approval())
			f.routes["GET "+org+"/jobs/"+job] = reply(200, map[string]any{"job_id": job, "state": tc.state})
			_, err := c.Do(context.Background(), OpDropDatabase, Params{"env": "production", "db": "shop"}, map[string]any{"confirm": "shop"}, nil)
			var ae *ApprovalError
			if !errors.As(err, &ae) || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "https://app.unrent.dev/org_aaaaaaaaaaaaaaaa/approvals/"+job) {
				t.Fatalf("%v", err)
			}
		})
	}
}

func TestJobsAndErrors(t *testing.T) {
	f, c := newFake(t)
	f.routes["GET "+org+"/jobs/"+job] = reply(200, map[string]any{"job_id": job, "kind": "db.create", "state": "failed", "finished": true, "result": map[string]any{"error": "disk full"}})
	err := c.WaitJobs(context.Background(), []string{job})
	var je *JobError
	if !errors.As(err, &je) || je.State != "failed" || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("%v", err)
	}
	f.routes["GET "+org+"/services/production/web"] = reply(404, map[string]any{"error": "not found"})
	if err := c.Get(context.Background(), OpGetService, Params{"env": "production", "name": "web"}, nil, nil); !IsNotFound(err) {
		t.Fatalf("404: %v", err)
	}
	f.routes["POST "+org+"/projects"] = reply(403, map[string]any{"error": "this token lacks the manage scope"})
	if _, err := c.Do(context.Background(), OpCreateProject, nil, map[string]any{"name": "x"}, nil); err == nil || !strings.Contains(err.Error(), "403 this token lacks the manage scope") {
		t.Fatalf("%v", err)
	}
	// P21: a conflict's code comes with why (the API's detail), e.g. a sandbox server that runs production.
	f.routes["PUT "+org+"/sandboxes/settings"] = reply(409, map[string]any{"error": "server", "detail": "agt_x: it runs production, a production-tier environment", "servers": []any{}})
	if _, err := c.Do(context.Background(), OpSetSandboxSettings, nil, map[string]any{"servers": []string{"agt_x"}}, nil); err == nil || !strings.Contains(err.Error(), "409 server: agt_x: it runs production") {
		t.Fatalf("%v", err)
	}
	// A job that is not final within job_timeout is an error, not a hang.
	f.routes["GET "+org+"/jobs/"+job] = reply(200, map[string]any{"job_id": job, "kind": "cron.upsert", "state": "delivered", "finished": false})
	if err := c.WaitJobs(context.Background(), []string{job}); err == nil || !strings.Contains(err.Error(), "job_timeout") {
		t.Fatalf("%v", err)
	}
}

func TestRateLimitAndTokenRefresh(t *testing.T) {
	f, c := newFake(t)
	n := 0
	f.routes["GET /v1/me"] = func(w http.ResponseWriter, r *http.Request, b map[string]any) {
		n++
		switch {
		case n == 1:
			w.Header().Set("Retry-After", "2")
			reply(429, map[string]any{"error": "rate limited"})(w, r, b)
		case r.Header.Get("Authorization") != "Bearer unr_ci_new":
			reply(401, map[string]any{"error": "token expired"})(w, r, b)
		default:
			reply(200, map[string]any{"kind": "ci", "org": map[string]any{"id": "org_aaaaaaaaaaaaaaaa"}, "scopes": []string{"read", "manage"}})(w, r, b)
		}
	}
	refreshes := 0
	c.Refresh = func(context.Context) (string, error) { refreshes++; return "unr_ci_new", nil }
	me, err := c.Me(context.Background())
	if err != nil || me.Org.ID != "org_aaaaaaaaaaaaaaaa" || refreshes != 1 || n != 3 {
		t.Fatalf("%+v %v refreshes=%d calls=%d", me, err, refreshes, n)
	}
}

func TestSealOpensOnlyWithTheServersKey(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := Seal(id.Recipient().String(), "s3cr3t-value")
	if err != nil || !strings.HasPrefix(sealed, "-----BEGIN AGE ENCRYPTED FILE-----") || strings.Contains(sealed, "s3cr3t") {
		t.Fatalf("%q %v", sealed, err)
	}
	r, err := age.Decrypt(armor.NewReader(strings.NewReader(sealed)), id)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	_, _ = io.Copy(&b, r)
	if b.String() != "s3cr3t-value" {
		t.Fatalf("opened %q", b.String())
	}
	other, _ := age.GenerateX25519Identity()
	if _, err := age.Decrypt(armor.NewReader(strings.NewReader(sealed)), other); err == nil {
		t.Fatal("another key opened it")
	}
	if _, err := Seal("age1notakey", "x"); err == nil {
		t.Fatal("a malformed recipient must be refused")
	}
}

// The operation table is generated from the API's OpenAPI document: stale or hand-edited fails here.
func TestOperationsMatchTheOpenAPIDocument(t *testing.T) {
	doc, err := os.ReadFile("../../../apps/control/openapi/v1.json")
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("not in the Unrent monorepo (the published provider repository has no OpenAPI document)")
	}
	if err != nil {
		t.Fatal(err)
	}
	want, err := opsgen.Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("ops_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("ops_gen.go is stale: go run ./internal/client/gen ../apps/control/openapi/v1.json > internal/client/ops_gen.go")
	}
	for _, op := range AllOperations {
		if op.Method == http.MethodDelete || op.Method == http.MethodPut || op.Method == http.MethodPatch || op.Method == http.MethodPost {
			// Reading logs, and WP2.14's routing test and unrent.toml checks, are POSTs that change nothing.
			readOnly := strings.Contains(op.ID, "Logs") || op.ID == "routingTest" || op.ID == "diffConfigFile" || op.ID == "validateConfigFile"
			if op.Scope == "read" && !readOnly {
				t.Errorf("%s changes something with the read scope", op.ID)
			}
		}
	}
}
