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
	"time"
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
	// changelogs feeds expand=changelog, which `history` requests with only summary and status.
	changelogs := map[string]string{}
	// issueReply serves renderedFields only when asked for, as Jira does.
	issueReply := func(key, fields, rendered string) {
		reply("/jira/rest/api/2/issue/"+key, func(r *http.Request) string {
			// `issue` asks for everything; epic lookups name a few fields.
			f, expand := r.URL.Query().Get("fields"), r.URL.Query().Get("expand")
			if f != "*all" && f != "attachment" && !strings.HasPrefix(f, "summary,status") {
				t.Errorf("%s fetched with fields=%q", key, f)
			}
			if f == "*all" && strings.Contains(expand, "changelog") {
				t.Errorf("%s: issue should not download the changelog", key)
			}
			body := `{"key":"` + key + `","fields":` + fields
			if rendered != "" && expand == "renderedFields" {
				body += `,"renderedFields":` + rendered
			}
			if cl, ok := changelogs[key]; ok && expand == "changelog" {
				body += `,"changelog":` + cl
			}
			return body + "}"
		})
	}

	jsonReply("/jira/rest/api/2/field", `[
		{"id":"summary","name":"Summary","custom":false,"schema":{"type":"string"}},
		{"id":"status","name":"Status","custom":false,"schema":{"type":"status"}},
		{"id":"assignee","name":"Assignee","custom":false,"schema":{"type":"user"}},
		{"id":"description","name":"Description","custom":false,"schema":{"type":"string"}},
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
		"attachment":[
			{"id":"10010","filename":"server.log","size":21,"mimeType":"text/plain","author":{"displayName":"Ann"},"created":"2026-10-01T09:31:00.000+0000","content":"BASE/jira/secure/attachment/10010/server.log"},
			{"id":"10011","filename":"../../.bashrc","size":2048,"mimeType":"text/plain","content":"BASE/jira/secure/attachment/10011/x"}
		],
		"comment":{"comments":[{"id":"501","author":{"displayName":"Cy"},"created":"2026-10-03T08:00:00.000+0000","body":"Also BASE/wiki/x/KgAAAA."}]}
	}`, `{
		"description":"<p>See <a href=\"BASE/wiki/display/DEV/Login+Flow\">Login Flow</a>.</p><h1>Steps</h1><ul><li>open page</li></ul>",
		"customfield_10011":"<ul><li>Login <b>works</b></li><li>Logout works</li></ul>",
		"customfield_10010":"5",
		"comment":{"comments":[{"id":"501","body":"<p>Also <em>this</em>.</p><h2>Note</h2><p>n</p>"}]}
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
	numbered := func(n int, suffix string) string {
		lines := make([]string, n)
		for i := range lines {
			lines[i] = fmt.Sprintf("item %d%s", i+1, suffix)
		}
		return strings.Join(lines, `\n`)
	}
	changelogs["PROJ-123"] = `{"startAt":0,"maxResults":5,"total":7,"histories":[
		{"author":{"displayName":"Ann"},"created":"2026-10-02T10:00:00.000+0000","items":[
			{"field":"status","fieldtype":"jira","fromString":"To Do","toString":"In Progress"},
			{"field":"assignee","fieldtype":"jira","fromString":null,"toString":"Ann"},
			{"field":"Team Notes","fieldtype":"custom","fromString":"a","toString":"b"},
			{"field":"Rank","fieldtype":"custom","fieldId":"customfield_10013","fromString":"","toString":"Ranked higher"}]},
		{"author":{"displayName":"Bob"},"created":"2026-09-20T08:00:00.000+0000","items":[
			{"field":"description","fieldtype":"jira","fromString":null,"toString":"Line one\nLine two"},
			{"field":"Story Points","fieldtype":"custom","fromString":"3","toString":"5"}]},
		{"author":{"displayName":"Cy"},"created":"2026-10-01T12:00:00.000+0000","items":[
			{"field":"description","fieldtype":"jira","fromString":"Line one\nLine two","toString":"Line one\nLine 2\nLine three"},
			{"field":"Acceptance Criteria","fieldtype":"custom","fromString":"` + numbered(30, "") + `","toString":"` + numbered(30, " v2") + `"},
			{"field":"environment","fieldtype":"jira","fromString":"` + numbered(250, "") + `","toString":""}]},
		{"author":{"displayName":"Dee"},"created":"2026-10-01T13:00:00.000+0000","items":[
			{"field":"Rank","fieldtype":"custom","fromString":"","toString":"Ranked lower"}]},
		{"created":"not a date","items":[{"field":"labels","fieldtype":"jira","fromString":"","toString":"auth"}]}
	]}`
	changelogs["PROJ-5"] = `{"startAt":0,"maxResults":0,"total":0,"histories":[]}`
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
	file := func(path, ctype, body string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if authorized(w, r) {
				w.Header().Set("Content-Type", ctype)
				fmt.Fprint(w, body)
			}
		})
	}
	redirect := func(path, to string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if authorized(w, r) {
				http.Redirect(w, r, strings.ReplaceAll(to, "BASE", srv.URL), http.StatusFound)
			}
		})
	}
	attMeta := func(id, name string, size int, content string) {
		jsonReply("/jira/rest/api/2/attachment/"+id, fmt.Sprintf(`{"id":%q,"filename":%q,"size":%d,"mimeType":"text/plain","content":%q}`, id, name, size, content))
	}
	file("/jira/secure/attachment/10010/server.log", "text/plain", "java.lang.NullPointer")
	file("/jira/secure/attachment/10011/x", "text/plain", "alias ls=rm")
	attMeta("10010", "server.log", 21, "BASE/jira/secure/attachment/10010/server.log")
	attMeta("10012", "huge.bin", 999999999, "BASE/jira/secure/attachment/10012/huge.bin")
	attMeta("10013", "elsewhere.txt", 3, "https://files.example.com/10013")
	attMeta("10014", "moved.txt", 5, "BASE/jira/secure/attachment/10014/")
	redirect("/jira/secure/attachment/10014/", "BASE/jira/secure/attachment/10014/real.txt")
	file("/jira/secure/attachment/10014/real.txt", "text/plain", "moved")
	attMeta("10015", "away.txt", 3, "BASE/jira/secure/attachment/10015")
	redirect("/jira/secure/attachment/10015", "http://other.invalid/x")
	attMeta("10016", "liar.txt", 3, "BASE/jira/secure/attachment/10016")
	mux.HandleFunc("/jira/secure/attachment/10016", func(w http.ResponseWriter, r *http.Request) {
		// Flushing first makes the response chunked, so only the streaming cap can catch it.
		w.(http.Flusher).Flush()
		fmt.Fprint(w, strings.Repeat("x", 4096))
	})
	attMeta("10017", "gated.txt", 3, "BASE/jira/secure/attachment/10017")
	redirect("/jira/secure/attachment/10017", "/login.jsp?os_destination=x")
	attMeta("10018", "gone.txt", 3, "BASE/jira/secure/attachment/10018")
	attMeta("10019", "nolink.txt", 3, "")
	attMeta("10020", "denied.txt", 3, "BASE/jira/secure/attachment/10020")
	mux.HandleFunc("/jira/secure/attachment/10020", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	attMeta("10021", "broken.txt", 3, "BASE/jira/secure/attachment/10021")
	mux.HandleFunc("/jira/secure/attachment/10021", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	attMeta("10022", "relative.txt", 3, "secure/attachment/10022")

	diagram := `{"id":"att900","title":"diagram.png","extensions":{"mediaType":"image/png","fileSize":4},
		"version":{"when":"2026-09-30T12:00:00.000Z","by":{"displayName":"Ann"}},
		"_links":{"download":"/download/attachments/42/diagram.png?version=1&api=v2"}}`
	jsonReply("/wiki/rest/api/content/42/child/attachment", `{"results":[`+diagram+`],"size":1}`)
	jsonReply("/wiki/rest/api/content/att900", diagram)
	file("/wiki/download/attachments/42/diagram.png", "image/png", "\x89PNG")
	jsonReply("/wiki/rest/api/content/77", `{"id":"77","title":"Design","space":{"key":"DEV","name":"Development"},
		"version":{"number":1,"when":"2026-09-01T08:00:00.000Z"},"_links":{"webui":"/display/DEV/Design"},
		"body":{"view":{"value":"`+outlineHTML+`"}}}`)
	jsonReply("/wiki/rest/api/content/77/child/attachment", `{"results":[],"size":0}`)
	// 77 has more children than one batch, so listing them takes two requests.
	reply("/wiki/rest/api/content/77/child/page", func(r *http.Request) string {
		start, _ := strconv.Atoi(r.URL.Query().Get("start"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		var items []string
		for i := start; i < min(start+limit, childCount); i++ {
			items = append(items, fmt.Sprintf(`{"id":"%d","title":"Child %d","version":{"when":"2026-09-0%dT10:00:00.000Z"},"_links":{"webui":"/pages/%d"}}`, 1000+i, i, 1+i%9, 1000+i))
		}
		next := ""
		if start+limit < childCount {
			next = `,"next":"/rest/api/content/77/child/page?start=x"`
		}
		return fmt.Sprintf(`{"results":[%s],"size":%d,"_links":{"base":"BASE"%s}}`, strings.Join(items, ","), len(items), next)
	})
	jsonReply("/wiki/rest/api/content/1000/child/page", `{"results":[{"id":"2000","title":"Grandchild","version":{"when":"2026-09-05T10:00:00.000Z"},"_links":{"webui":"/pages/2000"}}],"size":1,"_links":{}}`)
	// Every other page is a leaf; anything else under content/ stays a 404.
	mux.HandleFunc("/wiki/rest/api/content/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/child/page") {
			http.NotFound(w, r)
		} else if authorized(w, r) {
			fmt.Fprint(w, `{"results":[],"size":0,"_links":{}}`)
		}
	})
	jsonReply("/jira/rest/api/2/myself", `{"name":"ann","key":"ann","displayName":"Ann","emailAddress":"ann@example.com"}`)
	jsonReply("/jira/rest/api/2/serverInfo", `{"baseUrl":"BASE/jira","version":"9.12.3","deploymentType":"Server"}`)
	jsonReply("/wiki/rest/api/user/current", `{"type":"known","username":"ann","userKey":"k1","displayName":"Ann"}`)
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
		srv.URL + "/wiki/display/DEV/2026/09/30/Login+Flow", // blog post URL
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

func TestIssueListsAttachments(t *testing.T) {
	setup(t)
	out, errs, code := runAtl(t, "issue", "PROJ-123")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out,
		"## Attachments (2)\n\n- server.log — 21 B · text/plain · Ann · 2026-10-01 09:31 · id 10010\n- ../../.bashrc — 2.0 KB · text/plain · id 10011\n",
		"(download: atl attachment PROJ-123 for all, or atl attachment <id>)",
	)
	out, _, _ = runAtl(t, "issue", "PROJ-5")
	if strings.Contains(out, "## Attachments") {
		t.Errorf("an issue without attachments should have no section:\n%s", out)
	}
}

func TestPageListsAttachments(t *testing.T) {
	setup(t)
	out, errs, code := runAtl(t, "page", "42")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out, "## Attachments (1)\n\n- diagram.png — 4 B · image/png · Ann · 2026-09-30 12:00 · id att900\n", "(download: atl attachment <id>)")
}

func TestAttachmentDownloadIssue(t *testing.T) {
	setup(t)
	dir := filepath.Join(t.TempDir(), "out")
	out, errs, code := runAtl(t, "attachment", "proj-123", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	log, hidden := filepath.Join(dir, "server.log"), filepath.Join(dir, "_.._.bashrc")
	mustContain(t, out, "Saved 2 of 2 to "+dir+":\n", "- "+log+" (21 B, text/plain)\n", "- "+hidden+" (11 B, text/plain)\n")
	for path, want := range map[string]string{log: "java.lang.NullPointer", hidden: "alias ls=rm"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v", path, got, err)
		}
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v, want 0600", path, info.Mode().Perm())
		}
	}

	// A second download keeps the first copy and writes beside it.
	out, _, code = runAtl(t, "attachment", "10010", "--dir", dir)
	mustContain(t, out, "- "+filepath.Join(dir, "server-1.log")+" (21 B, text/plain)")
	if code != 0 {
		t.Errorf("second download exit %d", code)
	}
	if got, _ := os.ReadFile(log); string(got) != "java.lang.NullPointer" {
		t.Errorf("first copy overwritten: %q", got)
	}
}

func TestAttachmentDefaultDirAndJSON(t *testing.T) {
	setup(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	out, errs, code := runAtl(t, "attachment", "10010", "att900", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var saved []savedFile
	if err := json.Unmarshal([]byte(out), &saved); err != nil || len(saved) != 2 {
		t.Fatalf("json %v: %s", err, out)
	}
	for _, s := range saved {
		if !strings.HasPrefix(s.Path, filepath.Join(tmp, "atl-attachments-10010-")) {
			t.Errorf("%s saved outside a fresh temp dir", s.Path)
		}
	}
	if saved[1].Source != "confluence" || saved[1].Filename != "diagram.png" || saved[1].Bytes != 4 || saved[1].MimeType != "image/png" {
		t.Errorf("confluence attachment: %+v", saved[1])
	}
	if got, _ := os.ReadFile(saved[1].Path); string(got) != "\x89PNG" {
		t.Errorf("diagram content %q", got)
	}
}

func TestAttachmentFromBranch(t *testing.T) {
	setup(t)
	gitRepo(t, "feature/PROJ-123-attachments")
	out, errs, code := runAtl(t, "attachment", "--dir", t.TempDir())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, errs, "PROJ-123 from branch")
	mustContain(t, out, "Saved 2 of 2")
}

func TestAttachmentSameHostRedirect(t *testing.T) {
	setup(t)
	dir := t.TempDir()
	out, errs, code := runAtl(t, "attachment", "10014", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "moved.txt")); string(got) != "moved" {
		t.Errorf("redirected download %q:\n%s", got, out)
	}
}

func TestAttachmentRefusals(t *testing.T) {
	setup(t)
	for _, tc := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"10012"}, exitAPI, "over the 50.0 MB limit; raise --max-bytes"},
		{[]string{"10013"}, exitAPI, "refusing to send the token to https://files.example.com"},
		{[]string{"10015"}, exitAPI, "refusing redirect to another host, other.invalid"},
		{[]string{"10016", "--max-bytes", "100"}, exitAPI, "over the 100-byte limit"},
		{[]string{"10010", "--max-bytes", "10"}, exitAPI, "over the 10 B limit"},
		{[]string{"10017"}, exitAPI, "redirected to the login page"},
		{[]string{"10018"}, exitAPI, "not found: /jira/secure/attachment/10018"},
		{[]string{"10019"}, exitAPI, "nolink.txt has no download link"},
		{[]string{"10020"}, exitAPI, "HTTP 403 downloading /jira/secure/attachment/10020; check the token"},
		{[]string{"10021"}, exitAPI, "HTTP 502 downloading"},
		{[]string{"10022"}, exitAPI, `bad download link "secure/attachment/10022"`},
		{[]string{"99999"}, exitNotFound, "not found: /rest/api/2/attachment/99999"},
		{[]string{"PROJ-123", "10010"}, exitUsage, "neither an issue key"},
		{[]string{"10010", "--max-bytes", "0"}, exitUsage, "--max-bytes must be positive"},
	} {
		dir := t.TempDir()
		_, errs, code := runAtl(t, append([]string{"attachment", "--dir", dir}, tc.args...)...)
		if code != tc.code || !strings.Contains(errs, tc.msg) {
			t.Errorf("%v: exit %d, want %d with %q:\n%s", tc.args, code, tc.code, tc.msg, errs)
		}
		// A refused or failed download must not leave a partial file behind.
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%v left %d files in %s", tc.args, len(entries), dir)
		}
	}

	out, _, code := runAtl(t, "attachment", "PROJ-5", "--dir", t.TempDir())
	if code != 0 || out != "PROJ-5 has no attachments\n" {
		t.Errorf("no attachments: exit %d, %q", code, out)
	}
	// One failure among several downloads still saves the rest and exits non-zero.
	out, errs, code := runAtl(t, "attachment", "10010", "10013", "--dir", t.TempDir())
	if code != exitAPI || !strings.Contains(out, "Saved 1 of 2") || !strings.Contains(errs, "1 of 2 downloads failed: 10013") {
		t.Errorf("partial failure: exit %d\n%s\n%s", code, out, errs)
	}
	if _, errs, code := runAtl(t, "attachment", "10010", "--dir", "/proc/nope/x"); code != exitUsage || !strings.Contains(errs, "--dir") {
		t.Errorf("bad --dir: exit %d, %s", code, errs)
	}
}

func TestSafeFilename(t *testing.T) {
	long := strings.Repeat("я", 150) + ".txt"
	for in, want := range map[string]string{
		"report.pdf":       "report.pdf",
		"../../etc/passwd": "_.._etc_passwd",
		"..":               "fallback",
		".hidden":          "hidden",
		"  . spaced .txt ": "spaced .txt",
		"a\\b\x00c\nd.log": "a_bcd.log",
		"":                 "fallback",
		long:               strings.Repeat("я", 98) + ".txt",
	} {
		if got := safeFilename(in, "fallback"); got != want {
			t.Errorf("safeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 5 << 20: "5.0 MB", 3 << 30: "3.0 GB"} {
		if got := humanSize(n); got != want {
			t.Errorf("humanSize(%d) = %s, want %s", n, got, want)
		}
	}
}

// gitRepo makes the test's working directory a fresh repository on branch.
func gitRepo(t *testing.T, branch string) {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q", "-b", branch).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	t.Chdir(dir)
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
	srv := setup(t)
	out, errs, code := runAtl(t, "history", "proj-123")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out,
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
	setup(t)
	fixedNow(t, "2026-10-03T00:00:00Z")
	for _, since := range []string{"2026-10-01", "48h", "2d"} {
		out, errs, code := runAtl(t, "history", "PROJ-123", "--since", since)
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", since, code, errs)
		}
		mustContain(t, out, "Ann", "Cy", "not a date")
		if strings.Contains(out, "Bob") {
			t.Errorf("--since %s kept an older change:\n%s", since, out)
		}
	}
	out, _, _ := runAtl(t, "history", "PROJ-123", "--since", "1w")
	mustContain(t, out, "since 2026-09-26 ", "3 changes")

	out, _, code := runAtl(t, "history", "PROJ-5", "--since", "0d")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	mustContain(t, out, "· 0 changes since ", "(no changes since ")
	if strings.Contains(out, "partial") {
		t.Errorf("complete history marked partial:\n%s", out)
	}

	if _, errs, code := runAtl(t, "history", "PROJ-123", "--since", "yesterday"); code != exitUsage || !strings.Contains(errs, "--since") {
		t.Errorf("bad --since: exit %d, %s", code, errs)
	}
}

func TestHistoryJSONAndErrors(t *testing.T) {
	setup(t)
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
	if _, _, code := runAtl(t, "history", "PROJ-999"); code != exitNotFound {
		t.Errorf("missing issue: exit %d", code)
	}
	if _, _, code := runAtl(t, "history", "A-1", "B-2"); code != exitUsage {
		t.Errorf("two keys: exit %d", code)
	}
	clearEnv(t)
	t.Setenv("ATL_CONFIG", filepath.Join(t.TempDir(), "absent.ini"))
	if _, _, code := runAtl(t, "history", "PROJ-123"); code != exitConfig {
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

const childCount = 52

// outlineHTML is a page with nested, repeated and decoy headings: a '#' inside
// fenced code and a heading inside a table cell are content, not structure.
var outlineHTML = strings.NewReplacer(`"`, `\"`).Replace(
	`<p>Intro text.</p>` +
		`<h1>Overview</h1><p>Why this exists.</p>` +
		`<h2>Goals</h2><p>Fast login.</p>` +
		`<h3>Non-goals</h3><p>SSO.</p>` +
		`<h2>Notes</h2><p>First notes.</p>` +
		`<h1>Implementation details</h1><pre><code># not a heading\nrun()</code></pre>` +
		`<table><tr><td><h2>Cell heading</h2></td></tr></table>` +
		`<h2>Notes</h2><p>Second notes.</p>` +
		`<h1>Implementation plan</h1><p>Phase 1.</p>`)

func TestPageDemotesHeadings(t *testing.T) {
	setup(t)
	out, errs, code := runAtl(t, "page", "77")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out, "# Design\n", "\n## Overview\n", "\n### Goals\n", "\n#### Non-goals\n", "# not a heading",
		"| **Cell heading** |")
	if strings.Contains(out, "\n# Overview") {
		t.Errorf("page h1 should nest under the title:\n%s", out)
	}

	out, _, _ = runAtl(t, "issue", "PROJ-123", "--comments")
	mustContain(t, out, "\n### Steps\n", "\n#### Note\n")
}

func TestPageTOC(t *testing.T) {
	setup(t)
	out, errs, code := runAtl(t, "page", "77", "--toc")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out, "# Design", "## Contents (7 headings,", "- (before the first heading) — 11 chars",
		"\n- Overview — ", "\n  - Goals — ", "\n    - Non-goals — ", "\n- Implementation details — ",
		`atl page 77 --section "<heading>"`)
	if strings.Contains(out, "Why this exists") || strings.Contains(out, "not a heading —") || strings.Contains(out, "Cell heading —") {
		t.Errorf("toc should hold headings only, without decoys:\n%s", out)
	}

	lines := strings.Split(demoteHeadings("intro\n## A\naaaa\n### B\nbb\n## C\nc", 2), "\n")
	var b strings.Builder
	writeTOC(&b, lines, scanHeadings(lines))
	mustContain(t, b.String(), "- (before the first heading) — 5 chars\n- A — 18 chars\n  - B — 8 chars\n- C — 6 chars")

	b.Reset()
	writeTOC(&b, []string{"no headings"}, nil)
	mustContain(t, b.String(), "(no headings; read the whole page)")
}

func TestPageSection(t *testing.T) {
	setup(t)
	section := func(args ...string) (string, string, int) {
		return runAtl(t, append([]string{"page", "77"}, args...)...)
	}

	out, errs, code := section("--section", "overview")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out, "# Design", "## Overview\n\nWhy this exists.", "### Goals", "#### Non-goals\n\nSSO.", "First notes.")
	for _, absent := range []string{"Intro text", "Implementation", "Second notes", "## Attachments"} {
		if strings.Contains(out, absent) {
			t.Errorf("section should stop at the next same-level heading, found %q:\n%s", absent, out)
		}
	}

	out, _, _ = section("--section", "Notes")
	mustContain(t, out, "First notes.", "Second notes.")

	out, _, code = section("--section", "non", "--section", "Implementation p")
	if code != 0 {
		t.Fatalf("prefix sections: exit %d", code)
	}
	mustContain(t, out, "SSO.", "Phase 1.")
	if strings.Index(out, "SSO.") > strings.Index(out, "Phase 1.") {
		t.Errorf("sections should print in the order asked:\n%s", out)
	}

	out, _, _ = section("--section", "goals")
	mustContain(t, out, "Fast login.")
	if strings.Contains(out, "Why this exists") {
		t.Errorf("exact match should win over substring:\n%s", out)
	}

	if _, _, code := section("--section", "phase"); code != exitNotFound {
		t.Errorf("sections match titles, not body text: exit %d", code)
	}

	if _, errs, code := section("--section", "Implementation"); code != exitUsage || !strings.Contains(errs, "ambiguous") ||
		!strings.Contains(errs, "Implementation details") || !strings.Contains(errs, "Implementation plan") {
		t.Errorf("ambiguous prefix: exit %d, %s", code, errs)
	}
	if _, errs, code := section("--section", "tion"); code != exitUsage || !strings.Contains(errs, "ambiguous") {
		t.Errorf("ambiguous substring: exit %d, %s", code, errs)
	}
	if _, errs, code := section("--section", "Rollout"); code != exitNotFound || !strings.Contains(errs, "headings are:\n  Overview\n    Goals") {
		t.Errorf("missing section: exit %d, %s", code, errs)
	}
	if _, errs, code := runAtl(t, "page", "42", "--section", "Nope", "--section", "flow"); code != exitNotFound || !strings.Contains(errs, "Flow") {
		t.Errorf("missing section on another page: exit %d, %s", code, errs)
	}
	if _, err := findSections(nil, "x"); err == nil || !strings.Contains(err.Error(), "no headings") {
		t.Errorf("headingless page: %v", err)
	}

	out, _, _ = section("--section", "overview", "--max-chars", "10")
	mustContain(t, out, "[truncated at 10 of")

	out, _, _ = section("--toc", "--section", "Implementation plan")
	mustContain(t, out, "## Contents", "Phase 1.")
	if strings.Contains(out, "## Attachments") {
		t.Errorf("--section output should skip attachments:\n%s", out)
	}

	if _, errs, code := section("--toc", "--json"); code != exitUsage || !strings.Contains(errs, "--json") {
		t.Errorf("--toc --json: exit %d, %s", code, errs)
	}
}

func TestPageChildren(t *testing.T) {
	srv := setup(t)
	out, errs, code := runAtl(t, "page", "77", "--children")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	mustContain(t, out, "# Design", "## Child pages (52)", "- Child 0 — id 1000 · updated 2026-09-01 10:00\n  "+srv.URL+"/wiki/pages/1000", "- Child 51 — id 1051")
	if strings.Contains(out, "Why this exists") || strings.Contains(out, "Grandchild") || strings.Contains(out, "## Attachments") {
		t.Errorf("--children alone lists one level and no body:\n%s", out)
	}

	out, _, _ = runAtl(t, "page", "77", "--children", "--depth", "2")
	mustContain(t, out, "## Child pages (53)", "- Child 0 — id 1000", "\n  - Grandchild — id 2000 · updated 2026-09-05 10:00\n    "+srv.URL+"/wiki/pages/2000")

	out, _, _ = runAtl(t, "page", "77", "--children", "--toc")
	mustContain(t, out, "## Contents", "\n\n## Child pages (52)")
	if strings.Count(out, "# Design") != 1 || strings.Contains(out, "Why this exists") {
		t.Errorf("--children --toc prints the header once and no body:\n%s", out)
	}

	out, _, _ = runAtl(t, "page", "42", "--children")
	mustContain(t, out, "## Child pages (0)\n\n(none)")

	saved := maxChildPages
	maxChildPages = 51
	t.Cleanup(func() { maxChildPages = saved })
	out, _, _ = runAtl(t, "page", "77", "--children", "--depth", "3")
	mustContain(t, out, "## Child pages (51)", "(stopped at 51 pages")
	maxChildPages = 50
	out, _, _ = runAtl(t, "page", "77", "--children")
	mustContain(t, out, "## Child pages (50)", "(stopped at 50 pages")
	maxChildPages = saved

	out, errs, code = runAtl(t, "page", "77", "--children", "--depth", "2", "--json")
	if code != 0 {
		t.Fatalf("json: exit %d: %s", code, errs)
	}
	var got struct {
		Page struct {
			Title string `json:"title"`
		} `json:"page"`
		Children []struct {
			ID       string `json:"id"`
			Children []struct {
				Title string `json:"title"`
			} `json:"children"`
		} `json:"children"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if got.Page.Title != "Design" || len(got.Children) != childCount || got.Children[0].Children[0].Title != "Grandchild" || got.Truncated {
		t.Errorf("json tree: %+v", got)
	}
	out, _, _ = runAtl(t, "page", "42", "--children", "--json")
	mustContain(t, out, `"children": []`)

	for _, d := range []string{"0", "4"} {
		if _, errs, code := runAtl(t, "page", "77", "--children", "--depth", d); code != exitUsage || !strings.Contains(errs, "--depth") {
			t.Errorf("depth %s: exit %d, %s", d, code, errs)
		}
	}
}

func TestDemoteHeadings(t *testing.T) {
	for _, tc := range []struct {
		in   string
		min  int
		want string
	}{
		{"# A\n## B", 3, "### A\n#### B"},
		{"### A\n#### B", 2, "### A\n#### B"},
		{"# A\n###### F", 3, "### A\n###### F"},
		{"no headings", 3, "no headings"},
		{"```\n# code\n```\n# Real", 2, "```\n# code\n```\n## Real"},
		{"~~~~\n# code\n~~~\n# still code\n~~~~\n#Tag", 2, "~~~~\n# code\n~~~\n# still code\n~~~~\n#Tag"},
		{"  # indented\n- # in list", 2, "  # indented\n- # in list"},
	} {
		if got := demoteHeadings(tc.in, tc.min); got != tc.want {
			t.Errorf("demoteHeadings(%q, %d) = %q, want %q", tc.in, tc.min, got, tc.want)
		}
	}
	if got := plainTitle(`**Bold** [link](http://x) \_under\_ ` + "`code`" + ` ##`); got != "Bold link _under_ code ##" {
		t.Errorf("plainTitle = %q", got)
	}
	hs := scanHeadings(strings.Split("## Closed ##\ntext", "\n"))
	if len(hs) != 1 || hs[0].title != "Closed" {
		t.Errorf("closing hashes: %+v", hs)
	}
	md, err := htmlToMarkdown(`<table><tr><th>Name</th></tr><tr><td><h2>Cell</h2><p>a</p><p>b</p></td></tr></table><p>x <del>y</del></p>`)
	if err != nil || md != "| Name |\n|---|\n| **Cell**<br>a<br>b |\n\nx ~~y~~" {
		t.Errorf("htmlToMarkdown table = %q, %v", md, err)
	}
	var f stringsFlag
	_ = f.Set("a")
	_ = f.Set("b")
	if f.String() != "a, b" {
		t.Errorf("stringsFlag = %q", f.String())
	}
}
