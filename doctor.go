package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

type setting struct {
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Source string `json:"source"`
}

type fileStatus struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Mode   string `json:"mode,omitempty"`
	Error  string `json:"error,omitempty"`
}

type doctorUser struct {
	DisplayName string `json:"displayName,omitempty"`
	Name        string `json:"name,omitempty"`
	Email       string `json:"email,omitempty"`
}

type doctorServer struct {
	Version        string `json:"version,omitempty"`
	DeploymentType string `json:"deploymentType,omitempty"`
	BaseURL        string `json:"baseUrl,omitempty"`
}

type serviceReport struct {
	Name       string        `json:"name"`
	Configured bool          `json:"configured"`
	Settings   []setting     `json:"settings"`
	OK         bool          `json:"ok"`
	Error      string        `json:"error,omitempty"`
	User       *doctorUser   `json:"user,omitempty"`
	Server     *doctorServer `json:"server,omitempty"`
	Warnings   []string      `json:"warnings,omitempty"`

	// reached is false when the check stopped at configuration, before any request.
	reached bool
}

func inspectConfigFile() (fileStatus, map[string]map[string]string) {
	st := fileStatus{Path: configPath()}
	info, err := os.Stat(st.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	st.Exists = true
	if err == nil {
		st.Mode = fmt.Sprintf("%o", info.Mode().Perm())
	}
	cfg, err := loadConfig()
	if err != nil {
		st.Error = err.Error()
		return st, nil
	}
	return st, cfg
}

// describeSettings reports each effective value and where it came from. The
// token's value is never included, only whether it is set.
func describeSettings(name string, file map[string]string) []setting {
	p := envPrefix(name)
	envs := map[string]string{"url": p + "URL", "token": p + "API_TOKEN", "username": p + "USERNAME"}
	var out []setting
	for _, key := range []string{"url", "token", "username", "auth", "ca_bundle", "timeout"} {
		s := setting{Key: key, Source: "unset"}
		if env := envs[key]; env != "" && os.Getenv(env) != "" {
			s.Value, s.Source = os.Getenv(env), env
		} else if file[key] != "" {
			s.Value, s.Source = file[key], "config file"
		}
		out = append(out, s)
	}
	for i := range out {
		s := &out[i]
		switch {
		case s.Key == "token" && s.Source != "unset":
			s.Value = "set"
		case s.Key == "auth" && s.Source == "unset":
			s.Value, s.Source = "bearer", "default"
			if out[2].Source != "unset" {
				s.Value = "basic"
				s.Source = "default, because a username is set"
			}
		case s.Key == "timeout" && s.Source == "unset":
			s.Value, s.Source = "30", "default"
		}
	}
	return out
}

func checkJira(c *client, r *serviceReport) {
	var me struct {
		Name         string `json:"name"`
		Key          string `json:"key"`
		DisplayName  string `json:"displayName"`
		EmailAddress string `json:"emailAddress"`
	}
	if _, err := c.get("/rest/api/2/myself", nil, &me); err != nil {
		r.Error = err.Error()
		return
	}
	r.OK = true
	r.User = &doctorUser{DisplayName: me.DisplayName, Name: or(me.Name, me.Key), Email: me.EmailAddress}

	var info doctorServer
	if _, err := c.get("/rest/api/2/serverInfo", nil, &info); err != nil {
		r.Warnings = append(r.Warnings, "server info unavailable: "+err.Error())
		return
	}
	r.Server = &info
	if info.BaseURL != "" && !strings.EqualFold(strings.TrimRight(info.BaseURL, "/"), c.base) {
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"Jira's base URL is %s but atl is configured with %s; links and attachment downloads are matched against the configured URL",
			info.BaseURL, c.base))
	}
}

func checkConfluence(c *client, r *serviceReport) {
	var u struct {
		Type        string `json:"type"`
		Username    string `json:"username"`
		UserKey     string `json:"userKey"`
		DisplayName string `json:"displayName"`
	}
	if _, err := c.get("/rest/api/user/current", nil, &u); err != nil {
		r.Error = err.Error()
		return
	}
	// Confluence answers an unauthenticated request with a 200 and an anonymous user.
	if u.Type == "anonymous" {
		r.Error = "confluence: answered as an anonymous user, so the token was not accepted; check the token and auth mode"
		return
	}
	r.OK = true
	r.User = &doctorUser{DisplayName: u.DisplayName, Name: or(u.Username, u.UserKey)}
}

func diagnose(name string, file map[string]string, fileErr string) serviceReport {
	r := serviceReport{Name: name, Settings: describeSettings(name, file)}
	for _, s := range r.Settings {
		if (s.Key == "url" || s.Key == "token") && s.Source != "unset" {
			r.Configured = true
		}
	}
	if fileErr != "" {
		r.Error = fileErr
		return r
	}
	if !r.Configured {
		return r
	}
	c, err := newClient(name)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	r.reached = true
	if name == "jira" {
		checkJira(c, &r)
	} else {
		checkConfluence(c, &r)
	}
	return r
}

func (r serviceReport) summary() string {
	switch {
	case r.OK:
		who := or(r.User.DisplayName, r.User.Name)
		var ids []string
		if r.User.Name != "" && r.User.Name != who {
			ids = append(ids, r.User.Name)
		}
		if r.User.Email != "" {
			ids = append(ids, r.User.Email)
		}
		if len(ids) > 0 {
			who += " (" + strings.Join(ids, ", ") + ")"
		}
		line := fmt.Sprintf("✓ %s: authenticated as %s", r.Name, who)
		if r.Server != nil && r.Server.Version != "" {
			line += " — Jira " + strings.TrimSpace(r.Server.Version+" "+r.Server.DeploymentType)
		}
		return line
	case r.Error != "":
		return fmt.Sprintf("✗ %s: %s", r.Name, strings.TrimPrefix(r.Error, r.Name+": "))
	default:
		return fmt.Sprintf("- %s: not configured", r.Name)
	}
}

func cmdDoctor(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("doctor", "", "Check atl's configuration and that each configured service accepts its token.\nTokens are never printed.", stderr)
	asJSON := fs.Bool("json", false, "print the report as JSON")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fail(exitUsage, "doctor takes no arguments")
	}

	file, cfg := inspectConfigFile()
	reports := []serviceReport{
		diagnose("jira", cfg["jira"], file.Error),
		diagnose("confluence", cfg["confluence"], file.Error),
	}

	reached, failed := false, false
	for _, r := range reports {
		reached = reached || r.reached
		failed = failed || (r.Error != "")
	}
	code := 0
	switch {
	case !reached:
		code = exitConfig
	case failed:
		code = exitAPI
	}

	if *asJSON {
		data, _ := json.Marshal(map[string]any{"config_file": file, "services": reports, "exit": code})
		if err := writeJSON(stdout, data); err != nil {
			return err
		}
	} else {
		writeDoctor(stdout, file, reports, code)
	}
	if code != 0 {
		return &atlError{code: code}
	}
	return nil
}

func writeDoctor(w io.Writer, file fileStatus, reports []serviceReport, code int) {
	var b strings.Builder
	switch {
	case file.Error != "":
		fmt.Fprintf(&b, "Config file: %s (%s)\n", file.Path, file.Error)
	case file.Exists:
		fmt.Fprintf(&b, "Config file: %s (mode %s)\n", file.Path, file.Mode)
	default:
		fmt.Fprintf(&b, "Config file: %s (not present; environment variables only)\n", file.Path)
	}
	for _, r := range reports {
		fmt.Fprintf(&b, "\n%s\n", r.Name)
		for _, s := range r.Settings {
			value := or(s.Value, "-")
			if s.Source == "unset" {
				fmt.Fprintf(&b, "  %-10s %s\n", s.Key, "unset")
				continue
			}
			fmt.Fprintf(&b, "  %-10s %s (%s)\n", s.Key, value, s.Source)
		}
		fmt.Fprintf(&b, "%s\n", r.summary())
		for _, warn := range r.Warnings {
			fmt.Fprintf(&b, "  ! %s\n", warn)
		}
	}
	if code == exitConfig && file.Error == "" && !reports[0].Configured && !reports[1].Configured {
		fmt.Fprintf(&b, "\nNothing is configured: set %s and/or %s, or create %s.\n",
			credentialVars("jira"), credentialVars("confluence"), file.Path)
	}
	io.WriteString(w, b.String())
}
