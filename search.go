package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

const searchFields = "summary,status,issuetype,priority,assignee,updated"

type searchResult struct {
	Total  int        `json:"total"`
	Issues []issueRef `json:"issues"`
}

func searchLine(r *issueRef) string {
	s := issueLine(r)
	if a := nameOf(r.Fields.Assignee); a != "" {
		s += " · " + a
	}
	if r.Fields.Updated != "" {
		s += " · updated " + shortDate(r.Fields.Updated)
	}
	return s
}

func searchIssues(jira *client, jql string, limit int) ([]byte, searchResult, error) {
	var res searchResult
	params := url.Values{"jql": {jql}, "maxResults": {strconv.Itoa(limit)}, "fields": {searchFields}}
	raw, err := jira.get("/rest/api/2/search", params, &res)
	return raw, res, err
}

func truncationNote(w io.Writer, shown, total int) {
	if total > shown {
		fmt.Fprintf(w, "\nshowing %d of %d; raise --limit or narrow the query\n", shown, total)
	}
}

func cmdJQL(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("jql", "<query>", "Jira issues matching a JQL query. A query Jira rejects exits 2 with its message.", stderr)
	limit := fs.Int("limit", 20, "maximum results")
	asJSON := fs.Bool("json", false, "print the raw API response")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return fail(exitUsage, "jql needs a query")
	}
	jql := strings.Join(pos, " ")
	jira, err := newClient("jira")
	if err != nil {
		return err
	}
	raw, res, err := searchIssues(jira, jql, *limit)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, raw)
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

func epicLinkField(jira *client) (string, error) {
	var fields []fieldDef
	if _, err := jira.get("/rest/api/2/field", nil, &fields); err != nil {
		return "", err
	}
	for _, f := range fields {
		if f.Name == "Epic Link" {
			return f.ID, nil
		}
	}
	return "", nil
}

type epicCandidate struct {
	raw      []byte
	ref      issueRef
	epicLink string
	parent   string
}

func fetchEpicCandidate(jira *client, key, epicField string) (*epicCandidate, error) {
	fields := "summary,status,issuetype,parent"
	if epicField != "" {
		fields += "," + epicField
	}
	var c epicCandidate
	var extra struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	raw, err := jira.get("/rest/api/2/issue/"+url.PathEscape(key), url.Values{"fields": {fields}}, &c.ref)
	if err != nil {
		return nil, err
	}
	c.raw = raw
	if json.Unmarshal(raw, &extra) == nil && epicField != "" {
		_ = json.Unmarshal(extra.Fields[epicField], &c.epicLink)
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
func resolveEpic(jira *client, key, epicField string, stderr io.Writer) (*epicCandidate, error) {
	c, err := fetchEpicCandidate(jira, key, epicField)
	if err != nil {
		return nil, err
	}
	kind := or(nameOf(c.ref.Fields.IssueType), "issue")
	switch {
	case strings.EqualFold(kind, "epic"):
		return c, nil
	case c.epicLink != "":
		fmt.Fprintf(stderr, "atl: %s is a %s; showing its epic %s\n", c.ref.Key, kind, c.epicLink)
		return fetchEpicCandidate(jira, c.epicLink, epicField)
	case c.parent != "":
		p, err := fetchEpicCandidate(jira, c.parent, epicField)
		if err != nil {
			return nil, err
		}
		if p.epicLink != "" {
			fmt.Fprintf(stderr, "atl: %s is a %s; showing its parent %s's epic %s\n", c.ref.Key, kind, p.ref.Key, p.epicLink)
			return fetchEpicCandidate(jira, p.epicLink, epicField)
		}
		fmt.Fprintf(stderr, "atl: %s is a %s; showing its parent %s\n", c.ref.Key, kind, p.ref.Key)
		return p, nil
	default:
		fmt.Fprintf(stderr, "atl: %s has no epic or parent; listing its own children\n", c.ref.Key)
		return c, nil
	}
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

func cmdEpic(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("epic", "[KEY]", "The issues under an epic, grouped by status.\nKEY defaults to the current branch's issue; a non-epic resolves to its epic or parent.", stderr)
	limit := fs.Int("limit", 50, "maximum child issues")
	asJSON := fs.Bool("json", false, "print the raw API responses")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	key, err := keyArg(pos, "epic", stderr)
	if err != nil {
		return err
	}
	jira, err := newClient("jira")
	if err != nil {
		return err
	}
	epicField, err := epicLinkField(jira)
	if err != nil {
		return err
	}
	epic, err := resolveEpic(jira, key, epicField, stderr)
	if err != nil {
		return err
	}
	jql := fmt.Sprintf("parent = %s ORDER BY key", epic.ref.Key)
	if epicField != "" {
		jql = fmt.Sprintf(`"Epic Link" = %[1]s OR parent = %[1]s ORDER BY key`, epic.ref.Key)
	}
	raw, res, err := searchIssues(jira, jql, *limit)
	if err != nil {
		return err
	}
	if *asJSON {
		combined, err := json.Marshal(map[string]json.RawMessage{"epic": epic.raw, "children": raw})
		if err != nil {
			return fail(exitAPI, "encoding JSON: %v", err)
		}
		return writeJSON(stdout, combined)
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

	fmt.Fprintf(stdout, "# %s\n\n", issueLine(&epic.ref))
	if len(res.Issues) == 0 {
		fmt.Fprintf(stdout, "no child issues for: %s\n", jql)
		return nil
	}
	fmt.Fprintf(stdout, "%d issues: %s\n", res.Total, strings.Join(counts, ", "))
	fmt.Fprintf(stdout, "URL: %s/browse/%s\n", jira.base, epic.ref.Key)
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
