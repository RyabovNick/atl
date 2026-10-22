package confluence

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RyabovNick/atl/internal/atlassian"
	"github.com/RyabovNick/atl/internal/cli"
	"github.com/RyabovNick/atl/internal/diff"
	"github.com/RyabovNick/atl/internal/markdown"
)

const (
	versionBatch = 100
	diffContext  = 3
)

// maxVersions bounds a version listing; a var so tests can lower it.
var maxVersions = 200

// now is swapped in tests so durations like 7d have a fixed reference.
var now = time.Now

type version struct {
	Number    int              `json:"number"`
	When      string           `json:"when"`
	By        *atlassian.Named `json:"by"`
	Message   string           `json:"message"`
	MinorEdit bool             `json:"minorEdit"`
}

// before reports whether v was saved before t; an unparseable time is not.
func (v version) before(t time.Time) bool {
	when, err := time.Parse(time.RFC3339, v.When)
	return err == nil && when.Before(t)
}

func (v version) line() string {
	s := fmt.Sprintf("v%d · %s · %s", v.Number, atlassian.ShortDate(v.When), cli.Or(atlassian.NameOf(v.By), "?"))
	if v.Message != "" {
		s += fmt.Sprintf(" — %q", v.Message)
	}
	if v.MinorEdit {
		s += " (minor edit)"
	}
	return s
}

// pageVersions lists a page's versions newest first, at most limit of them,
// stopping after the first one stop accepts. Data Center lists versions only
// in its experimental API; where that is missing, they are read one by one.
func pageVersions(conf *atlassian.Client, id string, current, limit int, stop func(version) bool) (vs []version, raws []json.RawMessage, err error) {
	add := func(raw json.RawMessage) (bool, error) {
		var v version
		if err := json.Unmarshal(raw, &v); err != nil {
			return false, cli.Fail(cli.ExitAPI, "decoding version: %v", err)
		}
		vs, raws = append(vs, v), append(raws, raw)
		return len(vs) >= limit || (stop != nil && stop(v)), nil
	}
	for start := 0; ; {
		var res struct {
			Results []json.RawMessage `json:"results"`
			Links   struct {
				Next string `json:"next"`
			} `json:"_links"`
		}
		params := url.Values{"start": {strconv.Itoa(start)}, "limit": {strconv.Itoa(versionBatch)}}
		_, err := conf.Get("/rest/experimental/content/"+id+"/version", params, &res)
		if e, ok := errors.AsType[*cli.Error](err); ok && e.Code == cli.ExitNotFound && start == 0 {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		for _, r := range res.Results {
			if done, err := add(r); err != nil || done {
				return vs, raws, err
			}
		}
		start += len(res.Results)
		if len(res.Results) == 0 || (res.Links.Next == "" && len(res.Results) < versionBatch) {
			return vs, raws, nil
		}
	}
	for n := current; n >= 1; n-- {
		var p struct {
			Version json.RawMessage `json:"version"`
		}
		if _, err := conf.Get("/rest/api/content/"+id, versionParams(n, current, "version"), &p); err != nil {
			return nil, nil, err
		}
		if done, err := add(p.Version); err != nil || done {
			return vs, raws, err
		}
	}
	return vs, raws, nil
}

// versionParams asks for version n of a page; the current one is not historical.
func versionParams(n, current int, expand string) url.Values {
	params := url.Values{"expand": {expand}}
	if n != current {
		params.Set("status", "historical")
		params.Set("version", strconv.Itoa(n))
	}
	return params
}

func versionsFooter(id string) string {
	return fmt.Sprintf("(read one with: atl page %s --version N; compare two with: atl page %s --diff --from N --to M)", id, id)
}

// writeVersions prints `atl page --versions`.
func writeVersions(stdout io.Writer, conf *atlassian.Client, p page, limit int, cutoff time.Time, asJSON bool) error {
	var stop func(version) bool
	if !cutoff.IsZero() {
		limit = maxVersions
		stop = func(v version) bool { return v.before(cutoff) }
	}
	vs, raws, err := pageVersions(conf, p.ID, p.Version.Number, limit, stop)
	if err != nil {
		return err
	}
	// The version that was current at the cutoff ends the walk but is older than asked.
	if n := len(vs); stop != nil && n > 0 && stop(vs[n-1]) {
		vs, raws = vs[:n-1], raws[:n-1]
	}
	if asJSON {
		if raws == nil {
			raws = []json.RawMessage{}
		}
		out, jsonErr := json.Marshal(map[string]any{"results": raws})
		if jsonErr != nil {
			return cli.Fail(cli.ExitAPI, "encoding JSON: %v", jsonErr)
		}
		return cli.WriteJSON(stdout, out)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s — versions\n\n", p.Title)
	fmt.Fprintf(&b, "Space: %s (%s) · id %s\n", cli.Or(p.Space.Name, "?"), cli.Or(p.Space.Key, "?"), p.ID)
	scope := ""
	if !cutoff.IsZero() {
		scope = " since " + cutoff.Format("2006-01-02 15:04")
	}
	fmt.Fprintf(&b, "Current: v%d · %s listed%s\n", p.Version.Number, plural(len(vs), "version"), scope)
	fmt.Fprintf(&b, "URL: %s\n\n## Versions\n\n", pageURL(conf.Base, p.ID))
	if len(vs) == 0 {
		fmt.Fprintf(&b, "(no versions%s)\n", scope)
	}
	for _, v := range vs {
		fmt.Fprintf(&b, "- %s\n", v.line())
	}
	if oldest := len(vs); oldest > 0 && vs[oldest-1].Number > 1 && cutoff.IsZero() {
		fmt.Fprintf(&b, "\n(%d older versions; --limit N for more)\n", vs[oldest-1].Number-1)
	}
	fmt.Fprintf(&b, "\n%s\n", versionsFooter(p.ID))
	_, err = io.WriteString(stdout, b.String())
	return err
}

// versionBody fetches version n of a page and converts it as `atl page` does,
// so a diff lines up with what the page command prints.
func versionBody(conf *atlassian.Client, id string, n, current int) (page, []string, []byte, error) {
	var p page
	raw, err := conf.Get("/rest/api/content/"+id, versionParams(n, current, "body.view,version"), &p)
	if err != nil {
		return p, nil, nil, err
	}
	md, err := markdown.FromHTML(p.Body.View.Value)
	if err != nil {
		return p, nil, nil, cli.Fail(cli.ExitAPI, "converting page %s version %d to Markdown: %v", id, n, err)
	}
	if md == "" {
		return p, nil, raw, nil
	}
	return p, strings.Split(markdown.DemoteHeadings(md, 2), "\n"), raw, nil
}

// diffRange is what `atl page --diff` compares: versions from and to, where
// from 0 stands for the empty page before version 1. A negative from or a zero
// to means unset: the previous and the current version.
type diffRange struct {
	from, to int
	cutoff   time.Time
}

// writeDiff prints `atl page --diff`.
func writeDiff(stdout io.Writer, conf *atlassian.Client, p page, r diffRange, maxChars int, asJSON bool) error {
	current := p.Version.Number
	if r.to == 0 {
		r.to = current
	}
	if r.from < 0 {
		r.from = r.to - 1
	}
	if r.to < 1 || r.to > current || r.from >= r.to {
		return cli.Fail(cli.ExitUsage, "versions must satisfy 0 ≤ --from < --to ≤ %d (the current version), got %d and %d", current, r.from, r.to)
	}
	// The listing names who made the edits in between, and resolves --since to
	// the version that was current at the cutoff.
	limit := min(maxVersions, current-r.from)
	stop := func(v version) bool { return v.Number <= r.from }
	if !r.cutoff.IsZero() {
		limit, stop = maxVersions, func(v version) bool { return v.before(r.cutoff) }
	}
	vs, _, listErr := pageVersions(conf, p.ID, current, limit, stop)
	if !r.cutoff.IsZero() {
		if listErr != nil {
			return listErr
		}
		r.from = 0
		if n := len(vs); n > 0 && vs[n-1].before(r.cutoff) {
			r.from = vs[n-1].Number
		} else if n == limit {
			r.from = vs[n-1].Number
		}
	}
	var edits []version
	for _, v := range vs {
		if v.Number > r.from && v.Number <= r.to {
			edits = append(edits, v)
		}
	}

	var fromPage page
	var a []string
	fromRaw := []byte("null")
	if r.from > 0 && r.from < r.to {
		var err error
		if fromPage, a, fromRaw, err = versionBody(conf, p.ID, r.from, current); err != nil {
			return err
		}
	}
	toPage, b, toRaw, err := versionBody(conf, p.ID, r.to, current)
	if err != nil {
		return err
	}
	if r.from == r.to {
		fromPage, a, fromRaw = toPage, b, toRaw
	}
	if asJSON {
		out, jsonErr := json.Marshal(map[string]any{"from": json.RawMessage(fromRaw), "to": json.RawMessage(toRaw)})
		if jsonErr != nil {
			return cli.Fail(cli.ExitAPI, "encoding JSON: %v", jsonErr)
		}
		return cli.WriteJSON(stdout, out)
	}

	var out strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&out, format+"\n", args...) }
	line("# %s — changes v%d → v%d\n", p.Title, r.from, r.to)
	line("Space: %s (%s) · id %s", cli.Or(p.Space.Name, "?"), cli.Or(p.Space.Key, "?"), p.ID)
	if !r.cutoff.IsZero() {
		line("Since: %s", r.cutoff.Format("2006-01-02 15:04"))
	}
	if r.from == 0 {
		line("From: v0 (before the page existed)")
	} else {
		line("From: v%d by %s at %s", r.from, cli.Or(atlassian.NameOf(fromPage.Version.By), "?"), atlassian.ShortDate(fromPage.Version.When))
	}
	line("To: v%d by %s at %s", r.to, cli.Or(atlassian.NameOf(toPage.Version.By), "?"), atlassian.ShortDate(toPage.Version.When))
	if fromPage.Title != "" && fromPage.Title != toPage.Title {
		line("Title: %s → %s", fromPage.Title, toPage.Title)
	}
	if r.from > 0 && r.from < r.to {
		line("Compare: %s/pages/diffpagesbyversion.action?pageId=%s&selectedPageVersions=%d&selectedPageVersions=%d",
			conf.Base, url.QueryEscape(p.ID), r.from, r.to)
	}
	if r.from < r.to {
		line("\n## Edits\n")
		for _, v := range slices.Backward(edits) {
			line("- %s", v.line())
		}
		switch {
		case listErr != nil:
			line("(version list unavailable: %v)", listErr)
		case len(edits) < r.to-r.from:
			line("(%d of %d edits listed)", len(edits), r.to-r.from)
		}
	}

	line("\n## Changes\n")
	hunks := diff.Hunks(diff.Lines(a, b), diffContext)
	if len(hunks) == 0 {
		if r.from == r.to {
			line("(no edits since %s)", r.cutoff.Format("2006-01-02 15:04"))
		} else {
			line("(no changes to the body)")
		}
		_, err = io.WriteString(stdout, out.String())
		return err
	}
	text, added, removed := unified(hunks, b)
	line("+%d −%d lines in %s\n", added, removed, plural(len(hunks), "hunk"))
	truncated := ""
	if runes := []rune(text); maxChars > 0 && len(runes) > maxChars {
		text = string(runes[:maxChars])
		truncated = fmt.Sprintf("\n[truncated at %d of %d characters; --max-chars 0 for all]", maxChars, len(runes))
	}
	fence := codeFence(text)
	line("%sdiff\n%s\n%s%s", fence, strings.TrimSuffix(text, "\n"), fence, truncated)
	_, err = io.WriteString(stdout, out.String())
	return err
}

// unified formats hunks as a unified diff whose @@ lines name the section of
// the new version each hunk falls in, the way git names the enclosing function.
func unified(hunks []diff.Hunk, to []string) (text string, added, removed int) {
	hs := markdown.ScanHeadings(to)
	var b strings.Builder
	for _, h := range hunks {
		// The first changed line, as an index into to, picks the section. A
		// deletion sits before to[first], so a heading there comes after it.
		first, kind := h.ToLine-1, diff.Equal
		if h.ToCount == 0 {
			first = h.ToLine
		}
		for _, op := range h.Ops {
			if kind = op.Kind; kind != diff.Equal {
				break
			}
			first++
		}
		section := ""
		for _, hd := range hs {
			if hd.Start > first || (hd.Start == first && kind == diff.Delete) {
				break
			}
			section = " " + strings.Repeat("#", hd.Level) + " " + hd.Title
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@%s\n", h.FromLine, h.FromCount, h.ToLine, h.ToCount, section)
		for _, op := range h.Ops {
			fmt.Fprintf(&b, "%c%s\n", op.Kind, op.Text)
			switch op.Kind {
			case diff.Insert:
				added++
			case diff.Delete:
				removed++
			}
		}
	}
	return b.String(), added, removed
}

// codeFence returns a backtick fence longer than any backtick run in text.
func codeFence(text string) string {
	longest, run := 0, 0
	for _, c := range text {
		if c == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
