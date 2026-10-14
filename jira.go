package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

const confluenceApp = "com.atlassian.confluence"

var (
	keyRE      = regexp.MustCompile(`[A-Z][A-Z0-9]+-\d+`)
	looseKeyRE = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]+-\d+`)
	urlRE      = regexp.MustCompile(`https?://[^\s|\[\]<>"'{}]+`)
)

type named struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

type status struct {
	Name           string `json:"name"`
	StatusCategory struct {
		Key string `json:"key"`
	} `json:"statusCategory"`
}

type issueRef struct {
	Key    string `json:"key"`
	Fields struct {
		Summary   string  `json:"summary"`
		Status    *status `json:"status"`
		IssueType *named  `json:"issuetype"`
		Assignee  *named  `json:"assignee"`
		Updated   string  `json:"updated"`
	} `json:"fields"`
}

type issueLink struct {
	Type struct {
		Name    string `json:"name"`
		Inward  string `json:"inward"`
		Outward string `json:"outward"`
	} `json:"type"`
	InwardIssue  *issueRef `json:"inwardIssue"`
	OutwardIssue *issueRef `json:"outwardIssue"`
}

type comment struct {
	ID      string `json:"id"`
	Author  *named `json:"author"`
	Created string `json:"created"`
	Body    string `json:"body"`
}

type issue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary     string       `json:"summary"`
		Description string       `json:"description"`
		Created     string       `json:"created"`
		Updated     string       `json:"updated"`
		Status      *status      `json:"status"`
		IssueType   *named       `json:"issuetype"`
		Priority    *named       `json:"priority"`
		Resolution  *named       `json:"resolution"`
		Assignee    *named       `json:"assignee"`
		Reporter    *named       `json:"reporter"`
		Labels      []string     `json:"labels"`
		Components  []named      `json:"components"`
		FixVersions []named      `json:"fixVersions"`
		IssueLinks  []issueLink  `json:"issuelinks"`
		Subtasks    []issueRef   `json:"subtasks"`
		Parent      *issueRef    `json:"parent"`
		Attachment  []attachment `json:"attachment"`
		Comment     struct {
			Comments []comment `json:"comments"`
		} `json:"comment"`
	} `json:"fields"`
}

type remoteLink struct {
	Object struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	} `json:"object"`
	Application struct {
		Type string `json:"type"`
	} `json:"application"`
}

type ref struct{ title, url, where string }

func nameOf(n *named) string {
	if n == nil {
		return ""
	}
	if n.DisplayName != "" {
		return n.DisplayName
	}
	return n.Name
}

func keyFromBranchName(branch string) (string, bool) {
	if m := keyRE.FindString(branch); m != "" {
		return m, true
	}
	if m := looseKeyRE.FindString(branch); m != "" {
		return strings.ToUpper(m), true
	}
	return "", false
}

func keyFromBranch(stderr io.Writer) (string, error) {
	out, err := exec.Command("git", "branch", "--show-current").Output()
	if err != nil {
		return "", fail(exitUsage, "no issue key given and not inside a git repository")
	}
	branch := strings.TrimSpace(string(out))
	key, ok := keyFromBranchName(branch)
	if !ok {
		if branch == "" {
			branch = "(detached HEAD)"
		}
		return "", fail(exitUsage, "no issue key in branch %q; pass one explicitly", branch)
	}
	fmt.Fprintf(stderr, "atl: %s from branch %s\n", key, branch)
	return key, nil
}

// keyArg takes an explicit issue key or falls back to the current branch's.
func keyArg(pos []string, cmd string, stderr io.Writer) (string, error) {
	switch len(pos) {
	case 0:
		return keyFromBranch(stderr)
	case 1:
		return strings.ToUpper(pos[0]), nil
	default:
		return "", fail(exitUsage, "%s takes at most one key", cmd)
	}
}

func (l issueLink) relation() (string, *issueRef) {
	if l.OutwardIssue != nil {
		return l.Type.Outward, l.OutwardIssue
	}
	return l.Type.Inward, l.InwardIssue
}

func issueLine(r *issueRef) string {
	st, tail := "?", ""
	if r.Fields.Status != nil {
		st = r.Fields.Status.Name
	}
	if t := nameOf(r.Fields.IssueType); t != "" {
		tail = " (" + t + ")"
	}
	return fmt.Sprintf("%s [%s] %s%s", r.Key, st, r.Fields.Summary, tail)
}

// confluenceRefs lists the pages an issue points at: Jira records a page that
// mentions the issue as a remote link, and people paste URLs into text.
func confluenceRefs(is *issue, links []remoteLink, confBase string) (pages, others []ref) {
	seen := map[string]bool{}
	add := func(r ref) {
		if !seen[r.url] {
			seen[r.url] = true
			pages = append(pages, r)
		}
	}
	for _, rl := range links {
		u := rl.Object.URL
		switch {
		case rl.Application.Type == confluenceApp || (confBase != "" && strings.HasPrefix(u, confBase)):
			add(ref{rl.Object.Title, u, "remote link"})
		case u != "":
			others = append(others, ref{title: rl.Object.Title, url: u})
		}
	}
	if confBase == "" {
		return pages, others
	}
	texts := []ref{{where: "description", title: is.Fields.Description}}
	for _, c := range is.Fields.Comment.Comments {
		texts = append(texts, ref{where: "comment", title: c.Body})
	}
	for _, t := range texts {
		for _, u := range urlRE.FindAllString(t.title, -1) {
			u = strings.TrimRight(u, ".,;:)")
			if strings.HasPrefix(u, confBase) {
				add(ref{url: u, where: t.where})
			}
		}
	}
	return pages, others
}

func cmdIssue(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("issue", "[KEY]", "A Jira issue with its links, subtasks and Confluence references.\nKEY defaults to the issue key in the current git branch.", stderr)
	comments := fs.Bool("comments", false, "include comment bodies")
	asJSON := fs.Bool("json", false, "print the raw API responses")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	key, err := keyArg(pos, "issue", stderr)
	if err != nil {
		return err
	}

	jira, err := newClient("jira")
	if err != nil {
		return err
	}
	var allFields []fieldDef
	if _, err := jira.get("/rest/api/2/field", nil, &allFields); err != nil {
		return err
	}
	epicField := ""
	for _, f := range allFields {
		if f.Name == "Epic Link" {
			epicField = f.ID
		}
	}

	var is issue
	path := "/rest/api/2/issue/" + url.PathEscape(key)
	// Custom field ids can number in the hundreds, too many to list in a URL.
	rawIssue, err := jira.get(path, url.Values{"fields": {"*all"}, "expand": {"renderedFields"}}, &is)
	if err != nil {
		return err
	}
	var extra struct {
		Fields         map[string]json.RawMessage `json:"fields"`
		RenderedFields map[string]json.RawMessage `json:"renderedFields"`
	}
	if err := json.Unmarshal(rawIssue, &extra); err != nil {
		return fail(exitAPI, "jira: decoding %s: %v", path, err)
	}
	var links []remoteLink
	rawLinks, linkErr := jira.get(path+"/remotelink", nil, &links)

	if *asJSON {
		if linkErr != nil {
			rawLinks = []byte("null")
		}
		combined, _ := json.Marshal(map[string]json.RawMessage{"issue": rawIssue, "remotelinks": rawLinks})
		return writeJSON(stdout, combined)
	}

	f := is.Fields
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	line("# %s: %s\n", is.Key, f.Summary)
	var head []string
	if t := nameOf(f.IssueType); t != "" {
		head = append(head, "Type: "+t)
	}
	if f.Status != nil {
		head = append(head, "Status: "+f.Status.Name)
	}
	if p := nameOf(f.Priority); p != "" {
		head = append(head, "Priority: "+p)
	}
	resolution := nameOf(f.Resolution)
	if resolution == "" {
		resolution = "Unresolved"
	}
	line("%s", strings.Join(append(head, "Resolution: "+resolution), " · "))
	assignee := nameOf(f.Assignee)
	if assignee == "" {
		assignee = "Unassigned"
	}
	line("Assignee: %s · Reporter: %s", assignee, or(nameOf(f.Reporter), "?"))
	line("Created: %s · Updated: %s", shortDate(f.Created), shortDate(f.Updated))
	if f.Parent != nil {
		line("Parent: %s", issueLine(f.Parent))
	}
	if epicField != "" {
		var epic string
		if json.Unmarshal(extra.Fields[epicField], &epic) == nil && epic != "" {
			var e issueRef
			// The epic's summary is context, not essential: show the bare key if it cannot be read.
			if _, err := jira.get("/rest/api/2/issue/"+url.PathEscape(epic), url.Values{"fields": {"summary,status,issuetype"}}, &e); err == nil {
				epic = issueLine(&e)
			}
			line("Epic: %s", epic)
		}
	}
	listLine := func(label string, values []string) {
		if len(values) > 0 {
			line("%s: %s", label, strings.Join(values, ", "))
		}
	}
	listLine("Labels", f.Labels)
	listLine("Components", names(f.Components))
	listLine("Fix versions", names(f.FixVersions))
	line("URL: %s/browse/%s", jira.base, is.Key)

	type rel struct {
		name  string
		issue *issueRef
	}
	var rels []rel
	var blockers []string
	for _, l := range f.IssueLinks {
		name, other := l.relation()
		if other == nil {
			continue
		}
		rels = append(rels, rel{name, other})
		done := other.Fields.Status != nil && other.Fields.Status.StatusCategory.Key == "done"
		if strings.Contains(strings.ToLower(name), "blocked by") && !done {
			blockers = append(blockers, other.Key)
		}
	}
	listLine("Open blockers", blockers)

	shortFields, longFields := customFields(allFields, extra.Fields, extra.RenderedFields, epicField)
	if len(shortFields) > 0 {
		line("\n## Fields\n")
		for _, fv := range shortFields {
			line("- %s: %s", fv.name, fv.value)
		}
	}

	line("\n## Description\n")
	line("%s", or(demoteHeadings(richText(extra.RenderedFields["description"], f.Description), 3), "(empty)"))
	for _, fv := range longFields {
		line("\n## %s\n", fv.name)
		line("%s", demoteHeadings(fv.value, 3))
	}

	if len(rels) > 0 {
		sort.SliceStable(rels, func(i, j int) bool {
			bi, bj := strings.Contains(strings.ToLower(rels[i].name), "block"), strings.Contains(strings.ToLower(rels[j].name), "block")
			if bi != bj {
				return bi
			}
			return rels[i].name < rels[j].name
		})
		line("\n## Linked issues\n")
		for _, r := range rels {
			line("- %s %s", r.name, issueLine(r.issue))
		}
	}
	if len(f.Subtasks) > 0 {
		line("\n## Subtasks\n")
		for i := range f.Subtasks {
			line("- %s", issueLine(&f.Subtasks[i]))
		}
	}

	writeAttachments(&b, f.Attachment, "atl attachment "+is.Key+" for all, or atl attachment <id>")

	pages, others := confluenceRefs(&is, links, optionalBaseURL("confluence"))
	writeRefs := func(heading string, refs []ref) {
		if len(refs) == 0 {
			return
		}
		line("\n## %s\n", heading)
		for _, r := range refs {
			s := r.url
			if r.title != "" {
				s = r.title + " — " + s
			}
			if r.where != "" {
				s += " (" + r.where + ")"
			}
			line("- %s", s)
		}
	}
	writeRefs("Confluence", pages)
	writeRefs("Other links", others)
	if linkErr != nil {
		line("\n(remote links unavailable: %v)", linkErr)
	}

	cs := f.Comment.Comments
	switch {
	case *comments:
		var rendered struct {
			Comments []struct {
				ID   string          `json:"id"`
				Body json.RawMessage `json:"body"`
			} `json:"comments"`
		}
		_ = json.Unmarshal(extra.RenderedFields["comment"], &rendered)
		renderedBody := map[string]json.RawMessage{}
		for _, rc := range rendered.Comments {
			renderedBody[rc.ID] = rc.Body
		}
		line("\n## Comments (%d)", len(cs))
		for _, c := range cs {
			line("\n### %s — %s\n", or(nameOf(c.Author), "?"), shortDate(c.Created))
			line("%s", demoteHeadings(richText(renderedBody[c.ID], c.Body), 4))
		}
	case len(cs) > 0:
		line("\n(%d comments; add --comments to read them)", len(cs))
	}

	_, err = io.WriteString(stdout, b.String())
	return err
}

func names(ns []named) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.Name)
	}
	return out
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
