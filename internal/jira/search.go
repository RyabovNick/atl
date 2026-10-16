package jira

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/RyabovNick/atl/internal/atlassian"
	"github.com/RyabovNick/atl/internal/cli"
)

const searchFields = "summary,status,issuetype,priority,assignee,updated"

type searchResult struct {
	Total  int        `json:"total"`
	Issues []issueRef `json:"issues"`
}

func searchLine(r *issueRef) string {
	s := issueLine(r)
	if a := atlassian.NameOf(r.Fields.Assignee); a != "" {
		s += " · " + a
	}
	if r.Fields.Updated != "" {
		s += " · updated " + atlassian.ShortDate(r.Fields.Updated)
	}
	return s
}

func searchIssues(jira *atlassian.Client, jql string, limit int) ([]byte, searchResult, error) {
	var res searchResult
	params := url.Values{"jql": {jql}, "maxResults": {strconv.Itoa(limit)}, "fields": {searchFields}}
	raw, err := jira.Get("/rest/api/2/search", params, &res)
	return raw, res, err
}

func truncationNote(w io.Writer, shown, total int) {
	if total > shown {
		fmt.Fprintf(w, "\nshowing %d of %d; raise --limit or narrow the query\n", shown, total)
	}
}

// RunJQL implements `atl jql`.
func RunJQL(args []string, stdout, stderr io.Writer) error {
	fs := cli.NewFlags("jql", "<query>", "Jira issues matching a JQL query. A query Jira rejects exits 2 with its message.", stderr)
	limit := fs.Int("limit", 20, "maximum results")
	asJSON := fs.Bool("json", false, "print the raw API response")
	pos, err := cli.ParseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return cli.Fail(cli.ExitUsage, "jql needs a query")
	}
	jql := strings.Join(pos, " ")
	jira, err := atlassian.NewClient("jira")
	if err != nil {
		return err
	}
	raw, res, err := searchIssues(jira, jql, *limit)
	if err != nil {
		return err
	}
	if *asJSON {
		return cli.WriteJSON(stdout, raw)
	}
	if len(res.Issues) == 0 {
		fmt.Fprintf(stdout, "no issues for: %s\n", jql)
		return nil
	}
	for i := range res.Issues {
		fmt.Fprintf(stdout, "- %s\n", searchLine(&res.Issues[i]))
	}
	truncationNote(stdout, len(res.Issues), res.Total)
	return nil
}

// epicFields holds the ids of Jira Software's Epic Link and Epic Name fields,
// "" when the instance has none.
type epicFields struct{ link, name string }

// findEpicFields matches by field type: names are translated with the user's
// locale, so "Epic Link" is not a reliable way to find the field.
func findEpicFields(fields []fieldDef) epicFields {
	var ef epicFields
	for _, f := range fields {
		switch {
		case strings.HasSuffix(f.Schema.Custom, ":gh-epic-link"):
			ef.link = f.ID
		case strings.HasSuffix(f.Schema.Custom, ":gh-epic-label"):
			ef.name = f.ID
		}
	}
	return ef
}

func loadEpicFields(jira *atlassian.Client) (epicFields, error) {
	var fields []fieldDef
	if _, err := jira.Get("/rest/api/2/field", nil, &fields); err != nil {
		return epicFields{}, err
	}
	return findEpicFields(fields), nil
}

// jqlClause names a custom field by id, as cf[10008], which works whatever the
// field is called.
func jqlClause(fieldID string) string {
	if n, ok := strings.CutPrefix(fieldID, "customfield_"); ok {
		return "cf[" + n + "]"
	}
	return fieldID
}

type epicCandidate struct {
	raw      []byte
	ref      issueRef
	epicLink string
	parent   string
	isEpic   bool
}

func fetchEpicCandidate(jira *atlassian.Client, key string, ef epicFields) (*epicCandidate, error) {
	fields := []string{"summary", "status", "issuetype", "parent"}
	for _, id := range []string{ef.link, ef.name} {
		if id != "" {
			fields = append(fields, id)
		}
	}
	var c epicCandidate
	var extra struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	raw, err := jira.Get("/rest/api/2/issue/"+url.PathEscape(key), url.Values{"fields": {strings.Join(fields, ",")}}, &c.ref)
	if err != nil {
		return nil, err
	}
	c.raw = raw
	if json.Unmarshal(raw, &extra) == nil && ef.link != "" {
		_ = json.Unmarshal(extra.Fields[ef.link], &c.epicLink)
	}
	// Only epics have an Epic Name, which also catches a type named "Эпик".
	var epicName string
	if ef.name != "" && json.Unmarshal(extra.Fields[ef.name], &epicName) == nil && epicName != "" {
		c.isEpic = true
	}
	if strings.EqualFold(atlassian.NameOf(c.ref.Fields.IssueType), "epic") {
		c.isEpic = true
	}
	var parent struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(extra.Fields["parent"], &parent) == nil {
		c.parent = parent.Key
	}
	return &c, nil
}

// resolveEpic walks from an issue to its epic: its Epic Link, or its parent's
// when it is a subtask. An issue with neither is taken as the epic itself, since
// localized instances do not call the type "Epic".
func resolveEpic(jira *atlassian.Client, key string, ef epicFields, stderr io.Writer) (*epicCandidate, error) {
	c, err := fetchEpicCandidate(jira, key, ef)
	if err != nil {
		return nil, err
	}
	kind := cli.Or(atlassian.NameOf(c.ref.Fields.IssueType), "issue")
	switch {
	case c.isEpic:
		return c, nil
	case c.epicLink != "":
		fmt.Fprintf(stderr, "atl: %s is a %s; showing its epic %s\n", c.ref.Key, kind, c.epicLink)
		return fetchEpicCandidate(jira, c.epicLink, ef)
	case c.parent != "":
		p, err := fetchEpicCandidate(jira, c.parent, ef)
		if err != nil {
			return nil, err
		}
		if p.epicLink != "" {
			fmt.Fprintf(stderr, "atl: %s is a %s; showing its parent %s's epic %s\n", c.ref.Key, kind, p.ref.Key, p.epicLink)
			return fetchEpicCandidate(jira, p.epicLink, ef)
		}
		fmt.Fprintf(stderr, "atl: %s is a %s; showing its parent %s\n", c.ref.Key, kind, p.ref.Key)
		return p, nil
	default:
		fmt.Fprintf(stderr, "atl: %s has no epic or parent; listing its own children\n", c.ref.Key)
		return c, nil
	}
}

// epicChildren lists the issues under key. Jira rejects a whole query when one
// clause is unusable (Epic Link on a key that is not an epic, or a field the
// project cannot see), so each clause is retried alone before giving up.
func epicChildren(jira *atlassian.Client, key, linkField string, limit int, stderr io.Writer) (string, []byte, searchResult, error) {
	clauses := []string{"parent = " + key}
	if linkField != "" {
		clauses = []string{jqlClause(linkField) + " = " + key, "parent = " + key}
	}
	queries := []string{strings.Join(clauses, " OR ")}
	if len(clauses) > 1 {
		queries = append(queries, clauses...)
	}
	var firstErr error
	for _, q := range queries {
		jql := q + " ORDER BY key"
		raw, res, err := searchIssues(jira, jql, limit)
		if err == nil {
			if firstErr != nil {
				fmt.Fprintf(stderr, "atl: Jira rejected the full query (%v); showing only: %s\n", firstErr, q)
			}
			return jql, raw, res, nil
		}
		if e, ok := errors.AsType[*cli.Error](err); !ok || e.Code != cli.ExitUsage {
			return "", nil, searchResult{}, err
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return "", nil, searchResult{}, firstErr
}

var statusGroups = []struct{ key, label string }{
	{"new", "To do"},
	{"indeterminate", "In progress"},
	{"done", "Done"},
}

func statusGroup(r *issueRef) string {
	if r.Fields.Status != nil {
		for _, g := range statusGroups {
			if r.Fields.Status.StatusCategory.Key == g.key {
				return g.label
			}
		}
	}
	return "Other"
}

// RunEpic implements `atl epic`.
func RunEpic(args []string, stdout, stderr io.Writer) error {
	fs := cli.NewFlags("epic", "[KEY]", "The issues under an epic, grouped by status.\nKEY defaults to the current branch's issue; a non-epic resolves to its epic or parent.", stderr)
	limit := fs.Int("limit", 50, "maximum child issues")
	asJSON := fs.Bool("json", false, "print the raw API responses")
	pos, err := cli.ParseFlags(fs, args)
	if err != nil {
		return err
	}
	key, err := cli.KeyArg(pos, "epic", stderr)
	if err != nil {
		return err
	}
	jira, err := atlassian.NewClient("jira")
	if err != nil {
		return err
	}
	ef, err := loadEpicFields(jira)
	if err != nil {
		return err
	}
	epic, err := resolveEpic(jira, key, ef, stderr)
	if err != nil {
		return err
	}
	jql, raw, res, err := epicChildren(jira, epic.ref.Key, ef.link, *limit, stderr)
	if err != nil {
		return err
	}
	if *asJSON {
		combined, err := json.Marshal(map[string]json.RawMessage{"epic": epic.raw, "children": raw})
		if err != nil {
			return cli.Fail(cli.ExitAPI, "encoding JSON: %v", err)
		}
		return cli.WriteJSON(stdout, combined)
	}

	groups := map[string][]*issueRef{}
	for i := range res.Issues {
		g := statusGroup(&res.Issues[i])
		groups[g] = append(groups[g], &res.Issues[i])
	}
	order := []string{"To do", "In progress", "Done", "Other"}
	var counts []string
	for _, o := range slices.Backward(order) {
		if n := len(groups[o]); n > 0 {
			counts = append(counts, fmt.Sprintf("%d %s", n, strings.ToLower(o)))
		}
	}

	// The epic's own description lives in `atl issue`; say so, since agents
	// otherwise take this header for all there is.
	detail := fmt.Sprintf("(details and description: atl issue %s)\n", epic.ref.Key)
	fmt.Fprintf(stdout, "# %s\n\n", issueLine(&epic.ref))
	if len(res.Issues) == 0 {
		fmt.Fprintf(stdout, "no child issues for: %s\n%s", jql, detail)
		return nil
	}
	fmt.Fprintf(stdout, "%d issues: %s\n", res.Total, strings.Join(counts, ", "))
	fmt.Fprintf(stdout, "URL: %s/browse/%s\n", jira.Base, epic.ref.Key)
	fmt.Fprint(stdout, detail)
	for _, g := range order {
		if len(groups[g]) == 0 {
			continue
		}
		fmt.Fprintf(stdout, "\n## %s (%d)\n\n", g, len(groups[g]))
		for _, r := range groups[g] {
			fmt.Fprintf(stdout, "- %s\n", searchLine(r))
		}
	}
	if res.Total > len(res.Issues) {
		fmt.Fprintf(stdout, "\ncounts cover the first %d of %d; raise --limit for all\n", len(res.Issues), res.Total)
	}
	return nil
}
