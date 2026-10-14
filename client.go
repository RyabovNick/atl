package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	exitAPI      = 1
	exitUsage    = 2
	exitConfig   = 3
	exitNotFound = 4
)

type atlError struct {
	msg  string
	code int
}

func (e *atlError) Error() string { return e.msg }

func fail(code int, format string, args ...any) error {
	return &atlError{msg: fmt.Sprintf(format, args...), code: code}
}

func configPath() string {
	if p := os.Getenv("ATL_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "atl", "config.ini")
}

func parseINI(data string) (map[string]map[string]string, error) {
	cfg := map[string]map[string]string{}
	var cur map[string]string
	for i, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			cur = map[string]string{}
			cfg[strings.TrimSpace(line[1:len(line)-1])] = cur
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || cur == nil {
			return nil, fmt.Errorf("line %d: expected key = value inside a [section]", i+1)
		}
		cur[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return cfg, nil
}

func loadConfig() (map[string]map[string]string, error) {
	path := configPath()
	info, err := os.Stat(path)
	if err != nil {
		return nil, fail(exitConfig, "no config at %s; start from config.example.ini", path)
	}
	// The file holds tokens; refusing a readable one is cheaper than leaking it.
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fail(exitConfig, "%s is accessible to other users; chmod 600 it", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fail(exitConfig, "%v", err)
	}
	cfg, err := parseINI(string(data))
	if err != nil {
		return nil, fail(exitConfig, "%s: %v", path, err)
	}
	return cfg, nil
}

// optionalBaseURL lets `issue` recognise Confluence links without requiring Confluence credentials.
func optionalBaseURL(name string) string {
	cfg, err := loadConfig()
	if err != nil {
		return ""
	}
	return strings.TrimRight(cfg[name]["url"], "/")
}

type client struct {
	name   string
	base   string
	header http.Header
	user   string
	pass   string
	http   *http.Client
}

func newClient(name string) (*client, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	s := cfg[name]
	if s["url"] == "" || s["token"] == "" {
		return nil, fail(exitConfig, "[%s] needs url and token in %s", name, configPath())
	}
	c := &client{name: name, base: strings.TrimRight(s["url"], "/"), header: http.Header{"Accept": {"application/json"}}}
	switch mode := s["auth"]; mode {
	case "", "bearer":
		c.header.Set("Authorization", "Bearer "+s["token"])
	case "basic":
		if s["username"] == "" {
			return nil, fail(exitConfig, "[%s] auth = basic needs username", name)
		}
		c.user, c.pass = s["username"], s["token"]
	default:
		return nil, fail(exitConfig, "[%s] auth must be bearer or basic, not %q", name, mode)
	}

	timeout := 30 * time.Second
	if t := s["timeout"]; t != "" {
		secs, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return nil, fail(exitConfig, "[%s] timeout must be seconds, not %q", name, t)
		}
		timeout = time.Duration(secs * float64(time.Second))
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if bundle := s["ca_bundle"]; bundle != "" {
		pem, err := os.ReadFile(bundle)
		if err != nil {
			return nil, fail(exitConfig, "[%s] ca_bundle: %v", name, err)
		}
		pool, _ := x509.SystemCertPool()
		if pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fail(exitConfig, "[%s] ca_bundle %s holds no PEM certificates", name, bundle)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool}
	}
	c.http = &http.Client{
		Timeout:   timeout,
		Transport: transport,
		// Redirects are reported, not followed: a rejected token redirects to the login page.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return c, nil
}

func (c *client) raw(path string, params url.Values) (*http.Response, []byte, error) {
	u := c.base + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, fail(exitUsage, "%s: %v", c.name, err)
	}
	req.Header = c.header.Clone()
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fail(exitAPI, "%s: %v", c.name, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fail(exitAPI, "%s: reading %s: %v", c.name, path, err)
	}
	return resp, body, nil
}

// get fetches a JSON resource, decodes it into out when non-nil, and returns the raw body.
func (c *client) get(path string, params url.Values, out any) ([]byte, error) {
	resp, body, err := c.raw(path, params)
	if err != nil {
		return nil, err
	}
	switch code := resp.StatusCode; {
	case code == 401 || code == 403:
		return nil, fail(exitAPI, "%s: HTTP %d on %s; check the token and auth mode in %s", c.name, code, path, configPath())
	case code == 404:
		return nil, fail(exitNotFound, "%s: not found: %s", c.name, path)
	case code >= 300 && code < 400:
		return nil, fail(exitAPI, "%s: %s redirected to %s; the token was probably not accepted", c.name, path, resp.Header.Get("Location"))
	case code >= 400:
		return nil, fail(exitAPI, "%s: HTTP %d on %s: %s", c.name, code, path, truncate(string(body), 300))
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return nil, fail(exitAPI, "%s: %s did not return JSON; an SSO page usually means the token was not accepted", c.name, path)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return nil, fail(exitAPI, "%s: decoding %s: %v", c.name, path, err)
		}
	}
	return body, nil
}

func writeJSON(w io.Writer, raw []byte) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return fail(exitAPI, "indenting JSON: %v", err)
	}
	fmt.Fprintln(w, buf.String())
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func shortDate(s string) string {
	if s == "" {
		return "?"
	}
	return strings.Replace(truncate(s, 16), "T", " ", 1)
}
