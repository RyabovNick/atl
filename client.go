package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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

// loadConfig reads the optional config file; a missing file is an empty config.
func loadConfig() (map[string]map[string]string, error) {
	path := configPath()
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]map[string]string{}, nil
	}
	if err != nil {
		return nil, fail(exitConfig, "%v", err)
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

// envPrefix maps a config section to its variables: jira -> JIRA_URL, JIRA_API_TOKEN, JIRA_USERNAME.
func envPrefix(name string) string { return strings.ToUpper(name) + "_" }

// settings is a service's config section with its environment variables on top,
// and the built-in default URL underneath.
func settings(name string) (map[string]string, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	s := map[string]string{}
	for k, v := range cfg[name] {
		s[k] = v
	}
	p := envPrefix(name)
	for key, env := range map[string]string{"url": p + "URL", "token": p + "API_TOKEN", "username": p + "USERNAME"} {
		if v := os.Getenv(env); v != "" {
			s[key] = v
		}
	}
	if s["url"] == "" {
		s["url"] = defaultURL(name)
	}
	return s, nil
}

// optionalBaseURL lets `issue` recognise Confluence links without requiring Confluence credentials.
func optionalBaseURL(name string) string {
	s, err := settings(name)
	if err != nil {
		return ""
	}
	return strings.TrimRight(s["url"], "/")
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
	s, err := settings(name)
	if err != nil {
		return nil, err
	}
	p := envPrefix(name)
	switch {
	case s["url"] == "" && s["token"] == "":
		return nil, fail(exitConfig, "%s needs a URL and token: set %sURL and %sAPI_TOKEN, or url and token under [%s] in %s",
			name, p, p, name, configPath())
	case s["url"] == "":
		return nil, fail(exitConfig, "%s needs a URL: set %sURL, or url under [%s] in %s", name, p, name, configPath())
	case s["token"] == "":
		return nil, fail(exitConfig, "%s needs a token: set %sAPI_TOKEN, or token under [%s] in %s", name, p, name, configPath())
	}
	c := &client{name: name, base: strings.TrimRight(s["url"], "/"), header: http.Header{"Accept": {"application/json"}}}
	mode := s["auth"]
	if mode == "" {
		mode = "bearer"
		if s["username"] != "" {
			mode = "basic"
		}
	}
	switch mode {
	case "bearer":
		c.header.Set("Authorization", "Bearer "+s["token"])
	case "basic":
		if s["username"] == "" {
			return nil, fail(exitConfig, "%s: basic auth needs a username (%sUSERNAME or username under [%s])", name, envPrefix(name), name)
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
		// Confluence serves a rejected token as an anonymous user, who cannot see restricted pages.
		if c.name == "confluence" {
			return nil, fail(exitNotFound, "%s: not found: %s (a rejected token looks the same; atl doctor tells them apart)", c.name, path)
		}
		return nil, fail(exitNotFound, "%s: not found: %s", c.name, path)
	case code >= 300 && code < 400:
		return nil, fail(exitAPI, "%s: %s redirected to %s; the token was probably not accepted", c.name, path, resp.Header.Get("Location"))
	case code == 400:
		if msg := badRequestMessage(body); msg != "" {
			return nil, fail(exitUsage, "%s: rejected the request: %s", c.name, msg)
		}
		return nil, fail(exitUsage, "%s: HTTP 400 on %s: %s", c.name, path, truncate(string(body), 300))
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

func (c *client) sameHost(u *url.URL) bool {
	base, err := url.Parse(c.base)
	return err == nil && u.Scheme == base.Scheme && strings.EqualFold(u.Host, base.Host)
}

// download streams ref (absolute, or relative to the base URL) into dst, at most maxBytes.
// The token is only ever sent to the configured host: absolute URLs and redirects
// elsewhere are refused rather than followed.
func (c *client) download(ref string, dst io.Writer, maxBytes int64) (int64, error) {
	target := ref
	if strings.HasPrefix(ref, "/") {
		target = c.base + ref
	}
	u, err := url.Parse(target)
	if err != nil || !u.IsAbs() {
		return 0, fail(exitAPI, "%s: bad download link %q", c.name, ref)
	}
	if !c.sameHost(u) {
		return 0, fail(exitAPI, "%s: refusing to send the token to %s://%s, which is not %s", c.name, u.Scheme, u.Host, c.base)
	}
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, fail(exitAPI, "%s: %v", c.name, err)
	}
	req.Header = c.header.Clone()
	req.Header.Set("Accept", "*/*")
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	hc := &http.Client{
		Transport: c.http.Transport,
		Timeout:   10 * c.http.Timeout,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if !c.sameHost(next.URL) {
				return fmt.Errorf("refusing redirect to another host, %s", next.URL.Host)
			}
			return nil
		},
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, fail(exitAPI, "%s: %v", c.name, err)
	}
	defer resp.Body.Close()
	// Followed redirects can end on the login page, which Data Center serves with a 200.
	if strings.Contains(strings.ToLower(resp.Request.URL.Path), "login") {
		return 0, fail(exitAPI, "%s: download redirected to the login page; the token was probably not accepted", c.name)
	}
	switch code := resp.StatusCode; {
	case code == 401 || code == 403:
		return 0, fail(exitAPI, "%s: HTTP %d downloading %s; check the token", c.name, code, u.Path)
	case code == 404:
		return 0, fail(exitNotFound, "%s: not found: %s", c.name, u.Path)
	case code >= 300:
		return 0, fail(exitAPI, "%s: HTTP %d downloading %s", c.name, code, u.Path)
	}
	if resp.ContentLength > maxBytes {
		return 0, fail(exitAPI, "%s: %s is %d bytes, over the %d-byte limit; raise --max-bytes", c.name, u.Path, resp.ContentLength, maxBytes)
	}
	n, err := io.Copy(dst, io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return n, fail(exitAPI, "%s: downloading %s: %v", c.name, u.Path, err)
	}
	if n > maxBytes {
		return n, fail(exitAPI, "%s: %s is over the %d-byte limit; raise --max-bytes", c.name, u.Path, maxBytes)
	}
	return n, nil
}

// badRequestMessage extracts the reason from a 400 body: Jira sends errorMessages
// and per-field errors, Confluence a single message. Bad JQL and CQL land here.
func badRequestMessage(body []byte) string {
	var e struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
		Message       string            `json:"message"`
	}
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	msgs := e.ErrorMessages
	keys := make([]string, 0, len(e.Errors))
	for k := range e.Errors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		msgs = append(msgs, k+": "+e.Errors[k])
	}
	if e.Message != "" {
		msgs = append(msgs, e.Message)
	}
	return strings.Join(msgs, "; ")
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
