// Package atlassian reads the atl configuration and talks to Jira and
// Confluence Data Center over their REST APIs.
package atlassian

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/RyabovNick/atl/internal/cli"
)

// ConfigPath is $ATL_CONFIG, or ~/.config/atl/config.ini.
func ConfigPath() string {
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

// LoadConfig reads the optional config file; a missing file is an empty config.
func LoadConfig() (map[string]map[string]string, error) {
	path := ConfigPath()
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]map[string]string{}, nil
	}
	if err != nil {
		return nil, cli.Fail(cli.ExitConfig, "%v", err)
	}
	// The file holds tokens; refusing a readable one is cheaper than leaking it.
	if info.Mode().Perm()&0o077 != 0 {
		return nil, cli.Fail(cli.ExitConfig, "%s is accessible to other users; chmod 600 it", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, cli.Fail(cli.ExitConfig, "%v", err)
	}
	cfg, err := parseINI(string(data))
	if err != nil {
		return nil, cli.Fail(cli.ExitConfig, "%s: %v", path, err)
	}
	return cfg, nil
}

// EnvPrefix maps a config section to its variables: jira -> JIRA_URL, JIRA_API_TOKEN, JIRA_USERNAME.
func EnvPrefix(name string) string { return strings.ToUpper(name) + "_" }

// CredentialVars names the variables a user has to set for a service.
func CredentialVars(name string) string {
	p := EnvPrefix(name)
	return p + "URL and " + p + "API_TOKEN"
}

// Settings is a service's config section with its environment variables on top.
func Settings(name string) (map[string]string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	s := map[string]string{}
	maps.Copy(s, cfg[name])
	p := EnvPrefix(name)
	for key, env := range map[string]string{"url": p + "URL", "token": p + "API_TOKEN", "username": p + "USERNAME"} {
		if v := os.Getenv(env); v != "" {
			s[key] = v
		}
	}
	return s, nil
}

// OptionalBaseURL lets `issue` recognize Confluence links without requiring Confluence credentials.
func OptionalBaseURL(name string) string {
	s, err := Settings(name)
	if err != nil {
		return ""
	}
	return strings.TrimRight(s["url"], "/")
}
