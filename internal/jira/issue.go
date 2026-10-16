// Package jira implements the Jira commands: issue, jql, epic and history.
package jira

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/RyabovNick/atl/internal/atlassian"
	"github.com/RyabovNick/atl/internal/attachment"
	"github.com/RyabovNick/atl/internal/cli"
	"github.com/RyabovNick/atl/internal/markdown"
)

var urlRE = regexp.MustCompile(`https?://[^\s|\[\]<>"'{}]+`)

const confluenceApp = "com.atlassian.confluence"

type status struct {
	Name           string `json:"name"`
	StatusCategory struct {
		Key string `json:"key"`
	} `json:"statusCategory"`
}

type issueRef struct {
	Key    string `json:"key"`
	Fields struct {
		Summary   string           `json:"summary"`
		Status    *status          `json:"status"`
		IssueType *atlassian.Named `json:"issuetype"`
		Assignee  *atlassian.Named `json:"assignee"`
		Updated   string           `json:"updated"`
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
	ID      string           `json:"id"`
	Author  *atlassian.Named `json:"author"`
	Created string           `json:"created"`
	Body    string           `json:"body"`
}

type issue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary     string                  `json:"summary"`
		Description string                  `json:"description"`
		Created     string                  `json:"created"`
		Updated     string                  `json:"updated"`
		Status      *status                 `json:"status"`
		IssueType   *atlassian.Named        `json:"issuetype"`
		Priority    *atlassian.Named        `json:"priority"`
		Resolution  *atlassian.Named        `json:"resolution"`
		Assignee    *atlassian.Named        `json:"assignee"`
		Reporter    *atlassian.Named        `json:"reporter"`
		Labels      []string                `json:"labels"`
		Components  []atlassian.Named       `json:"components"`
		FixVersions []atlassian.Named       `json:"fixVersions"`
		IssueLinks  []issueLink             `json:"issuelinks"`
		Subtasks    []issueRef              `json:"subtasks"`
		Parent      *issueRef               `json:"parent"`
		Attachment  []attachment.Attachment `json:"attachment"`
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
	if t := atlassian.NameOf(r.Fields.IssueType); t != "" {
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

// RunIssue implements `atl issue`.
func RunIssue(args []string, stdout, stderr io.Writer) error {
	fs := cli.NewFlags("issue", "[KEY]", "A Jira issue with its links, subtasks and Confluence references.\nKEY defaults to the issue key in the current git branch.", stderr)
	comments := fs.Bool("comments", false, "include comment bodies")
	asJSON := fs.Bool("json", false, "print the raw API responses")
	pos, err := cli.ParseFlags(fs, args)
	if err != nil {
		return err
	}
	key, err := cli.KeyArg(pos, "issue", stderr)
	if err != nil {
		return err
	}

	jira, err := atlassian.NewClient("jira")
	if err != nil {
		return err
	}
	var allFields []fieldDef
	if _, err = jira.Get("/rest/api/2/field", nil, &allFields); err != nil {
		return err
	}
	epicField := findEpicFields(allFields).link

	var is issue
	path := "/rest/api/2/issue/" + url.PathEscape(key)
	// Custom field ids can number in the hundreds, too many to list in a URL.
	rawIssue, err := jira.Get(path, url.Values{"fields": {"*all"}, "expand": {"renderedFields"}}, &is)
	if err != nil {
		return err
	}
	var extra struct {
		Fields         map[string]json.RawMessage `json:"fields"`
		RenderedFields map[string]json.RawMessage `json:"renderedFields"`
	}
	if err = json.Unmarshal(rawIssue, &extra); err != nil {
		return cli.Fail(cli.ExitAPI, "jira: decoding %s: %v", path, err)
	}
	var links []remoteLink
	rawLinks, linkErr := jira.Get(path+"/remotelink", nil, &links)

	if *asJSON {
		if linkErr != nil {
			rawLinks = []byte("null")
		}
		combined, jsonErr := json.Marshal(map[string]json.RawMessage{"issue": rawIssue, "remotelinks": rawLinks})
		if jsonErr != nil {
			return cli.Fail(cli.ExitAPI, "encoding JSON: %v", jsonErr)
		}
		return cli.WriteJSON(stdout, combined)
	}

	f := is.Fields
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	line("# %s: %s\n", is.Key, f.Summary)
	var head []string
	if t := atlassian.NameOf(f.IssueType); t != "" {
		head = append(head, "Type: "+t)
	}
	if f.Status != nil {
		head = append(head, "Status: "+f.Status.Name)
	}
	if p := atlassian.NameOf(f.Priority); p != "" {
		head = append(head, "Priority: "+p)
	}
	resolution := atlassian.NameOf(f.Resolution)
	if resolution == "" {
		resolution = "Unresolved"
	}
	line("%s", strings.Join(append(head, "Resolution: "+resolution), " · "))
	assignee := atlassian.NameOf(f.Assignee)
	if assignee == "" {
		assignee = "Unassigned"
	}
	line("Assignee: %s · Reporter: %s", assignee, cli.Or(atlassian.NameOf(f.Reporter), "?"))
	line("Created: %s · Updated: %s", atlassian.ShortDate(f.Created), atlassian.ShortDate(f.Updated))
	if f.Parent != nil {
		line("Parent: %s", issueLine(f.Parent))
	}
	if epicField != "" {
		var epic string
		if json.Unmarshal(extra.Fields[epicField], &epic) == nil && epic != "" {
			var e issueRef
			// The epic's summary is context, not essential: show the bare key if it cannot be read.
			if _, epicErr := jira.Get("/rest/api/2/issue/"+url.PathEscape(epic), url.Values{"fields": {"summary,status,issuetype"}}, &e); epicErr == nil {
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
	line("URL: %s/browse/%s", jira.Base, is.Key)

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
	line("%s", cli.Or(markdown.DemoteHeadings(richText(extra.RenderedFields["description"], f.Description), 3), "(empty)"))
	for _, fv := range longFields {
		line("\n## %s\n", fv.name)
		line("%s", markdown.DemoteHeadings(fv.value, 3))
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

	attachment.Write(&b, f.Attachment, "atl attachment "+is.Key+" for all, or atl attachment <id>")

	pages, others := confluenceRefs(&is, links, atlassian.OptionalBaseURL("confluence"))
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
			line("\n### %s — %s\n", cli.Or(atlassian.NameOf(c.Author), "?"), atlassian.ShortDate(c.Created))
			line("%s", markdown.DemoteHeadings(richText(renderedBody[c.ID], c.Body), 4))
		}
	case len(cs) > 0:
		line("\n(%d comments; add --comments to read them)", len(cs))
	}

	_, err = io.WriteString(stdout, b.String())
	return err
}

func names(ns []atlassian.Named) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.Name)
	}
	return out
}
