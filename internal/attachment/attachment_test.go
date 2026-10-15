package attachment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RyabovNick/atl/internal/cli"
	"github.com/RyabovNick/atl/internal/testenv"
)

func runAtl(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return testenv.Run(t, map[string]testenv.Command{
		"attachment": Run,
	}, args...)
}

func TestAttachmentDownloadIssue(t *testing.T) {
	testenv.Setup(t)
	dir := filepath.Join(t.TempDir(), "out")
	out, errs, code := runAtl(t, "attachment", "proj-123", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	log, hidden := filepath.Join(dir, "server.log"), filepath.Join(dir, "_.._.bashrc")
	testenv.MustContain(t, out, "Saved 2 of 2 to "+dir+":\n", "- "+log+" (21 B, text/plain)\n", "- "+hidden+" (11 B, text/plain)\n")
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
	testenv.MustContain(t, out, "- "+filepath.Join(dir, "server-1.log")+" (21 B, text/plain)")
	if code != 0 {
		t.Errorf("second download exit %d", code)
	}
	if got, _ := os.ReadFile(log); string(got) != "java.lang.NullPointer" {
		t.Errorf("first copy overwritten: %q", got)
	}
}

func TestAttachmentDefaultDirAndJSON(t *testing.T) {
	testenv.Setup(t)
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
	testenv.Setup(t)
	testenv.GitRepo(t, "feature/PROJ-123-attachments")
	out, errs, code := runAtl(t, "attachment", "--dir", t.TempDir())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, errs, "PROJ-123 from branch")
	testenv.MustContain(t, out, "Saved 2 of 2")
}

func TestAttachmentSameHostRedirect(t *testing.T) {
	testenv.Setup(t)
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
	testenv.Setup(t)
	for _, tc := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"10012"}, cli.ExitAPI, "over the 50.0 MB limit; raise --max-bytes"},
		{[]string{"10013"}, cli.ExitAPI, "refusing to send the token to https://files.example.com"},
		{[]string{"10015"}, cli.ExitAPI, "refusing redirect to another host, other.invalid"},
		{[]string{"10016", "--max-bytes", "100"}, cli.ExitAPI, "over the 100-byte limit"},
		{[]string{"10010", "--max-bytes", "10"}, cli.ExitAPI, "over the 10 B limit"},
		{[]string{"10017"}, cli.ExitAPI, "redirected to the login page"},
		{[]string{"10018"}, cli.ExitAPI, "not found: /jira/secure/attachment/10018"},
		{[]string{"10019"}, cli.ExitAPI, "nolink.txt has no download link"},
		{[]string{"10020"}, cli.ExitAPI, "HTTP 403 downloading /jira/secure/attachment/10020; check the token"},
		{[]string{"10021"}, cli.ExitAPI, "HTTP 502 downloading"},
		{[]string{"10022"}, cli.ExitAPI, `bad download link "secure/attachment/10022"`},
		{[]string{"99999"}, cli.ExitNotFound, "not found: /rest/api/2/attachment/99999"},
		{[]string{"PROJ-123", "10010"}, cli.ExitUsage, "neither an issue key"},
		{[]string{"10010", "--max-bytes", "0"}, cli.ExitUsage, "--max-bytes must be positive"},
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
	if code != cli.ExitAPI || !strings.Contains(out, "Saved 1 of 2") || !strings.Contains(errs, "1 of 2 downloads failed: 10013") {
		t.Errorf("partial failure: exit %d\n%s\n%s", code, out, errs)
	}
	if _, errs, code := runAtl(t, "attachment", "10010", "--dir", "/proc/nope/x"); code != cli.ExitUsage || !strings.Contains(errs, "--dir") {
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
