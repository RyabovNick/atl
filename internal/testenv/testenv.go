// Package testenv is the shared test fixture: a fake Jira and Confluence
// server, config and environment helpers, and a way to run a command.
package testenv

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/RyabovNick/atl/internal/cli"
)

// Command is the signature of every atl command.
type Command func(args []string, stdout, stderr io.Writer) error

// Run runs the command named by args[0] from cmds and returns its output and exit code.
func Run(t *testing.T, cmds map[string]Command, args ...string) (string, string, int) {
	t.Helper()
	cmd, ok := cmds[args[0]]
	if !ok {
		t.Fatalf("no command %q in this package", args[0])
	}
	var out, errb bytes.Buffer
	code := cli.Report(cmd(args[1:], &out, &errb), &errb)
	return out.String(), errb.String(), code
}

const Token = "test-token"

// FakeServer serves the Jira and Confluence endpoints atl uses, under /jira and /wiki.
func FakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	authorized := func(w http.ResponseWriter, r *http.Request) bool {
		user, pass, basic := r.BasicAuth()
		if r.Header.Get("Authorization") != "Bearer "+Token && (!basic || user != "me" || pass != Token) {
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
		{"id":"customfield_10009","name":"Имя эпика","custom":true,"schema":{"type":"string","custom":"com.pyxis.greenhopper.jira:gh-epic-label"}},
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
	// A localized epic: only its Epic Name says what it is.
	issueReply("PROJ-2", `{"summary":"Billing","issuetype":{"name":"Эпик"},"customfield_10009":"Billing"}`, "")
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
		case `cf[10008] = PROJ-1 OR parent = PROJ-1 ORDER BY key`:
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
		case `cf[10008] = PROJ-2 OR parent = PROJ-2 ORDER BY key`, "parent = PROJ-5 ORDER BY key", "project = NONE":
			fmt.Fprint(w, `{"total":0,"issues":[]}`)
		case `cf[10008] = PROJ-5 OR parent = PROJ-5 ORDER BY key`, `cf[10008] = PROJ-5 ORDER BY key`:
			// What Jira says when an Epic Link clause names an issue that is not an epic.
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"errorMessages":["The value 'PROJ-5' does not exist for the field 'Epic Link'."],"errors":{}}`)
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

	// Page 42 has three versions; status=historical&version=N reads an older one.
	mux.HandleFunc("/wiki/rest/api/content/42", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		body := page42
		if q := r.URL.Query(); q.Get("status") == "historical" {
			if body = page42Versions[q.Get("version")]; body == "" {
				http.NotFound(w, r)
				return
			}
		}
		fmt.Fprint(w, body)
	})
	jsonReply("/wiki/rest/experimental/content/42/version", `{"results":[
		{"number":3,"when":"2026-09-30T12:00:00.000Z","by":{"displayName":"Ann"},"message":"Bold","minorEdit":true},
		{"number":2,"when":"2026-09-20T10:00:00.000Z","by":{"displayName":"Cy"},"message":"Add list"},
		{"number":1,"when":"2026-09-01T09:00:00.000Z","by":{"displayName":"Bob"},"message":""}
	],"start":0,"limit":100,"size":3,"_links":{}}`)
	// A footer thread with a reply listed before an unrelated inline comment, as depth=all can return them.
	reply("/wiki/rest/api/content/42/child/comment", func(r *http.Request) string {
		if q := r.URL.Query(); q.Get("depth") != "all" || !strings.Contains(q.Get("expand"), "body.view") {
			t.Errorf("comments fetched with %s", r.URL.RawQuery)
		}
		return `{"results":[
			{"id":"c1","type":"comment","history":{"createdBy":{"displayName":"Ann"},"createdDate":"2026-10-01T09:00:00.000Z"},
				"ancestors":[],"extensions":{"location":"footer"},"body":{"view":{"value":"<p>Is <em>SSO</em> in scope?</p><h1>Why</h1>"}}},
			{"id":"c2","type":"comment","history":{"createdBy":{"displayName":"Bob"},"createdDate":"2026-10-02T10:00:00.000Z"},
				"extensions":{"location":"inline","inlineProperties":{"originalSelection":"log in"},"resolution":{"status":"resolved"}},
				"ancestors":[],"body":{"view":{"value":"<p>Which IdP?</p>"}}},
			{"id":"c3","type":"comment","history":{"createdBy":{"displayName":"Cy"},"createdDate":"2026-10-03T11:00:00.000Z"},
				"ancestors":[{"id":"c1","type":"comment"}],"extensions":{"location":"footer"},"body":{"view":{"value":"<p>No, next quarter.</p>"}}}
		],"size":3,"_links":{}}`
	})
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
	mux.HandleFunc("/jira/secure/attachment/10016", func(w http.ResponseWriter, _ *http.Request) {
		// Flushing first makes the response chunked, so only the streaming cap can catch it.
		w.(http.Flusher).Flush()
		fmt.Fprint(w, strings.Repeat("x", 4096))
	})
	attMeta("10017", "gated.txt", 3, "BASE/jira/secure/attachment/10017")
	redirect("/jira/secure/attachment/10017", "/login.jsp?os_destination=x")
	attMeta("10018", "gone.txt", 3, "BASE/jira/secure/attachment/10018")
	attMeta("10019", "nolink.txt", 3, "")
	attMeta("10020", "denied.txt", 3, "BASE/jira/secure/attachment/10020")
	mux.HandleFunc("/jira/secure/attachment/10020", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	attMeta("10021", "broken.txt", 3, "BASE/jira/secure/attachment/10021")
	mux.HandleFunc("/jira/secure/attachment/10021", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) })
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
		for i := start; i < min(start+limit, ChildCount); i++ {
			items = append(items, fmt.Sprintf(`{"id":"%d","title":"Child %d","version":{"when":"2026-09-0%dT10:00:00.000Z"},"_links":{"webui":"/spaces/DEV/pages/%d/%%D0%%A1%%D1%%85%%D0%%B5%%D0%%BC%%D0%%B0"}}`, 1000+i, i, 1+i%9, 1000+i))
		}
		next := ""
		if start+limit < ChildCount {
			next = `,"next":"/rest/api/content/77/child/page?start=x"`
		}
		return fmt.Sprintf(`{"results":[%s],"size":%d,"_links":{"base":"BASE"%s}}`, strings.Join(items, ","), len(items), next)
	})
	jsonReply("/wiki/rest/api/content/1000/child/page", `{"results":[{"id":"2000","title":"Grandchild","version":{"when":"2026-09-05T10:00:00.000Z"},"_links":{"webui":"/pages/2000"}}],"size":1,"_links":{}}`)
	// Every other page is a leaf; anything else under content/ stays a 404.
	mux.HandleFunc("/wiki/rest/api/content/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/child/page") && !strings.HasSuffix(r.URL.Path, "/child/comment") {
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
func ClearEnv(t *testing.T) {
	t.Helper()
	for _, svc := range []string{"JIRA", "CONFLUENCE"} {
		for _, v := range []string{"_URL", "_API_TOKEN", "_USERNAME"} {
			t.Setenv(svc+v, "")
		}
	}
}

// WriteConfig writes a config file with mode and points ATL_CONFIG at it.
func WriteConfig(t *testing.T, body string, mode os.FileMode) {
	t.Helper()
	ClearEnv(t)
	path := filepath.Join(t.TempDir(), "config.ini")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATL_CONFIG", path)
}

// Setup starts FakeServer and configures both services against it.
func Setup(t *testing.T) *httptest.Server {
	t.Helper()
	srv := FakeServer(t)
	WriteConfig(t, fmt.Sprintf("# test\n[jira]\nurl = %[1]s/jira/\ntoken = %[2]s\n\n[confluence]\nurl = %[1]s/wiki\ntoken = %[2]s\n", srv.URL, Token), 0o600)
	return srv
}

// MustContain fails the test for each of wants that s lacks.
func MustContain(t *testing.T, s string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Errorf("output lacks %q:\n%s", w, s)
		}
	}
}

// gitRepo makes the test's working directory a fresh repository on branch.
func GitRepo(t *testing.T, branch string) {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q", "-b", branch).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	t.Chdir(dir)
}

const ChildCount = 52

const page42 = `{"id":"42","title":"Login spec","space":{"key":"DEV","name":"Development"},
	"version":{"number":3,"when":"2026-09-30T12:00:00.000Z","by":{"displayName":"Ann"},"message":"Bold"},
	"ancestors":[{"title":"Home"},{"title":"Auth"}],
	"body":{"view":{"value":"<h2>Flow</h2><p>Users <strong>log in</strong>.</p><ul><li>one</li></ul>"}},
	"children":{"comment":{"size":2}},
	"_links":{"webui":"/display/DEV/Login+spec"}}`

// page42Versions are the older versions of page 42, by number.
var page42Versions = map[string]string{
	"1": `{"id":"42","title":"Login","space":{"key":"DEV","name":"Development"},
		"version":{"number":1,"when":"2026-09-01T09:00:00.000Z","by":{"displayName":"Bob"}},
		"body":{"view":{"value":"<h2>Flow</h2><p>Users log in.</p><h2>Old</h2><p>gone</p>"}}}`,
	"2": `{"id":"42","title":"Login spec","space":{"key":"DEV","name":"Development"},
		"version":{"number":2,"when":"2026-09-20T10:00:00.000Z","by":{"displayName":"Cy"},"message":"Add list"},
		"body":{"view":{"value":"<h2>Flow</h2><p>Users log in.</p><ul><li>one</li></ul>"}}}`,
}

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
