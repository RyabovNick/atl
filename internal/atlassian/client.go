package atlassian

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RyabovNick/atl/internal/cli"
)

// Client sends authenticated read-only requests to one service, jira or confluence.
type Client struct {
	Name   string // the service, as in config sections and messages
	Base   string // base URL without a trailing slash
	header http.Header
	user   string
	pass   string
	http   *http.Client
}

// NewClient builds a client for the jira or confluence settings.
func NewClient(name string) (*Client, error) {
	s, err := Settings(name)
	if err != nil {
		return nil, err
	}
	p := EnvPrefix(name)
	switch {
	case s["url"] == "" && s["token"] == "":
		return nil, cli.Fail(cli.ExitConfig, "%s needs a URL and token: set %sURL and %sAPI_TOKEN, or url and token under [%s] in %s",
			name, p, p, name, ConfigPath())
	case s["url"] == "":
		return nil, cli.Fail(cli.ExitConfig, "%s needs a URL: set %sURL, or url under [%s] in %s", name, p, name, ConfigPath())
	case s["token"] == "":
		return nil, cli.Fail(cli.ExitConfig, "%s needs a token: set %sAPI_TOKEN, or token under [%s] in %s", name, p, name, ConfigPath())
	}
	c := &Client{Name: name, Base: strings.TrimRight(s["url"], "/"), header: http.Header{"Accept": {"application/json"}}}
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
			return nil, cli.Fail(cli.ExitConfig, "%s: basic auth needs a username (%sUSERNAME or username under [%s])", name, EnvPrefix(name), name)
		}
		c.user, c.pass = s["username"], s["token"]
	default:
		return nil, cli.Fail(cli.ExitConfig, "[%s] auth must be bearer or basic, not %q", name, mode)
	}

	timeout := 30 * time.Second
	if t := s["timeout"]; t != "" {
		secs, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return nil, cli.Fail(cli.ExitConfig, "[%s] timeout must be seconds, not %q", name, t)
		}
		timeout = time.Duration(secs * float64(time.Second))
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if bundle := s["ca_bundle"]; bundle != "" {
		pem, err := os.ReadFile(bundle)
		if err != nil {
			return nil, cli.Fail(cli.ExitConfig, "[%s] ca_bundle: %v", name, err)
		}
		pool, _ := x509.SystemCertPool()
		if pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, cli.Fail(cli.ExitConfig, "[%s] ca_bundle %s holds no PEM certificates", name, bundle)
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

// Raw sends a GET and returns the response with its body read; redirects are not followed.
func (c *Client) Raw(path string, params url.Values) (*http.Response, []byte, error) {
	u := c.Base + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, nil, cli.Fail(cli.ExitUsage, "%s: %v", c.Name, err)
	}
	req.Header = c.header.Clone()
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, cli.Fail(cli.ExitAPI, "%s: %v", c.Name, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, cli.Fail(cli.ExitAPI, "%s: reading %s: %v", c.Name, path, err)
	}
	return resp, body, nil
}

// Get fetches a JSON resource, decodes it into out when non-nil, and returns the raw body.
func (c *Client) Get(path string, params url.Values, out any) ([]byte, error) {
	resp, body, err := c.Raw(path, params)
	if err != nil {
		return nil, err
	}
	switch code := resp.StatusCode; {
	case code == 401 || code == 403:
		return nil, cli.Fail(cli.ExitAPI, "%s: HTTP %d on %s; check the token and auth mode in %s", c.Name, code, path, ConfigPath())
	case code == 404:
		// Confluence serves a rejected token as an anonymous user, who cannot see restricted pages.
		if c.Name == "confluence" {
			return nil, cli.Fail(cli.ExitNotFound, "%s: not found: %s (a rejected token looks the same; atl doctor tells them apart)", c.Name, path)
		}
		return nil, cli.Fail(cli.ExitNotFound, "%s: not found: %s", c.Name, path)
	case code >= 300 && code < 400:
		return nil, cli.Fail(cli.ExitAPI, "%s: %s redirected to %s; the token was probably not accepted", c.Name, path, resp.Header.Get("Location"))
	case code == 400:
		if msg := badRequestMessage(body); msg != "" {
			return nil, cli.Fail(cli.ExitUsage, "%s: rejected the request: %s", c.Name, msg)
		}
		return nil, cli.Fail(cli.ExitUsage, "%s: HTTP 400 on %s: %s", c.Name, path, cli.Truncate(string(body), 300))
	case code >= 400:
		return nil, cli.Fail(cli.ExitAPI, "%s: HTTP %d on %s: %s", c.Name, code, path, cli.Truncate(string(body), 300))
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return nil, cli.Fail(cli.ExitAPI, "%s: %s did not return JSON; an SSO page usually means the token was not accepted", c.Name, path)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return nil, cli.Fail(cli.ExitAPI, "%s: decoding %s: %v", c.Name, path, err)
		}
	}
	return body, nil
}

func (c *Client) sameHost(u *url.URL) bool {
	base, err := url.Parse(c.Base)
	return err == nil && u.Scheme == base.Scheme && strings.EqualFold(u.Host, base.Host)
}

// Download streams ref (absolute, or relative to the base URL) into dst, at most maxBytes.
// The token is only ever sent to the configured host: absolute URLs and redirects
// elsewhere are refused rather than followed.
func (c *Client) Download(ref string, dst io.Writer, maxBytes int64) (int64, error) {
	target := ref
	if strings.HasPrefix(ref, "/") {
		target = c.Base + ref
	}
	u, err := url.Parse(target)
	if err != nil || !u.IsAbs() {
		return 0, cli.Fail(cli.ExitAPI, "%s: bad download link %q", c.Name, ref)
	}
	if !c.sameHost(u) {
		return 0, cli.Fail(cli.ExitAPI, "%s: refusing to send the token to %s://%s, which is not %s", c.Name, u.Scheme, u.Host, c.Base)
	}
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, cli.Fail(cli.ExitAPI, "%s: %v", c.Name, err)
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
		return 0, cli.Fail(cli.ExitAPI, "%s: %v", c.Name, err)
	}
	defer resp.Body.Close()
	// Followed redirects can end on the login page, which Data Center serves with a 200.
	if strings.Contains(strings.ToLower(resp.Request.URL.Path), "login") {
		return 0, cli.Fail(cli.ExitAPI, "%s: download redirected to the login page; the token was probably not accepted", c.Name)
	}
	switch code := resp.StatusCode; {
	case code == 401 || code == 403:
		return 0, cli.Fail(cli.ExitAPI, "%s: HTTP %d downloading %s; check the token", c.Name, code, u.Path)
	case code == 404:
		return 0, cli.Fail(cli.ExitNotFound, "%s: not found: %s", c.Name, u.Path)
	case code >= 300:
		return 0, cli.Fail(cli.ExitAPI, "%s: HTTP %d downloading %s", c.Name, code, u.Path)
	}
	if resp.ContentLength > maxBytes {
		return 0, cli.Fail(cli.ExitAPI, "%s: %s is %d bytes, over the %d-byte limit; raise --max-bytes", c.Name, u.Path, resp.ContentLength, maxBytes)
	}
	n, err := io.Copy(dst, io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return n, cli.Fail(cli.ExitAPI, "%s: downloading %s: %v", c.Name, u.Path, err)
	}
	if n > maxBytes {
		return n, cli.Fail(cli.ExitAPI, "%s: %s is over the %d-byte limit; raise --max-bytes", c.Name, u.Path, maxBytes)
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

// ShortDate formats an API timestamp as "2006-01-02 15:04".
func ShortDate(s string) string {
	if s == "" {
		return "?"
	}
	return strings.Replace(cli.Truncate(s, 16), "T", " ", 1)
}

// Named is any API object with a name and display name: users, statuses, types.
type Named struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// NameOf returns the display name, falling back to the name; nil gives "".
func NameOf(n *Named) string {
	if n == nil {
		return ""
	}
	if n.DisplayName != "" {
		return n.DisplayName
	}
	return n.Name
}
