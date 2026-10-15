package confluence

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/RyabovNick/atl/internal/cli"
	"github.com/RyabovNick/atl/internal/markdown"
	"github.com/RyabovNick/atl/internal/testenv"
)

func runAtl(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return testenv.Run(t, map[string]testenv.Command{
		"page":   RunPage,
		"search": RunSearch,
	}, args...)
}

func TestPageRefs(t *testing.T) {
	srv := testenv.Setup(t)
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
		testenv.MustContain(t, out, "# Login spec", "Space: Development (DEV) · id 42", "Version 3 by Ann", "Path: Home / Auth",
			"URL: "+srv.URL+"/wiki/display/DEV/Login+spec", "## Flow", "**log in**", "- one")
	}
	out, _, _ := runAtl(t, "page", "42", "--max-chars", "5")
	testenv.MustContain(t, out, "[truncated at 5 of")
}

func TestSearch(t *testing.T) {
	testenv.Setup(t)
	out, errs, code := runAtl(t, "search", "login", "flow", "--space", "DEV")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "- Login spec — DEV · updated 2026-09-30 12:00 · id 42")
	if got := cqlString(`a "b" \c`); got != `"a \"b\" \\c"` {
		t.Errorf("cqlString = %s", got)
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

func TestPageListsAttachments(t *testing.T) {
	testenv.Setup(t)
	out, errs, code := runAtl(t, "page", "42")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "## Attachments (1)\n\n- diagram.png — 4 B · image/png · Ann · 2026-09-30 12:00 · id att900\n", "(download: atl attachment <id>)")
}

func TestPageTOC(t *testing.T) {
	testenv.Setup(t)
	out, errs, code := runAtl(t, "page", "77", "--toc")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "# Design", "## Contents (7 headings,", "- (before the first heading) — 11 chars",
		"\n- Overview — ", "\n  - Goals — ", "\n    - Non-goals — ", "\n- Implementation details — ",
		`atl page 77 --section "<heading>"`)
	if strings.Contains(out, "Why this exists") || strings.Contains(out, "not a heading —") || strings.Contains(out, "Cell heading —") {
		t.Errorf("toc should hold headings only, without decoys:\n%s", out)
	}

	lines := strings.Split(markdown.DemoteHeadings("intro\n## A\naaaa\n### B\nbb\n## C\nc", 2), "\n")
	var b strings.Builder
	markdown.WriteTOC(&b, lines, markdown.ScanHeadings(lines))
	testenv.MustContain(t, b.String(), "- (before the first heading) — 5 chars\n- A — 18 chars\n  - B — 8 chars\n- C — 6 chars")

	b.Reset()
	markdown.WriteTOC(&b, []string{"no headings"}, nil)
	testenv.MustContain(t, b.String(), "(no headings; read the whole page)")
}

func TestPageSection(t *testing.T) {
	testenv.Setup(t)
	section := func(args ...string) (string, string, int) {
		return runAtl(t, append([]string{"page", "77"}, args...)...)
	}

	out, errs, code := section("--section", "overview")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "# Design", "## Overview\n\nWhy this exists.", "### Goals", "#### Non-goals\n\nSSO.", "First notes.")
	for _, absent := range []string{"Intro text", "Implementation", "Second notes", "## Attachments"} {
		if strings.Contains(out, absent) {
			t.Errorf("section should stop at the next same-level heading, found %q:\n%s", absent, out)
		}
	}

	out, _, _ = section("--section", "Notes")
	testenv.MustContain(t, out, "First notes.", "Second notes.")

	out, _, code = section("--section", "non", "--section", "Implementation p")
	if code != 0 {
		t.Fatalf("prefix sections: exit %d", code)
	}
	testenv.MustContain(t, out, "SSO.", "Phase 1.")
	if strings.Index(out, "SSO.") > strings.Index(out, "Phase 1.") {
		t.Errorf("sections should print in the order asked:\n%s", out)
	}

	out, _, _ = section("--section", "goals")
	testenv.MustContain(t, out, "Fast login.")
	if strings.Contains(out, "Why this exists") {
		t.Errorf("exact match should win over substring:\n%s", out)
	}

	if _, _, code := section("--section", "phase"); code != cli.ExitNotFound {
		t.Errorf("sections match titles, not body text: exit %d", code)
	}

	if _, errs, code := section("--section", "Implementation"); code != cli.ExitUsage || !strings.Contains(errs, "ambiguous") ||
		!strings.Contains(errs, "Implementation details") || !strings.Contains(errs, "Implementation plan") {
		t.Errorf("ambiguous prefix: exit %d, %s", code, errs)
	}
	if _, errs, code := section("--section", "tion"); code != cli.ExitUsage || !strings.Contains(errs, "ambiguous") {
		t.Errorf("ambiguous substring: exit %d, %s", code, errs)
	}
	if _, errs, code := section("--section", "Rollout"); code != cli.ExitNotFound || !strings.Contains(errs, "headings are:\n  Overview\n    Goals") {
		t.Errorf("missing section: exit %d, %s", code, errs)
	}
	if _, errs, code := runAtl(t, "page", "42", "--section", "Nope", "--section", "flow"); code != cli.ExitNotFound || !strings.Contains(errs, "Flow") {
		t.Errorf("missing section on another page: exit %d, %s", code, errs)
	}
	if _, err := markdown.FindSections(nil, "x"); err == nil || !strings.Contains(err.Error(), "no headings") {
		t.Errorf("headingless page: %v", err)
	}

	out, _, _ = section("--section", "overview", "--max-chars", "10")
	testenv.MustContain(t, out, "[truncated at 10 of")

	out, _, _ = section("--toc", "--section", "Implementation plan")
	testenv.MustContain(t, out, "## Contents", "Phase 1.")
	if strings.Contains(out, "## Attachments") {
		t.Errorf("--section output should skip attachments:\n%s", out)
	}

	if _, errs, code := section("--toc", "--json"); code != cli.ExitUsage || !strings.Contains(errs, "--json") {
		t.Errorf("--toc --json: exit %d, %s", code, errs)
	}
}

func TestPageChildren(t *testing.T) {
	srv := testenv.Setup(t)
	out, errs, code := runAtl(t, "page", "77", "--children")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "# Design", "## Child pages (52)", "- Child 0 — id 1000 · updated 2026-09-01 10:00\n  "+srv.URL+"/wiki/pages/1000", "- Child 51 — id 1051")
	if strings.Contains(out, "Why this exists") || strings.Contains(out, "Grandchild") || strings.Contains(out, "## Attachments") {
		t.Errorf("--children alone lists one level and no body:\n%s", out)
	}

	out, _, _ = runAtl(t, "page", "77", "--children", "--depth", "2")
	testenv.MustContain(t, out, "## Child pages (53)", "- Child 0 — id 1000", "\n  - Grandchild — id 2000 · updated 2026-09-05 10:00\n    "+srv.URL+"/wiki/pages/2000")

	out, _, _ = runAtl(t, "page", "77", "--children", "--toc")
	testenv.MustContain(t, out, "## Contents", "\n\n## Child pages (52)")
	if strings.Count(out, "# Design") != 1 || strings.Contains(out, "Why this exists") {
		t.Errorf("--children --toc prints the header once and no body:\n%s", out)
	}

	out, _, _ = runAtl(t, "page", "42", "--children")
	testenv.MustContain(t, out, "## Child pages (0)\n\n(none)")

	saved := maxChildPages
	maxChildPages = 51
	t.Cleanup(func() { maxChildPages = saved })
	out, _, _ = runAtl(t, "page", "77", "--children", "--depth", "3")
	testenv.MustContain(t, out, "## Child pages (51)", "(stopped at 51 pages")
	maxChildPages = 50
	out, _, _ = runAtl(t, "page", "77", "--children")
	testenv.MustContain(t, out, "## Child pages (50)", "(stopped at 50 pages")
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
	if got.Page.Title != "Design" || len(got.Children) != testenv.ChildCount || got.Children[0].Children[0].Title != "Grandchild" || got.Truncated {
		t.Errorf("json tree: %+v", got)
	}
	out, _, _ = runAtl(t, "page", "42", "--children", "--json")
	testenv.MustContain(t, out, `"children": []`)

	for _, d := range []string{"0", "4"} {
		if _, errs, code := runAtl(t, "page", "77", "--children", "--depth", d); code != cli.ExitUsage || !strings.Contains(errs, "--depth") {
			t.Errorf("depth %s: exit %d, %s", d, code, errs)
		}
	}
}

func TestConfluenceNotFoundHint(t *testing.T) {
	testenv.Setup(t)
	_, errs, code := runAtl(t, "page", "99999")
	if code != cli.ExitNotFound || !strings.Contains(errs, "atl doctor") {
		t.Errorf("exit %d: %s", code, errs)
	}
}

func TestStringsFlag(t *testing.T) {
	var f stringsFlag
	_ = f.Set("a")
	_ = f.Set("b")
	if f.String() != "a, b" {
		t.Errorf("stringsFlag = %q", f.String())
	}
}
