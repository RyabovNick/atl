package jira

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RyabovNick/atl/internal/cli"
	"github.com/RyabovNick/atl/internal/testenv"
)

func runAtl(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return testenv.Run(t, map[string]testenv.Command{
		"issue":   RunIssue,
		"jql":     RunJQL,
		"epic":    RunEpic,
		"history": RunHistory,
	}, args...)
}

func TestIssue(t *testing.T) {
	srv := testenv.Setup(t)
	out, errs, code := runAtl(t, "issue", "proj-123")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out,
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
	testenv.MustContain(t, out, "## Comments (1)", "### Cy — 2026-10-03 08:00\n\nAlso *this*.")
}

func TestIssueFieldsAndRendering(t *testing.T) {
	srv := testenv.Setup(t)
	out, errs, code := runAtl(t, "issue", "PROJ-123")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out,
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
	testenv.Setup(t)
	out, errs, code := runAtl(t, "issue", "PROJ-5", "--comments")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "## Description\n\nh2. Raw wiki\n", "*raw* comment", "(remote links unavailable:")
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

func TestKeyFromBranch(t *testing.T) {
	testenv.Setup(t)
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
	testenv.MustContain(t, out, "# PROJ-123: Fix login")

	git("checkout", "-q", "-b", "main")
	if _, errs, code := runAtl(t, "issue"); code != cli.ExitUsage || !strings.Contains(errs, `no issue key in branch "main"`) {
		t.Errorf("keyless branch: exit %d, %s", code, errs)
	}
	t.Chdir(t.TempDir())
	if _, errs, code := runAtl(t, "issue"); code != cli.ExitUsage || !strings.Contains(errs, "not inside a git repository") {
		t.Errorf("outside git: exit %d, %s", code, errs)
	}
}

func TestIssueWithoutConfluenceConfig(t *testing.T) {
	srv := testenv.FakeServer(t)
	testenv.WriteConfig(t, fmt.Sprintf("[jira]\nurl = %s/jira\ntoken = %s\n", srv.URL, testenv.Token), 0o600)
	out, errs, code := runAtl(t, "issue", "PROJ-123")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "Login spec — ")
	if strings.Contains(out, "(description)") {
		t.Errorf("text URLs need a confluence url to be recognized:\n%s", out)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	srv := testenv.FakeServer(t)
	testenv.WriteConfig(t, "[jira]\nurl = http://127.0.0.1:1/nowhere\ntoken = wrong\n", 0o600)
	t.Setenv("JIRA_URL", srv.URL+"/jira")
	t.Setenv("JIRA_API_TOKEN", testenv.Token)
	if _, errs, code := runAtl(t, "issue", "PROJ-123"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
}

func TestClientConfigErrors(t *testing.T) {
	srv := testenv.FakeServer(t)
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
		testenv.WriteConfig(t, strings.ReplaceAll(cfg, "U", srv.URL+"/jira"), 0o600)
		if _, errs, code := runAtl(t, "issue", "PROJ-123"); code != cli.ExitConfig || !strings.Contains(errs, want) {
			t.Errorf("%q: exit %d, %s", cfg, code, errs)
		}
	}
	testenv.WriteConfig(t, fmt.Sprintf("[jira]\nurl = %s/jira\ntoken = %s\ntimeout = 5\n", srv.URL, testenv.Token), 0o600)
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
		testenv.WriteConfig(t, fmt.Sprintf("[jira]\nurl = %s/jira\ntoken = %s\n", srv.URL, tok), 0o600)
		if _, errs, code := runAtl(t, "issue", "PROJ-1"); code != cli.ExitAPI || !strings.Contains(errs, want) {
			t.Errorf("%s: exit %d, %s", tok, code, errs)
		}
	}
	testenv.WriteConfig(t, fmt.Sprintf("[jira]\nurl = %s/jira\ntoken = badreq\n", srv.URL), 0o600)
	if _, errs, code := runAtl(t, "issue", "PROJ-1"); code != cli.ExitUsage || !strings.Contains(errs, "HTTP 400 on /rest/api/2/field: nope") {
		t.Errorf("unparseable 400: exit %d, %s", code, errs)
	}
	testenv.WriteConfig(t, "[jira]\nurl = http://127.0.0.1:1\ntoken = x\n", 0o600)
	if _, _, code := runAtl(t, "issue", "PROJ-1"); code != cli.ExitAPI {
		t.Errorf("unreachable: exit %d", code)
	}
}

func TestIssueEpicLine(t *testing.T) {
	testenv.Setup(t)
	out, errs, code := runAtl(t, "issue", "PROJ-123")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "Epic: PROJ-1 [In Progress] Login epic (Epic)\n")
}

func TestJQL(t *testing.T) {
	testenv.Setup(t)
	out, errs, code := runAtl(t, "jql", "project", "=", "PROJ", "ORDER BY updated DESC", "--limit", "2")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out,
		"- PROJ-123 [In Progress] Fix login · Ann · updated 2026-10-02 10:00\n",
		"- PROJ-7 [Open] API\n",
		"showing 2 of 135; raise --limit or narrow the query",
	)

	out, _, code = runAtl(t, "jql", "project = NONE")
	if code != 0 || !strings.Contains(out, "no issues for: project = NONE") {
		t.Errorf("empty result: exit %d, %s", code, out)
	}

	_, errs, code = runAtl(t, "jql", "project = (")
	if code != cli.ExitUsage || !strings.Contains(errs, "jira: rejected the request: Error in the JQL Query: Expecting ')' before the end of the query.; jql: bad") {
		t.Errorf("bad JQL: exit %d, %s", code, errs)
	}

	out, _, code = runAtl(t, "jql", "project = NONE", "--json")
	if code != 0 || !strings.Contains(out, `"total": 0`) {
		t.Errorf("--json: exit %d, %s", code, out)
	}
	if _, _, code := runAtl(t, "jql"); code != cli.ExitUsage {
		t.Errorf("no query: exit %d", code)
	}
}

func TestEpic(t *testing.T) {
	srv := testenv.Setup(t)
	out, errs, code := runAtl(t, "epic", "PROJ-1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out,
		"# PROJ-1 [In Progress] Login epic (Epic)\n",
		"5 issues: 2 other, 1 done, 1 in progress, 1 to do\n",
		"URL: "+srv.URL+"/jira/browse/PROJ-1\n(details and description: atl issue PROJ-1)\n",
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
	testenv.MustContain(t, out, `"epic": {`, `"children": {`, `"total": 5`)
}

func TestEpicResolution(t *testing.T) {
	testenv.Setup(t)
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
		testenv.MustContain(t, out, "# PROJ-1 [In Progress] Login epic (Epic)")
	}

	out, errs, code := runAtl(t, "epic", "PROJ-5")
	if code != 0 {
		t.Fatalf("PROJ-5: exit %d: %s", code, errs)
	}
	// Jira rejects the Epic Link clause for a non-epic; the parent clause alone still runs.
	testenv.MustContain(t, errs, "atl: PROJ-5 has no epic or parent; listing its own children",
		"atl: Jira rejected the full query (jira: rejected the request: The value 'PROJ-5' does not exist for the field 'Epic Link'.); showing only: parent = PROJ-5\n")
	testenv.MustContain(t, out, "# PROJ-5 [?] Plain\n", "no child issues for: parent = PROJ-5 ORDER BY key\n(details and description: atl issue PROJ-5)\n")

	out, errs, code = runAtl(t, "epic", "PROJ-2")
	if code != 0 || errs != "" {
		t.Fatalf("localized epic: exit %d: %q", code, errs)
	}
	testenv.MustContain(t, out, "# PROJ-2 [?] Billing (Эпик)\n")

	out, errs, _ = runAtl(t, "epic", "PROJ-78")
	testenv.MustContain(t, errs, "atl: PROJ-78 is a Sub-task; showing its parent PROJ-5\n")
	testenv.MustContain(t, out, "# PROJ-5 [?] Plain")

	if _, _, code := runAtl(t, "epic", "PROJ-999"); code != cli.ExitNotFound {
		t.Errorf("missing issue: exit %d", code)
	}
	if _, _, code := runAtl(t, "epic", "A-1", "B-2"); code != cli.ExitUsage {
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
	testenv.WriteConfig(t, fmt.Sprintf("[jira]\nurl = %s\ntoken = x\n", srv.URL), 0o600)
	out, errs, code := runAtl(t, "epic", "ep-1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if seenJQL != "parent = EP-1 ORDER BY key" {
		t.Errorf("jql = %q", seenJQL)
	}
	testenv.MustContain(t, out, "1 issues: 1 done", "- EP-2 [Done] Child")
}

func TestEpicTruncated(t *testing.T) {
	testenv.Setup(t)
	out, errs, code := runAtl(t, "epic", "PROJ-1", "--limit", "1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "5 issues: 1 to do\n", "counts cover the first 1 of 5; raise --limit for all")
}

func TestIssueListsAttachments(t *testing.T) {
	testenv.Setup(t)
	out, errs, code := runAtl(t, "issue", "PROJ-123")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out,
		"## Attachments (2)\n\n- server.log — 21 B · text/plain · Ann · 2026-10-01 09:31 · id 10010\n- ../../.bashrc — 2.0 KB · text/plain · id 10011\n",
		"(download: atl attachment PROJ-123 for all, or atl attachment <id>)",
	)
	out, _, _ = runAtl(t, "issue", "PROJ-5")
	if strings.Contains(out, "## Attachments") {
		t.Errorf("an issue without attachments should have no section:\n%s", out)
	}
}

func fixedNow(t *testing.T, at string) {
	t.Helper()
	ref, err := time.Parse(time.RFC3339, at)
	if err != nil {
		t.Fatal(err)
	}
	old := now
	now = func() time.Time { return ref }
	t.Cleanup(func() { now = old })
}

func TestHistory(t *testing.T) {
	srv := testenv.Setup(t)
	out, errs, code := runAtl(t, "history", "proj-123")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out,
		"# PROJ-123: Fix login — history",
		"Status: In Progress · 4 changes\n",
		"(partial: Jira returned 5 of 7 changes)",
		"URL: "+srv.URL+"/jira/browse/PROJ-123",
		"### 2026-10-02 10:00 Ann\n\n- Status: To Do → In Progress\n- Assignee: (none) → Ann\n- Team Notes: a → b\n",
		"### 2026-09-20 08:00 Bob\n\n- Description set (17 chars)\n    + Line one\n    + Line two\n- Story Points: 3 → 5\n",
		"- Description changed (17 → 26 chars)\n    - Line two\n    + Line 2\n    + Line three\n",
		"- Acceptance Criteria changed (",
		"    … 40 more changed lines",
		"- Environment cleared (was ",
		"### not a date ?\n\n- Labels: (none) → auth",
	)
	// The 250-line environment is too big to diff and gets only its size.
	if strings.Contains(out, "- item 250") {
		t.Errorf("oversized text should not be diffed:\n%s", out)
	}
	if strings.Contains(out, "Rank") || strings.Contains(out, "Dee") {
		t.Errorf("rank-only changes should be dropped:\n%s", out)
	}
	if strings.Index(out, "Bob") > strings.Index(out, "Cy") || strings.Index(out, "Cy") > strings.Index(out, "Ann") {
		t.Errorf("history should be oldest first:\n%s", out)
	}
}

func TestHistorySince(t *testing.T) {
	testenv.Setup(t)
	fixedNow(t, "2026-10-03T00:00:00Z")
	for _, since := range []string{"2026-10-01", "48h", "2d"} {
		out, errs, code := runAtl(t, "history", "PROJ-123", "--since", since)
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", since, code, errs)
		}
		testenv.MustContain(t, out, "Ann", "Cy", "not a date")
		if strings.Contains(out, "Bob") {
			t.Errorf("--since %s kept an older change:\n%s", since, out)
		}
	}
	out, _, _ := runAtl(t, "history", "PROJ-123", "--since", "1w")
	testenv.MustContain(t, out, "since 2026-09-26 ", "3 changes")

	out, _, code := runAtl(t, "history", "PROJ-5", "--since", "0d")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	testenv.MustContain(t, out, "· 0 changes since ", "(no changes since ")
	if strings.Contains(out, "partial") {
		t.Errorf("complete history marked partial:\n%s", out)
	}

	if _, errs, code := runAtl(t, "history", "PROJ-123", "--since", "yesterday"); code != cli.ExitUsage || !strings.Contains(errs, "--since") {
		t.Errorf("bad --since: exit %d, %s", code, errs)
	}
}

func TestHistoryJSONAndErrors(t *testing.T) {
	testenv.Setup(t)
	out, errs, code := runAtl(t, "history", "PROJ-123", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var v struct {
		Changelog struct {
			Total int `json:"total"`
		} `json:"changelog"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.Changelog.Total != 7 {
		t.Errorf("history --json: %v, %+v", err, v)
	}
	if _, _, code := runAtl(t, "history", "PROJ-999"); code != cli.ExitNotFound {
		t.Errorf("missing issue: exit %d", code)
	}
	if _, _, code := runAtl(t, "history", "A-1", "B-2"); code != cli.ExitUsage {
		t.Errorf("two keys: exit %d", code)
	}
	testenv.ClearEnv(t)
	t.Setenv("ATL_CONFIG", filepath.Join(t.TempDir(), "absent.ini"))
	if _, _, code := runAtl(t, "history", "PROJ-123"); code != cli.ExitConfig {
		t.Errorf("no config: exit %d", code)
	}
}

func TestLineDiff(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want []string
	}{
		{"a\nb\nc", "a\nb\nc", nil},
		{"", "x", []string{"+ x"}},
		{"x", "", []string{"- x"}},
		{"a\nb\nc", "a\nc\nd", []string{"- b", "+ d"}},
	} {
		if got := lineDiff(splitLines(tc.a), splitLines(tc.b)); strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("lineDiff(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
	long := strings.Repeat("x", historyLineChars+10)
	if got := longChange("Summary", "", long); !strings.HasSuffix(got[1], "…") {
		t.Errorf("long diff lines should be clipped: %q", got)
	}
}
