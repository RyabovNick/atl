package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const token = "test-token"

func fakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	jsonReply := func(path, body string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+token {
				http.Redirect(w, r, "/login.jsp", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "application/json;charset=UTF-8")
			fmt.Fprint(w, strings.ReplaceAll(body, "BASE", srv.URL))
		})
	}

	jsonReply("/jira/rest/api/2/field", `[{"id":"summary","name":"Summary"},{"id":"customfield_10008","name":"Epic Link"}]`)
	jsonReply("/jira/rest/api/2/issue/PROJ-123", `{"key":"PROJ-123","fields":{
		"summary":"Fix login","description":"See BASE/wiki/display/DEV/Login+Flow and [spec|BASE/wiki/pages/viewpage.action?pageId=42].",
		"status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},
		"issuetype":{"name":"Story"},"priority":{"name":"High"},"resolution":null,
		"assignee":{"displayName":"Ann"},"reporter":{"displayName":"Bob"},
		"created":"2026-10-01T09:30:00.000+0000","updated":"2026-10-02T10:00:00.000+0000",
		"labels":["auth"],"components":[{"name":"web"}],"fixVersions":[],
		"customfield_10008":"PROJ-1",
		"issuelinks":[
			{"type":{"name":"Relates","inward":"relates to","outward":"relates to"},"outwardIssue":{"key":"PROJ-9","fields":{"summary":"Docs","status":{"name":"Open","statusCategory":{"key":"new"}},"issuetype":{"name":"Task"}}}},
			{"type":{"name":"Blocks","inward":"is blocked by","outward":"blocks"},"inwardIssue":{"key":"PROJ-7","fields":{"summary":"API","status":{"name":"Open","statusCategory":{"key":"new"}},"issuetype":{"name":"Bug"}}}},
			{"type":{"name":"Blocks","inward":"is blocked by","outward":"blocks"},"inwardIssue":{"key":"PROJ-8","fields":{"summary":"Done dep","status":{"name":"Closed","statusCategory":{"key":"done"}}}}}
		],
		"subtasks":[{"key":"PROJ-124","fields":{"summary":"Tests","status":{"name":"To Do","statusCategory":{"key":"new"}}}}],
		"comment":{"comments":[{"author":{"displayName":"Cy"},"created":"2026-10-03T08:00:00.000+0000","body":"Also BASE/wiki/x/KgAAAA."}]}
	}}`)
	jsonReply("/jira/rest/api/2/issue/PROJ-123/remotelink", `[
		{"object":{"url":"BASE/wiki/pages/viewpage.action?pageId=42","title":"Login spec"},"application":{"type":"com.atlassian.confluence"}},
		{"object":{"url":"https://github.com/x/y/pull/1","title":"PR 1"},"application":{}}
	]`)

	jsonReply("/wiki/rest/api/content/42", `{"id":"42","title":"Login spec","space":{"key":"DEV","name":"Development"},
		"version":{"number":3,"when":"2026-09-30T12:00:00.000Z","by":{"displayName":"Ann"}},
		"ancestors":[{"title":"Home"},{"title":"Auth"}],
		"body":{"view":{"value":"<h2>Flow</h2><p>Users <strong>log in</strong>.</p><ul><li>one</li></ul>"}},
		"_links":{"webui":"/display/DEV/Login+spec"}}`)
	jsonReply("/wiki/rest/api/content", `{"results":[{"id":"42","title":"Login Flow"}]}`)
	jsonReply("/wiki/rest/api/content/search", `{"results":[{"id":"42","title":"Login spec","space":{"key":"DEV"},"version":{"when":"2026-09-30T12:00:00.000Z"},"_links":{"webui":"/display/DEV/Login+spec"}}],"size":1}`)
	mux.HandleFunc("/wiki/x/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/wiki/pages/viewpage.action?pageId=42", http.StatusFound)
	})
	mux.HandleFunc("/", http.NotFound)

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeConfig(t *testing.T, body string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.ini")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATL_CONFIG", path)
}

func setup(t *testing.T) *httptest.Server {
	srv := fakeServer(t)
	writeConfig(t, fmt.Sprintf("# test\n[jira]\nurl = %[1]s/jira/\ntoken = %[2]s\n\n[confluence]\nurl = %[1]s/wiki\ntoken = %[2]s\n", srv.URL, token), 0o600)
	return srv
}

func runAtl(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return out.String(), errb.String(), code
}

func mustContain(t *testing.T, s string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Errorf("output lacks %q:\n%s", w, s)
		}
	}
}

func TestIssue(t *testing.T) {
	srv := setup(t)
	out, errs, code := runAtl(t, "issue", "proj-123")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out,
		"# PROJ-123: Fix login",
		"Type: Story · Status: In Progress · Priority: High · Resolution: Unresolved",
		"Assignee: Ann · Reporter: Bob",
		"Created: 2026-10-01 09:30",
		"Epic: PROJ-1",
		"Labels: auth",
		"Components: web",
		"URL: "+srv.URL+"/jira/browse/PROJ-123",
		"Open blockers: PROJ-7\n",
		"- PROJ-124 [To Do] Tests",
		"Login spec — "+srv.URL+"/wiki/pages/viewpage.action?pageId=42 (remote link)",
		srv.URL+"/wiki/display/DEV/Login+Flow (description)",
		srv.URL+"/wiki/x/KgAAAA (comment)",
		"PR 1 — https://github.com/x/y/pull/1",
		"(1 comments; add --comments to read them)",
	)
	// Blocking relations come before the rest; the pageId URL appears once despite two sources.
	if strings.Index(out, "is blocked by PROJ-7") > strings.Index(out, "relates to PROJ-9") {
		t.Errorf("blocking links should sort first:\n%s", out)
	}
	if section := out[strings.Index(out, "## Confluence"):]; strings.Count(section, "pageId=42") != 1 {
		t.Errorf("confluence refs should be deduplicated:\n%s", out)
	}

	out, _, _ = runAtl(t, "issue", "PROJ-123", "--comments")
	mustContain(t, out, "## Comments (1)", "### Cy — 2026-10-03 08:00")
}

func TestIssueWithoutConfluenceConfig(t *testing.T) {
	srv := fakeServer(t)
	writeConfig(t, fmt.Sprintf("[jira]\nurl = %s/jira\ntoken = %s\n", srv.URL, token), 0o600)
	out, errs, code := runAtl(t, "issue", "PROJ-123")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out, "Login spec — ")
	if strings.Contains(out, "(description)") {
		t.Errorf("text URLs need a confluence url to be recognised:\n%s", out)
	}
}

func TestPageRefs(t *testing.T) {
	srv := setup(t)
	for _, ref := range []string{
		"42",
		srv.URL + "/wiki/pages/viewpage.action?pageId=42",
		srv.URL + "/wiki/spaces/DEV/pages/42/Login+spec",
		srv.URL + "/wiki/display/DEV/Login+Flow",
		srv.URL + "/wiki/x/KgAAAA",
		srv.URL + "/wiki/x/zzzz", // decodes to a missing page; resolved by redirect
	} {
		out, errs, code := runAtl(t, "page", ref)
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", ref, code, errs)
		}
		mustContain(t, out, "# Login spec", "Space: Development (DEV) · id 42", "Version 3 by Ann", "Path: Home / Auth",
			"URL: "+srv.URL+"/wiki/display/DEV/Login+spec", "## Flow", "**log in**", "- one")
	}
	out, _, _ := runAtl(t, "page", "42", "--max-chars", "5")
	mustContain(t, out, "[truncated at 5 of")
}

func TestSearch(t *testing.T) {
	setup(t)
	out, errs, code := runAtl(t, "search", "login", "flow", "--space", "DEV")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out, "- Login spec — DEV · updated 2026-09-30 12:00 · id 42")
	if got := cqlString(`a "b" \c`); got != `"a \"b\" \\c"` {
		t.Errorf("cqlString = %s", got)
	}
}

func TestErrors(t *testing.T) {
	srv := fakeServer(t)
	cfg := fmt.Sprintf("[jira]\nurl = %s/jira\ntoken = wrong\n", srv.URL)

	writeConfig(t, cfg, 0o644)
	if _, errs, code := runAtl(t, "issue", "PROJ-123"); code != exitConfig || !strings.Contains(errs, "chmod 600") {
		t.Errorf("readable config: exit %d, %s", code, errs)
	}
	writeConfig(t, cfg, 0o600)
	if _, errs, code := runAtl(t, "issue", "PROJ-123"); code != exitAPI || !strings.Contains(errs, "redirected") {
		t.Errorf("bad token: exit %d, %s", code, errs)
	}
	writeConfig(t, fmt.Sprintf("[jira]\nurl = %s/jira\ntoken = %s\n", srv.URL, token), 0o600)
	if _, _, code := runAtl(t, "issue", "PROJ-999"); code != exitNotFound {
		t.Errorf("missing issue: exit %d", code)
	}
	if _, errs, code := runAtl(t, "page", "42"); code != exitConfig || !strings.Contains(errs, "[confluence]") {
		t.Errorf("missing confluence section: exit %d, %s", code, errs)
	}
	if _, _, code := runAtl(t, "issue", "--nope"); code != exitUsage {
		t.Errorf("bad flag: exit %d", code)
	}
}

func TestKeyFromBranchName(t *testing.T) {
	for branch, want := range map[string]string{
		"feature/PROJ-123-fix-login": "PROJ-123",
		"PROJ-7":                     "PROJ-7",
		"bugfix/ab2-45_thing":        "AB2-45",
		"main":                       "",
	} {
		got, _ := keyFromBranchName(branch)
		if got != want {
			t.Errorf("%s: got %q, want %q", branch, got, want)
		}
	}
}

func TestTinyRoundTrip(t *testing.T) {
	for _, id := range []uint64{42, 65538, 123456789, 1 << 40} {
		raw := make([]byte, 8)
		binary.LittleEndian.PutUint64(raw, id)
		code := strings.TrimRight(base64.StdEncoding.EncodeToString(raw), "A=")
		code = strings.NewReplacer("/", "-", "+", "_").Replace(code)
		got, err := tinyToPageID(code)
		if err != nil || got != strconv.FormatUint(id, 10) {
			t.Errorf("%d via %s: got %s, %v", id, code, got, err)
		}
	}
}
