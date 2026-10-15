package jira

import (
	"fmt"
	"io"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RyabovNick/atl/internal/atlassian"
	"github.com/RyabovNick/atl/internal/cli"
)

const (
	jiraTime = "2006-01-02T15:04:05.000-0700"
	// Long text edits are summarized with a line diff, capped so a rewritten
	// description does not print both versions in full.
	historyDiffLines = 20
	historyDiffInput = 200
	historyLineChars = 200
)

// now is swapped in tests so durations like 7d have a fixed reference.
var now = time.Now

type changeItem struct {
	Field      string `json:"field"`
	FieldType  string `json:"fieldtype"`
	FieldID    string `json:"fieldId"`
	FromString string `json:"fromString"`
	ToString   string `json:"toString"`
}

type change struct {
	Author  *atlassian.Named `json:"author"`
	Created string           `json:"created"`
	Items   []changeItem     `json:"items"`
	when    time.Time
}

type historyIssue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary string  `json:"summary"`
		Status  *status `json:"status"`
	} `json:"fields"`
	Changelog struct {
		Total     int      `json:"total"`
		Histories []change `json:"histories"`
	} `json:"changelog"`
}

func parseSince(s string, ref time.Time) (time.Time, error) {
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	for suffix, days := range map[string]int{"d": 1, "w": 7} {
		if n, ok := strings.CutSuffix(s, suffix); ok {
			if count, err := strconv.Atoi(n); err == nil && count >= 0 {
				return ref.AddDate(0, 0, -count*days), nil
			}
		}
	}
	if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return ref.Add(-d), nil
	}
	return time.Time{}, cli.Fail(cli.ExitUsage, "--since takes a date (2026-10-01) or a duration (7d, 2w, 48h), not %q", s)
}

// fieldNames resolves changelog field labels: DC names system fields by id
// ("status", "description") and custom fields by display name.
type fieldNames struct {
	byID   map[string]fieldDef
	byName map[string]fieldDef
}

func newFieldNames(defs []fieldDef) fieldNames {
	fn := fieldNames{byID: map[string]fieldDef{}, byName: map[string]fieldDef{}}
	for _, d := range defs {
		fn.byID[d.ID] = d
		fn.byName[d.Name] = d
	}
	return fn
}

func (fn fieldNames) lookup(it changeItem) (string, bool) {
	for _, d := range []fieldDef{fn.byID[it.FieldID], fn.byID[it.Field], fn.byName[it.Field]} {
		if d.ID != "" {
			return d.Name, skippedType(d.Schema.Custom)
		}
	}
	if it.FieldType == "jira" && it.Field != "" {
		return strings.ToUpper(it.Field[:1]) + it.Field[1:], false
	}
	return it.Field, false
}

func isLongText(s string) bool {
	return strings.Contains(s, "\n") || len([]rune(s)) > longFieldChars
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// lineDiff is a longest-common-subsequence diff, returning only changed lines.
func lineDiff(a, b []string) []string {
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i, v := range slices.Backward(a) {
		for j := len(b) - 1; j >= 0; j-- {
			if v == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			i, j = i+1, j+1
		case j == len(b) || (i < len(a) && lcs[i+1][j] >= lcs[i][j+1]):
			out = append(out, "- "+a[i])
			i++
		default:
			out = append(out, "+ "+b[j])
			j++
		}
	}
	return out
}

func longChange(name, from, to string) []string {
	fromLen, toLen := len([]rune(from)), len([]rune(to))
	var out []string
	switch {
	case from == "":
		out = append(out, fmt.Sprintf("- %s set (%d chars)", name, toLen))
	case to == "":
		out = append(out, fmt.Sprintf("- %s cleared (was %d chars)", name, fromLen))
	default:
		out = append(out, fmt.Sprintf("- %s changed (%d → %d chars)", name, fromLen, toLen))
	}
	a, b := splitLines(from), splitLines(to)
	if len(a) > historyDiffInput || len(b) > historyDiffInput {
		return out
	}
	diff := lineDiff(a, b)
	for i, d := range diff {
		if i == historyDiffLines {
			out = append(out, fmt.Sprintf("    … %d more changed lines", len(diff)-i))
			break
		}
		out = append(out, "    "+clip(d, historyLineChars))
	}
	return out
}

func changeLines(it changeItem, names fieldNames) []string {
	name, skip := names.lookup(it)
	if skip {
		return nil
	}
	if isLongText(it.FromString) || isLongText(it.ToString) {
		return longChange(name, it.FromString, it.ToString)
	}
	return []string{fmt.Sprintf("- %s: %s → %s", name, cli.Or(it.FromString, "(none)"), cli.Or(it.ToString, "(none)"))}
}

// RunHistory implements `atl history`.
func RunHistory(args []string, stdout, stderr io.Writer) error {
	fs := cli.NewFlags("history", "[KEY]", "Who changed what on a Jira issue, oldest first. Long text edits are summarized as a line diff.\nKEY defaults to the issue key in the current git branch.", stderr)
	since := fs.String("since", "", "only changes on or after a date (2026-10-01) or within a duration (7d, 2w, 48h)")
	asJSON := fs.Bool("json", false, "print the raw API response")
	pos, err := cli.ParseFlags(fs, args)
	if err != nil {
		return err
	}
	key, err := cli.KeyArg(pos, "history", stderr)
	if err != nil {
		return err
	}
	var cutoff time.Time
	if *since != "" {
		if cutoff, err = parseSince(*since, now()); err != nil {
			return err
		}
	}

	jira, err := atlassian.NewClient("jira")
	if err != nil {
		return err
	}
	var defs []fieldDef
	if _, err = jira.Get("/rest/api/2/field", nil, &defs); err != nil {
		return err
	}
	var is historyIssue
	raw, err := jira.Get("/rest/api/2/issue/"+url.PathEscape(key), url.Values{"fields": {"summary,status"}, "expand": {"changelog"}}, &is)
	if err != nil {
		return err
	}
	if *asJSON {
		return cli.WriteJSON(stdout, raw)
	}

	all := is.Changelog.Histories
	for i := range all {
		all[i].when, _ = time.Parse(jiraTime, all[i].Created)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].when.Before(all[j].when) })
	names := newFieldNames(defs)

	type block struct {
		head  string
		lines []string
	}
	var blocks []block
	for _, c := range all {
		// An unparseable timestamp is kept: dropping a change silently is worse than an extra one.
		if !cutoff.IsZero() && !c.when.IsZero() && c.when.Before(cutoff) {
			continue
		}
		var lines []string
		for _, it := range c.Items {
			lines = append(lines, changeLines(it, names)...)
		}
		if len(lines) > 0 {
			blocks = append(blocks, block{fmt.Sprintf("%s %s", atlassian.ShortDate(c.Created), cli.Or(atlassian.NameOf(c.Author), "?")), lines})
		}
	}

	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	line("# %s: %s — history\n", is.Key, is.Fields.Summary)
	scope := ""
	if !cutoff.IsZero() {
		scope = " since " + cutoff.Format("2006-01-02 15:04")
	}
	st := "?"
	if is.Fields.Status != nil {
		st = is.Fields.Status.Name
	}
	line("Status: %s · %d changes%s", st, len(blocks), scope)
	if total := is.Changelog.Total; total > len(all) {
		line("(partial: Jira returned %d of %d changes)", len(all), total)
	}
	line("URL: %s/browse/%s", jira.Base, is.Key)
	line("\n## History")
	if len(blocks) == 0 {
		line("\n(no changes%s)", scope)
	}
	for _, bl := range blocks {
		line("\n### %s\n", bl.head)
		line("%s", strings.Join(bl.lines, "\n"))
	}
	_, err = io.WriteString(stdout, b.String())
	return err
}
