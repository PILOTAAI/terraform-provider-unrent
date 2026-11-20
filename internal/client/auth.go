package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Discovery is what the API's metadata says about its authorization server (RFC 9728, RFC 8414).
type Discovery struct {
	Resource string
	Issuer   string
	Token    string
}

func getJSON(ctx context.Context, h *http.Client, u string, hdr http.Header, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	req.Header.Set("Accept", "application/json")
	res, err := h.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %d", u, res.StatusCode)
	}
	return json.Unmarshal(b, out)
}

// Discover reads the API's protected resource metadata and its authorization server's metadata.
func Discover(ctx context.Context, h *http.Client, api string) (*Discovery, error) {
	api = strings.TrimRight(api, "/")
	var prm struct {
		Resource string   `json:"resource"`
		AS       []string `json:"authorization_servers"`
	}
	if err := getJSON(ctx, h, api+"/.well-known/oauth-protected-resource/v1", nil, &prm); err != nil {
		return nil, fmt.Errorf("%s does not look like the Unrent API: %w", api, err)
	}
	if prm.Resource != api+"/v1" || len(prm.AS) == 0 {
		return nil, fmt.Errorf("%s: unexpected resource metadata", api)
	}
	var as struct {
		Issuer string `json:"issuer"`
		Token  string `json:"token_endpoint"`
	}
	if err := getJSON(ctx, h, strings.TrimRight(prm.AS[0], "/")+"/.well-known/oauth-authorization-server", nil, &as); err != nil {
		return nil, fmt.Errorf("authorization server: %w", err)
	}
	if as.Issuer != prm.AS[0] || as.Token == "" {
		return nil, errors.New("authorization server metadata does not match its issuer")
	}
	return &Discovery{Resource: prm.Resource, Issuer: as.Issuer, Token: as.Token}, nil
}

// GitHubActions exchanges the running job's GitHub OIDC token (audience: the API) for a ten-minute CI token bound
// to one environment (RFC 8693, docs/api.md §4). getenv reads ACTIONS_ID_TOKEN_REQUEST_URL and _TOKEN, which the
// runner sets for jobs with `permissions: id-token: write`.
func GitHubActions(ctx context.Context, h *http.Client, api, org, env string, getenv func(string) string) (string, time.Duration, error) {
	reqURL, reqTok := getenv("ACTIONS_ID_TOKEN_REQUEST_URL"), getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if reqURL == "" || reqTok == "" {
		return "", 0, errors.New("no GitHub Actions OIDC token: the job needs `permissions: id-token: write` (ACTIONS_ID_TOKEN_REQUEST_URL is not set)")
	}
	d, err := Discover(ctx, h, api)
	if err != nil {
		return "", 0, err
	}
	sep := "&"
	if !strings.Contains(reqURL, "?") {
		sep = "?"
	}
	var idt struct {
		Value string `json:"value"`
	}
	if err := getJSON(ctx, h, reqURL+sep+"audience="+url.QueryEscape(d.Resource), http.Header{"Authorization": {"Bearer " + reqTok}}, &idt); err != nil {
		return "", 0, fmt.Errorf("GitHub OIDC token: %w", err)
	}
	if idt.Value == "" {
		return "", 0, errors.New("GitHub answered without an OIDC token")
	}
	f := url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {idt.Value},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:id_token"},
		"resource":           {d.Resource},
		"org":                {org},
		"environment":        {env},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Token, strings.NewReader(f.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := h.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer res.Body.Close()
	var t struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&t); err != nil {
		return "", 0, fmt.Errorf("token exchange: %w", err)
	}
	if res.StatusCode != http.StatusOK || t.AccessToken == "" {
		return "", 0, fmt.Errorf("token exchange refused: %s %s", t.Error, t.Description)
	}
	return t.AccessToken, time.Duration(t.ExpiresIn) * time.Second, nil
}

// CLICredentials is the token `unrent login` keeps (docs/cli.md), when it is for this API and still valid.
func CLICredentials(api string, getenv func(string) string, now time.Time) (token, org string, err error) {
	dir := getenv("XDG_CONFIG_HOME")
	if dir != "" {
		dir = filepath.Join(dir, "unrent")
	} else if h, e := os.UserHomeDir(); e == nil {
		dir = filepath.Join(h, ".config", "unrent")
	} else {
		return "", "", e
	}
	b, err := os.ReadFile(filepath.Join(dir, "credentials.json"))
	if err != nil {
		return "", "", err
	}
	var c struct {
		API         string `json:"api"`
		AccessToken string `json:"access_token"`
		ExpiresAt   int64  `json:"expires_at"`
		Org         string `json:"org"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return "", "", err
	}
	if strings.TrimRight(c.API, "/") != strings.TrimRight(api, "/") {
		return "", "", fmt.Errorf("unrent login is signed in to %s, not %s", c.API, api)
	}
	if c.AccessToken == "" || (c.ExpiresAt > 0 && now.Unix() >= c.ExpiresAt) {
		return "", "", errors.New("the unrent login token expired: run `unrent login` again or set UNRENT_TOKEN")
	}
	return c.AccessToken, c.Org, nil
}
