package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	authorized := func(w http.ResponseWriter, r *http.Request) bool {
		user, pass, basic := r.BasicAuth()
		if r.Header.Get("Authorization") != "Bearer "+token && !(basic && user == "me" && pass == token) {
			http.Redirect(w, r, "/login.jsp", http.StatusFound)
			return false
		}
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		return true
	}
	reply := func(path string, body func(*http.Request) string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if authorized(w, r) {
				fmt.Fprint(w, strings.ReplaceAll(body(r), "BASE", srv.URL))
			}
		})
	}
	jsonReply := func(path, body string) {
		reply(path, func(*http.Request) string { return body })
	}
	// issueReply serves renderedFields only when asked for, as Jira does.
	issueReply := func(key, fields, rendered string) {
		reply("/jira/rest/api/2/issue/"+key, func(r *http.Request) string {
			// `issue` asks for everything; epic lookups name a few fields.
			if f := r.URL.Query().Get("fields"); f != "*all" && !strings.HasPrefix(f, "summary,status,issuetype") {
				t.Errorf("%s fetched with fields=%q", key, f)
			}
			body := `{"key":"` + key + `","fields":` + fields
			if rendered != "" && r.URL.Query().Get("expand") == "renderedFields" {
				body += `,"renderedFields":` + rendered
			}
			return body + "}"
		})
	}

	jsonReply("/jira/rest/api/2/field", `[
		{"id":"summary","name":"Summary","custom":false,"schema":{"type":"string"}},
		{"id":"customfield_10008","name":"Epic Link","custom":true,"schema":{"type":"any","custom":"com.pyxis.greenhopper.jira:gh-epic-link"}},
		{"id":"customfield_10010","name":"Story Points","custom":true,"schema":{"type":"number"}},
		{"id":"customfield_10011","name":"Acceptance Criteria","custom":true,"schema":{"type":"string","custom":"com.atlassian.jira.plugin.system.customfieldtypes:textarea"}},
		{"id":"customfield_10012","name":"Sprint","custom":true,"schema":{"type":"array","custom":"com.pyxis.greenhopper.jira:gh-sprint"}},
		{"id":"customfield_10013","name":"Rank","custom":true,"schema":{"type":"any","custom":"com.pyxis.greenhopper.jira:gh-lexo-rank"}},
		{"id":"customfield_10014","name":"Team","custom":true,"schema":{"type":"option"}},
		{"id":"customfield_10015","name":"Reviewers","custom":true,"schema":{"type":"array"}},
		{"id":"customfield_10016","name":"Region","custom":true,"schema":{"type":"option-with-child"}},
		{"id":"customfield_10017","name":"Empty","custom":true,"schema":{"type":"string"}},
		{"id":"customfield_10018","name":"Flagged","custom":true,"schema":{"type":"boolean"}},
		{"id":"customfield_10019","name":"Opaque","custom":true,"schema":{"type":"any"}},
		{"id":"customfield_10020","name":"Development","custom":true,"schema":{"type":"any","custom":"com.atlassian.jira.plugins.jira-development-integration-plugin:devsummary"}},
		{"id":"customfield_10021","name":"Hidden Rank","custom":true,"schema":{"type":"string"}},
		{"id":"customfield_10022","name":"Board Sprint","custom":true,"schema":{"type":"array"}},
		{"id":"customfield_10023","name":"Absent","custom":true,"schema":{"type":"string"}}
	]`)
	issueReply("PROJ-123", `{
		"summary":"Fix login","description":"See BASE/wiki/display/DEV/Login+Flow and [spec|BASE/wiki/pages/viewpage.action?pageId=42].",
		"status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},
		"issuetype":{"name":"Story"},"priority":{"name":"High"},"resolution":null,
		"assignee":{"displayName":"Ann"},"reporter":{"displayName":"Bob"},
		"created":"2026-10-01T09:30:00.000+0000","updated":"2026-10-02T10:00:00.000+0000",
		"labels":["auth"],"components":[{"name":"web"}],"fixVersions":[],
		"customfield_10008":"PROJ-1",
		"customfield_10010":5,
		"customfield_10011":"* Login works\n* Logout works",
		"customfield_10012":["com.atlassian.greenhopper.service.sprint.Sprint@1a2b[id=7,rapidViewId=3,state=ACTIVE,name=Sprint 5,startDate=2026-10-01]"],
		"customfield_10013":"0|i0000f:",
		"customfield_10014":{"self":"x","value":"Core","id":"1"},
		"customfield_10015":[{"displayName":"Ann"},{"displayName":"Dee"}],
		"customfield_10016":{"value":"EU","child":{"value":"NL"}},
		"customfield_10017":null,
		"customfield_10018":true,
		"customfield_10019":{"id":"5"},
		"customfield_10020":"{summaryBean=com.atlassian.jira.plugin.devstatus.rest.SummaryBean@1}",
		"customfield_10021":"1|hzzzzz:",
		"customfield_10022":[{"id":9,"name":"Sprint 6","state":"future"}],
		"issuelinks":[
			{"type":{"name":"Relates","inward":"relates to","outward":"relates to"},"outwardIssue":{"key":"PROJ-9","fields":{"summary":"Docs","status":{"name":"Open","statusCategory":{"key":"new"}},"issuetype":{"name":"Task"}}}},
			{"type":{"name":"Blocks","inward":"is blocked by","outward":"blocks"},"inwardIssue":{"key":"PROJ-7","fields":{"summary":"API","status":{"name":"Open","statusCategory":{"key":"new"}},"issuetype":{"name":"Bug"}}}},
			{"type":{"name":"Blocks","inward":"is blocked by","outward":"blocks"},"inwardIssue":{"key":"PROJ-8","fields":{"summary":"Done dep","status":{"name":"Closed","statusCategory":{"key":"done"}}}}}
		],
		"subtasks":[{"key":"PROJ-124","fields":{"summary":"Tests","status":{"name":"To Do","statusCategory":{"key":"new"}}}}],
		"comment":{"comments":[{"id":"501","author":{"displayName":"Cy"},"created":"2026-10-03T08:00:00.000+0000","body":"Also BASE/wiki/x/KgAAAA."}]}
	}`, `{
		"description":"<p>See <a href=\"BASE/wiki/display/DEV/Login+Flow\">Login Flow</a>.</p><h3>Steps</h3><ul><li>open page</li></ul>",
		"customfield_10011":"<ul><li>Login <b>works</b></li><li>Logout works</li></ul>",
		"customfield_10010":"5",
		"comment":{"comments":[{"id":"501","body":"<p>Also <em>this</em>.</p>"}]}
	}`)
	issueReply("PROJ-1", `{"summary":"Login epic","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"issuetype":{"name":"Epic"}}`, "")
	issueReply("PROJ-124", `{"summary":"Tests","issuetype":{"name":"Sub-task"},"parent":{"key":"PROJ-123","fields":{"summary":"Fix login"}}}`, "")
	issueReply("PROJ-77", `{"summary":"Child","issuetype":{"name":"Story"},"parent":{"key":"PROJ-1"}}`, "")
	issueReply("PROJ-78", `{"summary":"Orphan sub","issuetype":{"name":"Sub-task"},"parent":{"key":"PROJ-5"}}`, "")
	mux.HandleFunc("/jira/rest/api/2/search", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		q := r.URL.Query()
		if q.Get("fields") != "summary,status,issuetype,priority,assignee,updated" {
			t.Errorf("search fields=%q", q.Get("fields"))
		}
		switch jql := q.Get("jql"); jql {
		case `"Epic Link" = PROJ-1 OR parent = PROJ-1 ORDER BY key`:
			if q.Get("maxResults") == "1" {
				fmt.Fprint(w, `{"total":5,"issues":[{"key":"PROJ-7","fields":{"summary":"API","status":{"name":"Open","statusCategory":{"key":"new"}}}}]}`)
				return
			}
			fmt.Fprint(w, `{"total":5,"issues":[
				{"key":"PROJ-123","fields":{"summary":"Fix login","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"issuetype":{"name":"Story"},"assignee":{"displayName":"Ann"},"updated":"2026-10-02T10:00:00.000+0000"}},
				{"key":"PROJ-7","fields":{"summary":"API","status":{"name":"Open","statusCategory":{"key":"new"}},"issuetype":{"name":"Bug"}}},
				{"key":"PROJ-8","fields":{"summary":"Done dep","status":{"name":"Closed","statusCategory":{"key":"done"}}}},
				{"key":"PROJ-10","fields":{"summary":"Odd","status":{"name":"Parked","statusCategory":{"key":"undefined"}}}},
				{"key":"PROJ-11","fields":{"summary":"No status"}}
			]}`)
		case `"Epic Link" = PROJ-5 OR parent = PROJ-5 ORDER BY key`, "project = NONE":
			fmt.Fprint(w, `{"total":0,"issues":[]}`)
		case "project = PROJ ORDER BY updated DESC":
			if q.Get("maxResults") != "2" {
				t.Errorf("maxResults=%q", q.Get("maxResults"))
			}
			fmt.Fprint(w, `{"total":135,"issues":[
				{"key":"PROJ-123","fields":{"summary":"Fix login","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"assignee":{"displayName":"Ann"},"updated":"2026-10-02T10:00:00.000+0000"}},
				{"key":"PROJ-7","fields":{"summary":"API","status":{"name":"Open","statusCategory":{"key":"new"}}}}
			]}`)
		default:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"errorMessages":["Error in the JQL Query: Expecting ')' before the end of the query."],"errors":{"jql":"bad"}}`)
		}
	})
	issueReply("PROJ-5", `{"summary":"Plain","description":"h2. Raw wiki","comment":{"comments":[{"id":"9","body":"*raw* comment"}]}}`, "")
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

// clearEnv keeps a developer's real credentials out of the tests.
func clearEnv(t *testing.T) {
	for _, svc := range []string{"JIRA", "CONFLUENCE"} {
		for _, v := range []string{"_URL", "_API_TOKEN", "_USERNAME"} {
			t.Setenv(svc+v, "")
		}
	}
}

func writeConfig(t *testing.T, body string, mode os.FileMode) {
	t.Helper()
	clearEnv(t)
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
	mustContain(t, out, "## Comments (1)", "### Cy — 2026-10-03 08:00\n\nAlso *this*.")
}

func TestIssueFieldsAndRendering(t *testing.T) {
	srv := setup(t)
	out, errs, code := runAtl(t, "issue", "PROJ-123")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out,
		"## Fields\n\n- Board Sprint: Sprint 6 (future)\n- Flagged: yes\n- Region: EU / NL\n- Reviewers: Ann, Dee\n- Sprint: Sprint 5 (ACTIVE)\n- Story Points: 5\n- Team: Core\n",
		"## Description\n\nSee [Login Flow]("+srv.URL+"/wiki/display/DEV/Login+Flow).\n\n### Steps\n\n- open page\n",
		"## Acceptance Criteria\n\n- Login **works**\n- Logout works\n",
	)
	for _, hidden := range []string{"Epic Link", "Rank", "Opaque", "Development", "summaryBean", "Empty", "Absent", "0|i0000f"} {
		if strings.Contains(out, hidden) {
			t.Errorf("output shows %q:\n%s", hidden, out)
		}
	}
	if strings.Index(out, "## Fields") > strings.Index(out, "## Description") || strings.Index(out, "## Acceptance Criteria") < strings.Index(out, "## Description") {
		t.Errorf("short fields go before the description and long ones after it:\n%s", out)
	}
}

func TestIssueWithoutRenderedFields(t *testing.T) {
	setup(t)
	out, errs, code := runAtl(t, "issue", "PROJ-5", "--comments")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out, "## Description\n\nh2. Raw wiki\n", "*raw* comment", "(remote links unavailable:")
	if strings.Contains(out, "## Fields") {
		t.Errorf("no custom fields expected:\n%s", out)
	}
}

func TestRenderValue(t *testing.T) {
	for raw, want := range map[string]string{
		`"plain"`:                             "plain",
		`" pad "`:                             "pad",
		`3.5`:                                 "3.5",
		`false`:                               "no",
		`null`:                                "",
		`[]`:                                  "",
		`{}`:                                  "",
		`[null,""]`:                           "",
		`not json`:                            "",
		`{"value":"A","child":{"value":"B"}}`: "A / B",
		`{"value":"A","child":{"id":"1"}}`:    "A",
		`{"name":"PROJ board","key":"X"}`:     "PROJ board",
		`{"key":"PROJ"}`:                      "PROJ",
		`{"title":"Doc","state":""}`:          "Doc",
		`["com.atlassian.greenhopper.service.sprint.Sprint@1[id=1,name=S1]"]`: "S1",
		`"com.atlassian.greenhopper.service.sprint.Sprint@1[id=1]"`:           "",
		`["a",{"displayName":"B"},2]`:                                         "a, B, 2",
	} {
		if got := renderValue([]byte(raw)); got != want {
			t.Errorf("renderValue(%s) = %q, want %q", raw, got, want)
		}
	}
}

func TestRichTextFallsBack(t *testing.T) {
	for rendered, want := range map[string]string{
		``:              "raw",
		`null`:          "raw",
		`"  "`:          "raw",
		`"<p></p>"`:     "raw",
		`"<p>html</p>"`: "html",
	} {
		if got := richText([]byte(rendered), " raw "); got != want {
			t.Errorf("richText(%s) = %q, want %q", rendered, got, want)
		}
	}
}

func TestJSONOutput(t *testing.T) {
	setup(t)
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
	mustContain(t, out, `"remotelinks": null`)
}

func TestKeyFromBranch(t *testing.T) {
	setup(t)
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "feature/proj-123-login")
	t.Chdir(dir)
	out, errs, code := runAtl(t, "issue")
	if code != 0 || !strings.Contains(errs, "PROJ-123 from branch feature/proj-123-login") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out, "# PROJ-123: Fix login")

	git("checkout", "-q", "-b", "main")
	if _, errs, code := runAtl(t, "issue"); code != exitUsage || !strings.Contains(errs, `no issue key in branch "main"`) {
		t.Errorf("keyless branch: exit %d, %s", code, errs)
	}
	t.Chdir(t.TempDir())
	if _, errs, code := runAtl(t, "issue"); code != exitUsage || !strings.Contains(errs, "not inside a git repository") {
		t.Errorf("outside git: exit %d, %s", code, errs)
	}
}

func TestRunUsage(t *testing.T) {
	if _, errs, code := runAtl(t); code != exitUsage || !strings.Contains(errs, "atl issue") {
		t.Errorf("no args: exit %d, %s", code, errs)
	}
	if out, _, code := runAtl(t, "--help"); code != 0 || !strings.Contains(out, "Exit codes") {
		t.Errorf("--help: exit %d, %s", code, out)
	}
	if _, errs, code := runAtl(t, "frob"); code != exitUsage || !strings.Contains(errs, `unknown command "frob"`) {
		t.Errorf("unknown: exit %d, %s", code, errs)
	}
	if _, errs, code := runAtl(t, "issue", "-h"); code != 0 || !strings.Contains(errs, "usage: atl issue") {
		t.Errorf("issue -h: exit %d, %s", code, errs)
	}
	for _, args := range [][]string{{"issue", "A-1", "B-2"}, {"page"}, {"search"}} {
		if _, _, code := runAtl(t, args...); code != exitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
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

func TestEnvOnly(t *testing.T) {
	srv := fakeServer(t)
	clearEnv(t)
	t.Setenv("ATL_CONFIG", filepath.Join(t.TempDir(), "absent.ini"))
	t.Setenv("JIRA_URL", srv.URL+"/jira")
	t.Setenv("JIRA_API_TOKEN", token)
	t.Setenv("CONFLUENCE_URL", srv.URL+"/wiki")
	t.Setenv("CONFLUENCE_API_TOKEN", token)
	out, errs, code := runAtl(t, "issue", "PROJ-123")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out, "# PROJ-123: Fix login", "(description)")
	if _, errs, code := runAtl(t, "page", "42"); code != 0 {
		t.Fatalf("page: exit %d: %s", code, errs)
	}

	t.Setenv("JIRA_USERNAME", "me")
	if _, errs, code := runAtl(t, "issue", "PROJ-123"); code != 0 {
		t.Fatalf("basic auth via JIRA_USERNAME: exit %d: %s", code, errs)
	}
	t.Setenv("JIRA_USERNAME", "")

	t.Setenv("JIRA_API_TOKEN", "")
	if _, errs, code := runAtl(t, "issue", "PROJ-123"); code != exitConfig || !strings.Contains(errs, "JIRA_API_TOKEN") {
		t.Errorf("missing token: exit %d, %s", code, errs)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	srv := fakeServer(t)
	writeConfig(t, "[jira]\nurl = http://127.0.0.1:1/nowhere\ntoken = wrong\n", 0o600)
	t.Setenv("JIRA_URL", srv.URL+"/jira")
	t.Setenv("JIRA_API_TOKEN", token)
	if _, errs, code := runAtl(t, "issue", "PROJ-123"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
}

func TestClientConfigErrors(t *testing.T) {
	srv := fakeServer(t)
	pem := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(pem, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	for cfg, want := range map[string]string{
		"[jira]\nurl = U\ntoken = T\nauth = digest\n":            "auth must be bearer or basic",
		"[jira]\nurl = U\ntoken = T\nauth = basic\n":             "basic auth needs a username",
		"[jira]\nurl = U\ntoken = T\ntimeout = soon\n":           "timeout must be seconds",
		"[jira]\nurl = U\ntoken = T\nca_bundle = /nonexistent\n": "ca_bundle",
		"[jira]\nurl = U\ntoken = T\nca_bundle = " + pem + "\n":  "holds no PEM certificates",
		"url = U\n": "expected key = value",
	} {
		writeConfig(t, strings.ReplaceAll(cfg, "U", srv.URL+"/jira"), 0o600)
		if _, errs, code := runAtl(t, "issue", "PROJ-123"); code != exitConfig || !strings.Contains(errs, want) {
			t.Errorf("%q: exit %d, %s", cfg, code, errs)
		}
	}
	writeConfig(t, fmt.Sprintf("[jira]\nurl = %s/jira\ntoken = %s\ntimeout = 5\n", srv.URL, token), 0o600)
	if _, errs, code := runAtl(t, "issue", "PROJ-123"); code != 0 {
		t.Errorf("timeout setting: exit %d, %s", code, errs)
	}
}

func TestHTTPErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/jira/rest/api/2/field", func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer html":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html>login</html>")
		case "Bearer forbidden":
			http.Error(w, "no", http.StatusForbidden)
		case "Bearer badreq":
			http.Error(w, "nope", http.StatusBadRequest)
		case "Bearer broken":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, "{not json")
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	for tok, want := range map[string]string{
		"html":      "did not return JSON",
		"forbidden": "HTTP 403",
		"broken":    "decoding",
		"other":     "HTTP 500 on /rest/api/2/field: boom",
	} {
		writeConfig(t, fmt.Sprintf("[jira]\nurl = %s/jira\ntoken = %s\n", srv.URL, tok), 0o600)
		if _, errs, code := runAtl(t, "issue", "PROJ-1"); code != exitAPI || !strings.Contains(errs, want) {
			t.Errorf("%s: exit %d, %s", tok, code, errs)
		}
	}
	writeConfig(t, fmt.Sprintf("[jira]\nurl = %s/jira\ntoken = badreq\n", srv.URL), 0o600)
	if _, errs, code := runAtl(t, "issue", "PROJ-1"); code != exitUsage || !strings.Contains(errs, "HTTP 400 on /rest/api/2/field: nope") {
		t.Errorf("unparseable 400: exit %d, %s", code, errs)
	}
	writeConfig(t, "[jira]\nurl = http://127.0.0.1:1\ntoken = x\n", 0o600)
	if _, _, code := runAtl(t, "issue", "PROJ-1"); code != exitAPI {
		t.Errorf("unreachable: exit %d", code)
	}
}

func TestIssueEpicLine(t *testing.T) {
	setup(t)
	out, errs, code := runAtl(t, "issue", "PROJ-123")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out, "Epic: PROJ-1 [In Progress] Login epic (Epic)\n")
}

func TestJQL(t *testing.T) {
	setup(t)
	out, errs, code := runAtl(t, "jql", "project", "=", "PROJ", "ORDER BY updated DESC", "--limit", "2")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out,
		"- PROJ-123 [In Progress] Fix login · Ann · updated 2026-10-02 10:00\n",
		"- PROJ-7 [Open] API\n",
		"showing 2 of 135; raise --limit or narrow the query",
	)

	out, _, code = runAtl(t, "jql", "project = NONE")
	if code != 0 || !strings.Contains(out, "no issues for: project = NONE") {
		t.Errorf("empty result: exit %d, %s", code, out)
	}

	_, errs, code = runAtl(t, "jql", "project = (")
	if code != exitUsage || !strings.Contains(errs, "jira: rejected the request: Error in the JQL Query: Expecting ')' before the end of the query.; jql: bad") {
		t.Errorf("bad JQL: exit %d, %s", code, errs)
	}

	out, _, code = runAtl(t, "jql", "project = NONE", "--json")
	if code != 0 || !strings.Contains(out, `"total": 0`) {
		t.Errorf("--json: exit %d, %s", code, out)
	}
	if _, _, code := runAtl(t, "jql"); code != exitUsage {
		t.Errorf("no query: exit %d", code)
	}
}

func TestEpic(t *testing.T) {
	srv := setup(t)
	out, errs, code := runAtl(t, "epic", "PROJ-1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out,
		"# PROJ-1 [In Progress] Login epic (Epic)\n",
		"5 issues: 2 other, 1 done, 1 in progress, 1 to do\n",
		"URL: "+srv.URL+"/jira/browse/PROJ-1",
		"## To do (1)\n\n- PROJ-7 [Open] API (Bug)\n",
		"## In progress (1)\n\n- PROJ-123 [In Progress] Fix login (Story) · Ann · updated 2026-10-02 10:00\n",
		"## Done (1)\n\n- PROJ-8 [Closed] Done dep\n",
		"## Other (2)\n\n- PROJ-10 [Parked] Odd\n- PROJ-11 [?] No status\n",
	)
	if strings.Index(out, "## To do") > strings.Index(out, "## In progress") || strings.Index(out, "## In progress") > strings.Index(out, "## Done") {
		t.Errorf("groups out of order:\n%s", out)
	}
	if strings.Contains(out, "counts cover") {
		t.Errorf("complete result reported as truncated:\n%s", out)
	}

	out, _, _ = runAtl(t, "epic", "PROJ-1", "--limit", "5", "--json")
	mustContain(t, out, `"epic": {`, `"children": {`, `"total": 5`)
}

func TestEpicResolution(t *testing.T) {
	setup(t)
	for key, note := range map[string]string{
		"PROJ-123": "atl: PROJ-123 is a Story; showing its epic PROJ-1\n",
		"PROJ-124": "atl: PROJ-124 is a Sub-task; showing its parent PROJ-123's epic PROJ-1\n",
		"PROJ-77":  "atl: PROJ-77 is a Story; showing its parent PROJ-1\n",
	} {
		out, errs, code := runAtl(t, "epic", key)
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", key, code, errs)
		}
		if !strings.Contains(errs, note) {
			t.Errorf("%s: stderr %q, want %q", key, errs, note)
		}
		mustContain(t, out, "# PROJ-1 [In Progress] Login epic (Epic)")
	}

	out, errs, code := runAtl(t, "epic", "PROJ-5")
	if code != 0 {
		t.Fatalf("PROJ-5: exit %d: %s", code, errs)
	}
	mustContain(t, errs, "atl: PROJ-5 has no epic or parent; listing its own children")
	mustContain(t, out, "# PROJ-5 [?] Plain\n", `no child issues for: "Epic Link" = PROJ-5 OR parent = PROJ-5 ORDER BY key`)

	out, errs, _ = runAtl(t, "epic", "PROJ-78")
	mustContain(t, errs, "atl: PROJ-78 is a Sub-task; showing its parent PROJ-5\n")
	mustContain(t, out, "# PROJ-5 [?] Plain")

	if _, _, code := runAtl(t, "epic", "PROJ-999"); code != exitNotFound {
		t.Errorf("missing issue: exit %d", code)
	}
	if _, _, code := runAtl(t, "epic", "A-1", "B-2"); code != exitUsage {
		t.Errorf("two keys: exit %d", code)
	}
}

// An instance without the Epic Link field relates issues by parent only.
func TestEpicWithoutEpicLinkField(t *testing.T) {
	mux := http.NewServeMux()
	var seenJQL string
	mux.HandleFunc("/rest/api/2/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/2/field":
			fmt.Fprint(w, `[{"id":"summary","name":"Summary"}]`)
		case "/rest/api/2/issue/EP-1":
			fmt.Fprint(w, `{"key":"EP-1","fields":{"summary":"Epic","issuetype":{"name":"Epic"}}}`)
		case "/rest/api/2/search":
			seenJQL = r.URL.Query().Get("jql")
			fmt.Fprint(w, `{"total":1,"issues":[{"key":"EP-2","fields":{"summary":"Child","status":{"name":"Done","statusCategory":{"key":"done"}}}}]}`)
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	writeConfig(t, fmt.Sprintf("[jira]\nurl = %s\ntoken = x\n", srv.URL), 0o600)
	out, errs, code := runAtl(t, "epic", "ep-1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if seenJQL != "parent = EP-1 ORDER BY key" {
		t.Errorf("jql = %q", seenJQL)
	}
	mustContain(t, out, "1 issues: 1 done", "- EP-2 [Done] Child")
}

func TestEpicTruncated(t *testing.T) {
	setup(t)
	out, errs, code := runAtl(t, "epic", "PROJ-1", "--limit", "1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out, "5 issues: 1 to do\n", "counts cover the first 1 of 5; raise --limit for all")
}

func TestBadRequestMessage(t *testing.T) {
	for body, want := range map[string]string{
		`{"errorMessages":["a"],"errors":{"z":"2","b":"1"}}`: "a; b: 1; z: 2",
		`{"statusCode":400,"message":"Could not parse cql"}`: "Could not parse cql",
		`{}`:       "",
		`not json`: "",
	} {
		if got := badRequestMessage([]byte(body)); got != want {
			t.Errorf("%s: got %q, want %q", body, got, want)
		}
	}
}
