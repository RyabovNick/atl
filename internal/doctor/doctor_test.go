package doctor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RyabovNick/atl/internal/cli"
	"github.com/RyabovNick/atl/internal/testenv"
)

func runAtl(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return testenv.Run(t, map[string]testenv.Command{
		"doctor": Run,
	}, args...)
}

func TestDoctorAllOK(t *testing.T) {
	testenv.Setup(t)
	out, errs, code := runAtl(t, "doctor")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, errs, out)
	}
	testenv.MustContain(t, out,
		"(mode 600)",
		"token      set (config file)",
		"username   unset",
		"auth       bearer (default)",
		"timeout    30 (default)",
		"✓ jira: authenticated as Ann (ann, ann@example.com) — Jira 9.12.3 Server",
		"✓ confluence: authenticated as Ann (ann)",
	)
	if strings.Contains(out, testenv.Token) || strings.Contains(out, "  ! ") {
		t.Errorf("unexpected token or warning in output:\n%s", out)
	}
}

func TestDoctorEnvAndOneFailing(t *testing.T) {
	srv := testenv.FakeServer(t)
	testenv.ClearEnv(t)
	t.Setenv("ATL_CONFIG", filepath.Join(t.TempDir(), "absent.ini"))
	const wrong = "wrong-secret-value"
	t.Setenv("JIRA_URL", srv.URL+"/jira")
	t.Setenv("JIRA_API_TOKEN", wrong)
	t.Setenv("CONFLUENCE_URL", srv.URL+"/wiki")
	t.Setenv("CONFLUENCE_API_TOKEN", testenv.Token)
	t.Setenv("CONFLUENCE_USERNAME", "me")
	out, errs, code := runAtl(t, "doctor")
	if code != cli.ExitAPI {
		t.Fatalf("exit %d: %s\n%s", code, errs, out)
	}
	testenv.MustContain(t, out,
		"(not present; environment variables only)",
		"url        "+srv.URL+"/jira (JIRA_URL)",
		"token      set (JIRA_API_TOKEN)",
		"username   me (CONFLUENCE_USERNAME)",
		"auth       basic (default, because a username is set)",
		"✗ jira: ",
		"redirected to /login.jsp",
		"✓ confluence: authenticated as Ann (ann)",
	)
	if strings.Contains(out+errs, wrong) || strings.Contains(out+errs, testenv.Token) {
		t.Errorf("a token leaked into the output:\n%s%s", out, errs)
	}
}

// miniServer answers fixed JSON without checking credentials, for responses
// the shared fake server does not model.
func miniServer(t *testing.T, replies map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range replies {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, body)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestDoctorAnonymousAndBaseURLMismatch(t *testing.T) {
	srv := miniServer(t, map[string]string{
		"/jira/rest/api/2/myself":     `{"name":"ann","displayName":"Ann"}`,
		"/jira/rest/api/2/serverInfo": `{"baseUrl":"https://jira.public.example","version":"9.4.0","deploymentType":"Server"}`,
		"/wiki/rest/api/user/current": `{"type":"anonymous","displayName":"Anonymous"}`,
	})
	testenv.WriteConfig(t, fmt.Sprintf("[jira]\nurl = %[1]s/jira\ntoken = t1\n[confluence]\nurl = %[1]s/wiki\ntoken = t2\n", srv.URL), 0o600)
	out, _, code := runAtl(t, "doctor")
	if code != cli.ExitAPI {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	testenv.MustContain(t, out,
		"✓ jira: authenticated as Ann (ann) — Jira 9.4.0 Server",
		"! Jira's base URL is https://jira.public.example but atl is configured with "+srv.URL+"/jira",
		"✗ confluence: answered as an anonymous user",
	)
}

func TestDoctorWithoutServerInfo(t *testing.T) {
	srv := miniServer(t, map[string]string{"/jira/rest/api/2/myself": `{"key":"JIRAUSER1"}`})
	testenv.WriteConfig(t, fmt.Sprintf("[jira]\nurl = %s/jira\ntoken = t1\n", srv.URL), 0o600)
	out, _, code := runAtl(t, "doctor")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	testenv.MustContain(t, out, "✓ jira: authenticated as JIRAUSER1\n", "! server info unavailable: ", "- confluence: not configured")
}

func TestDoctorConfigProblems(t *testing.T) {
	testenv.ClearEnv(t)
	t.Setenv("ATL_CONFIG", filepath.Join(t.TempDir(), "absent.ini"))
	out, _, code := runAtl(t, "doctor")
	if code != cli.ExitConfig {
		t.Errorf("nothing configured: exit %d", code)
	}
	testenv.MustContain(t, out, "- jira: not configured", "- confluence: not configured", "Nothing is configured")

	testenv.WriteConfig(t, "[jira]\nurl = http://127.0.0.1:1\ntoken = secret-in-file\n", 0o644)
	out, _, code = runAtl(t, "doctor")
	if code != cli.ExitConfig {
		t.Errorf("readable config: exit %d", code)
	}
	testenv.MustContain(t, out, "chmod 600", "✗ jira: ", "✗ confluence: ")
	if strings.Contains(out, "secret-in-file") || strings.Contains(out, "Nothing is configured") {
		t.Errorf("unexpected output:\n%s", out)
	}

	srv := testenv.FakeServer(t)
	testenv.WriteConfig(t, fmt.Sprintf("[confluence]\nurl = %s/wiki\ntoken = %s\n", srv.URL, testenv.Token), 0o600)
	t.Setenv("JIRA_URL", srv.URL+"/jira")
	out, _, code = runAtl(t, "doctor")
	if code != cli.ExitAPI {
		t.Errorf("half-configured jira: exit %d", code)
	}
	testenv.MustContain(t, out, "✗ jira: jira needs a token: set JIRA_API_TOKEN", "token      unset", "✓ confluence")

	if _, _, code := runAtl(t, "doctor", "extra"); code != cli.ExitUsage {
		t.Errorf("doctor with an argument: exit %d", code)
	}
}

func TestDoctorJSON(t *testing.T) {
	testenv.Setup(t)
	out, errs, code := runAtl(t, "doctor", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var report struct {
		ConfigFile fileStatus      `json:"config_file"`
		Services   []serviceReport `json:"services"`
		Exit       int             `json:"exit"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if !report.ConfigFile.Exists || report.ConfigFile.Mode != "600" || len(report.Services) != 2 || report.Exit != 0 {
		t.Errorf("report = %+v", report)
	}
	jira := report.Services[0]
	if !jira.OK || jira.Server == nil || jira.Server.Version != "9.12.3" || jira.User == nil || jira.User.Email != "ann@example.com" {
		t.Errorf("jira = %+v", jira)
	}
	if jira.Settings[1].Key != "token" || jira.Settings[1].Value != "set" || strings.Contains(out, testenv.Token) {
		t.Errorf("token must be reported only as set:\n%s", out)
	}

	testenv.ClearEnv(t)
	t.Setenv("ATL_CONFIG", filepath.Join(t.TempDir(), "absent.ini"))
	out, _, code = runAtl(t, "doctor", "--json")
	if code != cli.ExitConfig || !strings.Contains(out, `"exit": 3`) {
		t.Errorf("nothing configured as JSON: exit %d\n%s", code, out)
	}
}
