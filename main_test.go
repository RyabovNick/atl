package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RyabovNick/atl/internal/cli"
	"github.com/RyabovNick/atl/internal/testenv"
)

func runAtl(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return out.String(), errb.String(), code
}

func TestRunUsage(t *testing.T) {
	if _, errs, code := runAtl(t); code != cli.ExitUsage || !strings.Contains(errs, "atl issue") {
		t.Errorf("no args: exit %d, %s", code, errs)
	}
	if out, _, code := runAtl(t, "--help"); code != 0 || !strings.Contains(out, "Exit codes") {
		t.Errorf("--help: exit %d, %s", code, out)
	}
	if _, errs, code := runAtl(t, "frob"); code != cli.ExitUsage || !strings.Contains(errs, `unknown command "frob"`) {
		t.Errorf("unknown: exit %d, %s", code, errs)
	}
	if _, errs, code := runAtl(t, "issue", "-h"); code != 0 || !strings.Contains(errs, "usage: atl issue") {
		t.Errorf("issue -h: exit %d, %s", code, errs)
	}
	for _, args := range [][]string{{"issue", "A-1", "B-2"}, {"page"}, {"search"}} {
		if _, _, code := runAtl(t, args...); code != cli.ExitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func TestErrors(t *testing.T) {
	srv := testenv.FakeServer(t)
	cfg := fmt.Sprintf("[jira]\nurl = %s/jira\ntoken = wrong\n", srv.URL)

	testenv.WriteConfig(t, cfg, 0o644)
	if _, errs, code := runAtl(t, "issue", "PROJ-123"); code != cli.ExitConfig || !strings.Contains(errs, "chmod 600") {
		t.Errorf("readable config: exit %d, %s", code, errs)
	}
	testenv.WriteConfig(t, cfg, 0o600)
	if _, errs, code := runAtl(t, "issue", "PROJ-123"); code != cli.ExitAPI || !strings.Contains(errs, "redirected") {
		t.Errorf("bad token: exit %d, %s", code, errs)
	}
	testenv.WriteConfig(t, fmt.Sprintf("[jira]\nurl = %s/jira\ntoken = %s\n", srv.URL, testenv.Token), 0o600)
	if _, _, code := runAtl(t, "issue", "PROJ-999"); code != cli.ExitNotFound {
		t.Errorf("missing issue: exit %d", code)
	}
	if _, errs, code := runAtl(t, "page", "42"); code != cli.ExitConfig || !strings.Contains(errs, "[confluence]") {
		t.Errorf("missing confluence section: exit %d, %s", code, errs)
	}
	if _, _, code := runAtl(t, "issue", "--nope"); code != cli.ExitUsage {
		t.Errorf("bad flag: exit %d", code)
	}
}

func TestEnvOnly(t *testing.T) {
	srv := testenv.FakeServer(t)
	testenv.ClearEnv(t)
	t.Setenv("ATL_CONFIG", filepath.Join(t.TempDir(), "absent.ini"))
	t.Setenv("JIRA_URL", srv.URL+"/jira")
	t.Setenv("JIRA_API_TOKEN", testenv.Token)
	t.Setenv("CONFLUENCE_URL", srv.URL+"/wiki")
	t.Setenv("CONFLUENCE_API_TOKEN", testenv.Token)
	out, errs, code := runAtl(t, "issue", "PROJ-123")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "# PROJ-123: Fix login", "(description)")
	if _, errs, code := runAtl(t, "page", "42"); code != 0 {
		t.Fatalf("page: exit %d: %s", code, errs)
	}

	t.Setenv("JIRA_USERNAME", "me")
	if _, errs, code := runAtl(t, "issue", "PROJ-123"); code != 0 {
		t.Fatalf("basic auth via JIRA_USERNAME: exit %d: %s", code, errs)
	}
	t.Setenv("JIRA_USERNAME", "")

	t.Setenv("JIRA_API_TOKEN", "")
	if _, errs, code := runAtl(t, "issue", "PROJ-123"); code != cli.ExitConfig || !strings.Contains(errs, "JIRA_API_TOKEN") {
		t.Errorf("missing token: exit %d, %s", code, errs)
	}
}

func TestPageDemotesHeadings(t *testing.T) {
	testenv.Setup(t)
	out, errs, code := runAtl(t, "page", "77")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "# Design\n", "\n## Overview\n", "\n### Goals\n", "\n#### Non-goals\n", "# not a heading",
		"| **Cell heading** |")
	if strings.Contains(out, "\n# Overview") {
		t.Errorf("page h1 should nest under the title:\n%s", out)
	}

	out, _, _ = runAtl(t, "issue", "PROJ-123", "--comments")
	testenv.MustContain(t, out, "\n### Steps\n", "\n#### Note\n")
}

func TestVersion(t *testing.T) {
	old := cli.Version
	cli.Version = "v1.2.3"
	t.Cleanup(func() { cli.Version = old })
	for _, arg := range []string{"version", "--version"} {
		if out, _, code := runAtl(t, arg); code != 0 || out != "atl v1.2.3\n" {
			t.Errorf("%s: exit %d, %q", arg, code, out)
		}
	}
}

func TestSettingsPrecedence(t *testing.T) {
	srv := testenv.FakeServer(t)
	testenv.ClearEnv(t)
	t.Setenv("ATL_CONFIG", filepath.Join(t.TempDir(), "absent.ini"))

	// The environment beats the config file.
	testenv.WriteConfig(t, fmt.Sprintf("[jira]\nurl = %s/jira\n", srv.URL), 0o600)
	t.Setenv("JIRA_API_TOKEN", testenv.Token)
	out, _, code := runAtl(t, "doctor")
	if code != 0 {
		t.Errorf("doctor: exit %d\n%s", code, out)
	}
	testenv.MustContain(t, out, "url        "+srv.URL+"/jira (config file)", "✓ jira", "- confluence: not configured")
	t.Setenv("JIRA_URL", "http://127.0.0.1:1/env")
	out, _, _ = runAtl(t, "doctor")
	testenv.MustContain(t, out, "url        http://127.0.0.1:1/env (JIRA_URL)", "✗ jira")

	t.Setenv("JIRA_URL", "")
	testenv.WriteConfig(t, "[jira]\ntoken = t\n", 0o600)
	if _, errs, code := runAtl(t, "issue", "PROJ-1"); code != cli.ExitConfig || !strings.Contains(errs, "jira needs a URL: set JIRA_URL") {
		t.Errorf("URL missing: exit %d, %s", code, errs)
	}
}

func TestJSONOutput(t *testing.T) {
	testenv.Setup(t)
	for _, args := range [][]string{
		{"issue", "PROJ-123", "--json"},
		{"issue", "PROJ-5", "--json"},
		{"page", "42", "--json"},
		{"search", "login", "--json"},
	} {
		out, errs, code := runAtl(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errs)
		}
		var v any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Errorf("%v: not JSON: %v\n%s", args, err, out)
		}
	}
	out, _, _ := runAtl(t, "issue", "PROJ-5", "--json")
	testenv.MustContain(t, out, `"remotelinks": null`)
}
