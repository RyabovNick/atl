package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RyabovNick/atl/internal/cli"
	"github.com/RyabovNick/atl/internal/testenv"
	"github.com/RyabovNick/atl/skill"
)

func runAtl(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return testenv.Run(t, map[string]testenv.Command{
		"install":   Run,
		"uninstall": RunUninstall,
	}, args...)
}

type installFixture struct {
	home, oc, claude, codex, bin, src string
}

func (f installFixture) agents() string { return filepath.Join(f.oc, "AGENTS.md") }

func (f installFixture) skill() string {
	return filepath.Join(f.home, ".agents", "skills", "atlassian", "SKILL.md")
}

func (f installFixture) claudeSkill() string {
	return filepath.Join(f.claude, "skills", "atlassian", "SKILL.md")
}

func newInstallFixture(t *testing.T) installFixture {
	t.Helper()
	testenv.ClearEnv(t)
	home := t.TempDir()
	f := installFixture{
		home: home, oc: filepath.Join(home, "oc"), claude: filepath.Join(home, "claude"), codex: filepath.Join(home, "codex"),
		bin: filepath.Join(home, "bin"), src: filepath.Join(home, "build", "atl"),
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("OPENCODE_CONFIG_DIR", f.oc)
	t.Setenv("CLAUDE_CONFIG_DIR", f.claude)
	t.Setenv("CODEX_HOME", f.codex)
	// Only opencode is installed, the agent set up by default.
	if err := os.MkdirAll(f.oc, 0o755); err != nil {
		t.Fatal(err)
	}
	oldTerm := openTerminal
	openTerminal = func() (*os.File, *os.File, func(), error) { return nil, nil, nil, errors.New("no terminal") }
	t.Cleanup(func() { openTerminal = oldTerm })
	t.Setenv("ATL_CONFIG", filepath.Join(home, "absent.ini"))
	t.Setenv("PATH", "/usr/bin")
	if err := os.MkdirAll(filepath.Dir(f.src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.src, []byte("fake atl binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := executable
	executable = func() (string, error) { return f.src, nil }
	t.Cleanup(func() { executable = old })
	return f
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func expectedBlock(nl string) string {
	body := strings.TrimRight(skill.Instructions, "\n")
	return agentsBegin + nl + strings.ReplaceAll(body, "\n", nl) + nl + agentsEnd
}

func TestInstallFreshAndIdempotent(t *testing.T) {
	f := newInstallFixture(t)
	out, errs, code := runAtl(t, "install", "--bin-dir", f.bin)
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, errs, out)
	}
	testenv.MustContain(t, out,
		"Installed atl dev:",
		"binary       "+filepath.Join(f.bin, "atl")+" (created)",
		"skill        "+f.skill()+" (created)",
		"opencode     "+f.agents()+" (created)",
		"! "+f.bin+" is not on PATH",
		`export PATH="$HOME/bin:$PATH"`,
		"Set JIRA_URL and JIRA_API_TOKEN and CONFLUENCE_URL and CONFLUENCE_API_TOKEN",
		"atl doctor",
		"leave CONFLUENCE_USERNAME unset",
	)
	if got := readFile(t, filepath.Join(f.bin, "atl")); got != "fake atl binary" {
		t.Errorf("binary = %q", got)
	}
	if info, _ := os.Stat(filepath.Join(f.bin, "atl")); info.Mode().Perm() != 0o755 {
		t.Errorf("binary mode = %v", info.Mode().Perm())
	}
	if readFile(t, f.skill()) != string(skill.Skill) {
		t.Error("skill differs from the embedded copy")
	}
	if got, want := readFile(t, f.agents()), expectedBlock("\n")+"\n"; got != want {
		t.Errorf("AGENTS.md = %q, want %q", got, want)
	}

	before := map[string]string{}
	for _, p := range []string{filepath.Join(f.bin, "atl"), f.skill(), f.agents()} {
		before[p] = readFile(t, p)
	}
	t.Setenv("PATH", f.bin+string(os.PathListSeparator)+"/usr/bin")
	out, _, code = runAtl(t, "install", "--bin-dir", f.bin)
	if code != 0 || strings.Count(out, "(unchanged)") != 3 || strings.Contains(out, "not on PATH") {
		t.Errorf("rerun: exit %d\n%s", code, out)
	}
	for p, want := range before {
		if readFile(t, p) != want {
			t.Errorf("%s changed on rerun", p)
		}
	}

	if err := os.WriteFile(f.src, []byte("fake atl binary v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, _, _ = runAtl(t, "install", "--bin-dir", f.bin)
	testenv.MustContain(t, out, "binary       "+filepath.Join(f.bin, "atl")+" (updated)")
}

func TestInstallPreservesAgentsContent(t *testing.T) {
	f := newInstallFixture(t)
	const mine = "# Mine\n\nKeep this.\n"
	if err := os.MkdirAll(f.oc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.agents(), []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errs, code := runAtl(t, "install", "--no-binary"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	got := readFile(t, f.agents())
	if want := mine + "\n" + expectedBlock("\n") + "\n"; got != want {
		t.Fatalf("AGENTS.md = %q, want %q", got, want)
	}
	if info, _ := os.Stat(f.agents()); info.Mode().Perm() != 0o600 {
		t.Errorf("existing file mode not kept: %v", info.Mode().Perm())
	}

	old := skill.Instructions
	skill.Instructions = "## Changed\n\nNew text.\n"
	t.Cleanup(func() { skill.Instructions = old })
	out, _, _ := runAtl(t, "install", "--no-binary")
	testenv.MustContain(t, out, "opencode     "+f.agents()+" (updated)")
	got = readFile(t, f.agents())
	if !strings.HasPrefix(got, mine) || strings.Count(got, agentsBegin) != 1 || !strings.Contains(got, "New text.") || strings.Contains(got, "Jira and Confluence") {
		t.Errorf("block not replaced in place:\n%s", got)
	}

	out, errs, code := runAtl(t, "uninstall", "--keep-binary")
	if code != 0 {
		t.Fatalf("uninstall: exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "opencode     "+f.agents()+" (section removed)", "binary       kept (--keep-binary)")
	if got := readFile(t, f.agents()); got != mine {
		t.Errorf("uninstall left %q, want %q", got, mine)
	}
}

func TestUpsertBlockLineEndings(t *testing.T) {
	for _, tc := range []struct{ name, doc, want string }{
		{"empty", "", expectedBlock("\n") + "\n"},
		{"no trailing newline", "x", "x\n\n" + expectedBlock("\n") + "\n"},
		{"trailing blank line", "x\n\n", "x\n\n" + expectedBlock("\n") + "\n"},
		{"crlf without trailing newline", "a\r\nb", "a\r\nb\r\n\r\n" + expectedBlock("\r\n") + "\r\n"},
		{"block in the middle", "a\n\n" + agentsBegin + "\nold\n" + agentsEnd + "\n\nb\n", "a\n\n" + expectedBlock("\n") + "\n\nb\n"},
	} {
		got, err := upsertBlock(tc.doc, skill.Instructions, false)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.name, got, err, tc.want)
			continue
		}
		if again, _ := upsertBlock(got, skill.Instructions, false); again != got {
			t.Errorf("%s: not idempotent: %q", tc.name, again)
		}
		if strings.Contains(tc.doc, "\r\n") && strings.Count(got, "\n") != strings.Count(got, "\r\n") {
			t.Errorf("%s: mixed line endings: %q", tc.name, got)
		}
	}

	doc, changed, err := removeBlock("a\n\n" + expectedBlock("\n") + "\n\nb\n")
	if err != nil || !changed || doc != "a\n\nb\n" {
		t.Errorf("removeBlock middle = %q, %v, %v", doc, changed, err)
	}
	if doc, changed, err := removeBlock("plain\n"); err != nil || changed || doc != "plain\n" {
		t.Errorf("removeBlock without block = %q, %v, %v", doc, changed, err)
	}
}

func TestInstallMalformedMarkers(t *testing.T) {
	f := newInstallFixture(t)
	if err := os.MkdirAll(f.oc, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, doc, want string }{
		{"begin without end", "keep\n\n" + agentsBegin + "\nstale\n", "keep\n\n" + expectedBlock("\n") + "\n"},
		{"duplicate blocks", "keep\n" + agentsBegin + "\n1\n" + agentsEnd + "\n" + agentsBegin + "\n2\n" + agentsEnd + "\ntail\n", "keep\n" + expectedBlock("\n") + "\ntail\n"},
		{"end before begin", "keep\n" + agentsEnd + "\nmid\n" + agentsBegin + "\n", "keep\n" + expectedBlock("\n") + "\n"},
	} {
		if err := os.WriteFile(f.agents(), []byte(tc.doc), 0o644); err != nil {
			t.Fatal(err)
		}
		_, errs, code := runAtl(t, "install", "--no-binary")
		if code != cli.ExitConfig || !strings.Contains(errs, "--force") || readFile(t, f.agents()) != tc.doc {
			t.Errorf("%s: exit %d, %s", tc.name, code, errs)
		}
		if _, errs, code := runAtl(t, "uninstall", "--keep-binary"); code != cli.ExitConfig || !strings.Contains(errs, "by hand") {
			t.Errorf("%s: uninstall exit %d, %s", tc.name, code, errs)
		}
		if _, errs, code := runAtl(t, "install", "--no-binary", "--force"); code != 0 {
			t.Errorf("%s: --force exit %d, %s", tc.name, code, errs)
		}
		if got := readFile(t, f.agents()); got != tc.want {
			t.Errorf("%s: --force gave %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestInstallSkipsAndSameFile(t *testing.T) {
	f := newInstallFixture(t)
	out, errs, code := runAtl(t, "install", "--no-binary", "--no-instructions")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "binary       skipped (--no-binary)", "opencode     instructions skipped (--no-instructions)", "(created)")
	if strings.Contains(out, "not on PATH") {
		t.Errorf("PATH warning without a binary:\n%s", out)
	}
	for _, p := range []string{f.agents(), filepath.Join(f.bin, "atl")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s should not exist", p)
		}
	}

	// Running the installed binary itself: nothing to copy.
	if _, _, code := runAtl(t, "install", "--bin-dir", f.bin, "--no-instructions"); code != 0 {
		t.Fatal("first install failed")
	}
	installed := filepath.Join(f.bin, "atl")
	link := filepath.Join(f.home, "atl-link")
	if err := os.Symlink(installed, link); err != nil {
		t.Fatal(err)
	}
	executable = func() (string, error) { return link, nil }
	out, _, _ = runAtl(t, "install", "--bin-dir", f.bin, "--no-instructions")
	testenv.MustContain(t, out, "binary       "+installed+" (unchanged)")

	executable = func() (string, error) { return "", errors.New("no /proc") }
	if _, errs, code := runAtl(t, "install", "--bin-dir", f.bin); code != cli.ExitAPI || !strings.Contains(errs, "cannot find the running binary") {
		t.Errorf("missing executable: exit %d, %s", code, errs)
	}
	if _, _, code := runAtl(t, "install", "extra"); code != cli.ExitUsage {
		t.Errorf("install with an argument: exit %d", code)
	}
	if _, _, code := runAtl(t, "uninstall", "extra"); code != cli.ExitUsage {
		t.Errorf("uninstall with an argument: exit %d", code)
	}
}

func TestUninstall(t *testing.T) {
	f := newInstallFixture(t)
	if _, _, code := runAtl(t, "install", "--bin-dir", f.bin); code != 0 {
		t.Fatal("install failed")
	}
	out, errs, code := runAtl(t, "uninstall", "--bin-dir", f.bin)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out,
		"skill        "+filepath.Dir(f.skill())+" (removed)",
		"opencode     "+f.agents()+" (removed (it held only the atl section))",
		"binary       "+filepath.Join(f.bin, "atl")+" (removed)",
		"Left in place:",
	)
	for _, p := range []string{filepath.Dir(f.skill()), f.agents(), filepath.Join(f.bin, "atl")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s still exists", p)
		}
	}
	out, _, code = runAtl(t, "uninstall", "--bin-dir", f.bin)
	if code != 0 || strings.Count(out, "(absent)") != 1 || strings.Contains(out, "skill") {
		t.Errorf("second uninstall: exit %d\n%s", code, out)
	}

	if err := os.WriteFile(f.agents(), []byte("no block here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, code := runAtl(t, "uninstall", "--keep-binary"); code != 0 || readFile(t, f.agents()) != "no block here\n" {
		t.Errorf("uninstall without a section: exit %d", code)
	}
}

func TestOpencodeAndBinDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if d, _ := opencodeDir(); d != filepath.Join(home, ".config", "opencode") {
		t.Errorf("default opencode dir = %s", d)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	if d, _ := opencodeDir(); d != filepath.Join(home, "xdg", "opencode") {
		t.Errorf("XDG opencode dir = %s", d)
	}
	t.Setenv("OPENCODE_CONFIG_DIR", "/custom")
	if d, _ := opencodeDir(); d != "/custom" {
		t.Errorf("OPENCODE_CONFIG_DIR = %s", d)
	}
	if d, _ := defaultBinDir(); d != filepath.Join(home, ".local", "bin") {
		t.Errorf("bin dir = %s", d)
	}
	if got := pathHint("/opt/bin"); got != `add to your shell profile:  export PATH="/opt/bin:$PATH"` {
		t.Errorf("pathHint = %s", got)
	}
}

func TestInstallAgents(t *testing.T) {
	f := newInstallFixture(t)
	legacy := filepath.Join(f.oc, "skills", "atlassian", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, skill.Skill, 0o644); err != nil {
		t.Fatal(err)
	}

	out, errs, code := runAtl(t, "install", "--no-binary", "--agents", "all")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	claudeMD, codexMD := filepath.Join(f.claude, "CLAUDE.md"), filepath.Join(f.codex, "AGENTS.md")
	testenv.MustContain(t, out,
		"skill        "+f.claudeSkill()+" (created)",
		"skill        "+f.skill()+" (created)",
		"old skill    "+filepath.Dir(legacy)+" (removed)",
		"Claude Code  "+claudeMD+" (created)",
		"Codex        "+codexMD+" (created)",
		"opencode     "+f.agents()+" (created)",
		"Restart Claude Code, Codex, opencode to load the skill",
	)
	// Codex and opencode share ~/.agents/skills: one copy, written once.
	if n := strings.Count(out, f.skill()); n != 1 {
		t.Errorf("shared skill listed %d times\n%s", n, out)
	}
	for _, p := range []string{f.claudeSkill(), f.skill()} {
		if readFile(t, p) != string(skill.Skill) {
			t.Errorf("%s differs from the embedded skill", p)
		}
	}
	for _, p := range []string{claudeMD, codexMD, f.agents()} {
		if got := readFile(t, p); got != expectedBlock("\n")+"\n" {
			t.Errorf("%s = %q", p, got)
		}
	}
	if _, err := os.Stat(legacy); err == nil {
		t.Error("old opencode skill not removed")
	}

	out, _, code = runAtl(t, "uninstall", "--keep-binary")
	if code != 0 {
		t.Fatalf("uninstall: exit %d", code)
	}
	for _, p := range []string{f.claudeSkill(), f.skill(), claudeMD, codexMD, f.agents()} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s left after uninstall\n%s", p, out)
		}
	}

	// Only the chosen agents are touched.
	out, _, code = runAtl(t, "install", "--no-binary", "--agents", " claude ")
	if code != 0 || strings.Contains(out, "Codex") || strings.Contains(out, "opencode") {
		t.Errorf("--agents claude: exit %d\n%s", code, out)
	}
	for _, bad := range []string{"cursor", ","} {
		if _, errs, code := runAtl(t, "install", "--no-binary", "--agents", bad); code != cli.ExitUsage || !strings.Contains(errs, "claude,codex,opencode") {
			t.Errorf("--agents %q: exit %d, %s", bad, code, errs)
		}
	}
}

func TestInstallDefaultsToOpencode(t *testing.T) {
	f := newInstallFixture(t)
	for _, d := range []string{f.codex, f.claude} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Without a terminal, install sets up opencode only, even with other agents present.
	out, _, code := runAtl(t, "install", "--no-binary")
	if code != 0 || !strings.Contains(out, "opencode") || strings.Contains(out, "Codex") || strings.Contains(out, "Claude") {
		t.Errorf("default install: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(f.codex, "AGENTS.md")); !os.IsNotExist(err) {
		t.Errorf("Codex instructions written by default: %v", err)
	}
	if fmt.Sprint(defaultSelection()) != "[false false true]" {
		t.Errorf("defaultSelection = %v", defaultSelection())
	}
}

func TestSelectLoop(t *testing.T) {
	// selectLoop reads one key per Read, as a raw terminal delivers them.
	keys := func(ks ...string) io.Reader {
		r, w := io.Pipe()
		go func() {
			for _, k := range ks {
				w.Write([]byte(k))
			}
			w.Close()
		}()
		return r
	}
	for _, tc := range []struct {
		name string
		keys []string
		want []bool
		err  error
	}{
		{"enter keeps the default", []string{"\r"}, []bool{false, true, false}, nil},
		{"move and toggle", []string{"\x1b[B", "\x1b[B", " ", "k", " ", "\r"}, []bool{false, false, true}, nil},
		{"wraps upward", []string{"\x1b[A", " ", "\r"}, []bool{false, true, true}, nil},
		{"a toggles all", []string{"a", "\r"}, []bool{true, true, true}, nil},
		{"empty needs a choice", []string{"j", " ", "\r", "j", " ", "\r"}, []bool{false, false, true}, nil},
		{"q cancels", []string{"q"}, nil, errCanceled},
		{"ctrl-c cancels", []string{"\x03"}, nil, errCanceled},
	} {
		var out strings.Builder
		got, err := selectLoop(keys(tc.keys...), &out, []bool{false, true, false})
		if !errors.Is(err, tc.err) || fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%s: got %v, %v; want %v, %v", tc.name, got, err, tc.want, tc.err)
		}
	}
	var out strings.Builder
	selectLoop(keys("j", " ", "\r", "j", " ", "\r"), &out, []bool{false, true, false})
	testenv.MustContain(t, out.String(), "Select at least one agent", "> [x] opencode", "[ ] Claude Code")
}
