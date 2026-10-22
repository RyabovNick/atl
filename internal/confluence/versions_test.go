package confluence

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/RyabovNick/atl/internal/cli"
	"github.com/RyabovNick/atl/internal/diff"
	"github.com/RyabovNick/atl/internal/testenv"
)

func fixNow(t *testing.T, s string) {
	t.Helper()
	ref, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	old := now
	now = func() time.Time { return ref }
	t.Cleanup(func() { now = old })
}

func TestPageVersions(t *testing.T) {
	testenv.Setup(t)
	out, errs, code := runAtl(t, "page", "42", "--versions")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "# Login spec — versions", "Current: v3 · 3 versions listed",
		"- v3 · 2026-09-30 12:00 · Ann — \"Bold\" (minor edit)\n- v2 · 2026-09-20 10:00 · Cy — \"Add list\"\n- v1 · 2026-09-01 09:00 · Bob\n",
		"atl page 42 --version N")

	out, _, _ = runAtl(t, "page", "42", "--versions", "--limit", "1")
	testenv.MustContain(t, out, "1 version listed", "- v3 ·", "(2 older versions; --limit N for more)")

	fixNow(t, "2026-10-01T00:00:00Z")
	out, _, _ = runAtl(t, "page", "42", "--versions", "--since", "15d")
	testenv.MustContain(t, out, "2 versions listed since", "- v3 ·", "- v2 ·")
	if strings.Contains(out, "- v1 ·") {
		t.Errorf("v1 predates --since:\n%s", out)
	}

	out, _, _ = runAtl(t, "page", "42", "--versions", "--json")
	var got struct {
		Results []version `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Results) != 3 {
		t.Errorf("--versions --json: %v, %+v", err, got)
	}

	// Page 77's server has no version list, so its versions are read one by one.
	out, errs, code = runAtl(t, "page", "77", "--versions")
	if code != 0 {
		t.Fatalf("fallback: exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "Current: v1 · 1 version listed", "- v1 · 2026-09-01 08:00")
}

func TestPageAtVersion(t *testing.T) {
	testenv.Setup(t)
	out, errs, code := runAtl(t, "page", "42", "--version", "1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "# Login\n", "Version 1 by Bob", "## Old\n\ngone")
	if _, _, code := runAtl(t, "page", "42", "--version", "9"); code != cli.ExitNotFound {
		t.Errorf("missing version: exit %d", code)
	}
}

func TestPageDiff(t *testing.T) {
	testenv.Setup(t)
	out, errs, code := runAtl(t, "page", "42", "--diff")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "# Login spec — changes v2 → v3", "From: v2 by Cy", "To: v3 by Ann",
		"selectedPageVersions=2&selectedPageVersions=3",
		"## Edits\n\n- v3 · 2026-09-30 12:00 · Ann",
		"+1 −1 lines in 1 hunk\n",
		"```diff\n@@ -1,5 +1,5 @@ ## Flow\n ## Flow\n \n-Users log in.\n+Users **log in**.\n \n - one\n```")

	out, _, _ = runAtl(t, "page", "42", "--diff", "--from", "1", "--to", "2")
	testenv.MustContain(t, out, "changes v1 → v2", "Title: Login → Login spec", "- v2 · 2026-09-20", "-## Old", "+- one")
	if strings.Contains(out, "- v3 ·") {
		t.Errorf("v3 is outside the range:\n%s", out)
	}

	fixNow(t, "2026-10-01T00:00:00Z")
	out, _, _ = runAtl(t, "page", "42", "--diff", "--since", "2026-09-10")
	testenv.MustContain(t, out, "changes v1 → v3", "Since: 2026-09-10", "- v2 ·", "- v3 ·")
	out, _, _ = runAtl(t, "page", "42", "--diff", "--since", "6h")
	testenv.MustContain(t, out, "changes v3 → v3", "(no edits since")
	out, _, _ = runAtl(t, "page", "42", "--diff", "--since", "2026-08-01")
	testenv.MustContain(t, out, "From: v0 (before the page existed)", "@@ -0,0 +1,5 @@ ## Flow\n+## Flow")

	out, _, _ = runAtl(t, "page", "42", "--diff", "--max-chars", "20")
	testenv.MustContain(t, out, "```\n[truncated at 20 of")

	out, _, _ = runAtl(t, "page", "42", "--diff", "--json")
	var got struct {
		From page `json:"from"`
		To   page `json:"to"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.From.Version.Number != 2 || got.To.Version.Number != 3 {
		t.Errorf("--diff --json: %v, %+v", err, got)
	}

	for _, args := range [][]string{
		{"--diff", "--from", "3"},
		{"--diff", "--to", "4"},
		{"--diff", "--from", "2", "--to", "1"},
		{"--diff", "--since", "7d", "--from", "1"},
		{"--diff", "--versions"},
		{"--diff", "--toc"},
		{"--version", "1", "--children"},
		{"--from", "1"},
		{"--since", "7d"},
		{"--versions", "--limit", "0"},
	} {
		if _, _, code := runAtl(t, append([]string{"page", "42"}, args...)...); code != cli.ExitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, cli.ExitUsage)
		}
	}
}

func TestUnifiedSections(t *testing.T) {
	from := []string{"## A", "a", "", "## B", "b"}
	to := []string{"## A", "", "## B", "b", "new"}
	text, added, removed := unified(diff.Hunks(diff.Lines(from, to), 0), to)
	// The deleted line sits under A even though B's heading follows it.
	want := "@@ -2,1 +1,0 @@ ## A\n-a\n@@ -5,0 +5,1 @@ ## B\n+new\n"
	if text != want || added != 1 || removed != 1 {
		t.Errorf("unified = %q (+%d -%d), want %q", text, added, removed, want)
	}
	if got := codeFence("a ``` b"); got != "````" {
		t.Errorf("codeFence = %q", got)
	}
}
