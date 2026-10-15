// Package confluence implements the Confluence commands: page and search.
package confluence

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/RyabovNick/atl/internal/atlassian"
	"github.com/RyabovNick/atl/internal/attachment"
	"github.com/RyabovNick/atl/internal/cli"
	"github.com/RyabovNick/atl/internal/markdown"
)

var (
	pagesPathRE   = regexp.MustCompile(`/pages/(\d+)`)
	tinyPathRE    = regexp.MustCompile(`/x/([A-Za-z0-9_-]+)`)
	displayPathRE = regexp.MustCompile(`/display/([^/]+)/(.+)$`)
)

type page struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Space struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"space"`
	Version struct {
		Number int              `json:"number"`
		When   string           `json:"when"`
		By     *atlassian.Named `json:"by"`
	} `json:"version"`
	Ancestors []struct {
		Title string `json:"title"`
	} `json:"ancestors"`
	Body struct {
		View struct {
			Value string `json:"value"`
		} `json:"view"`
	} `json:"body"`
	Links struct {
		WebUI string `json:"webui"`
	} `json:"_links"`
}

// tinyToPageID decodes a /x/<code> link: the page id as 8 little-endian bytes,
// base64 with '/' and '+' swapped for '-' and '_' and trailing 'A' and '=' removed.
func tinyToPageID(code string) (string, error) {
	s := strings.NewReplacer("-", "/", "_", "+").Replace(code)
	if len(s) > 11 {
		return "", fmt.Errorf("tiny code %q is too long", code)
	}
	raw, err := base64.StdEncoding.DecodeString(s + strings.Repeat("A", 11-len(s)) + "=")
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(binary.LittleEndian.Uint64(raw), 10), nil
}

func pageIDFromRef(conf *atlassian.Client, refStr string) (string, error) {
	refStr = strings.TrimSpace(refStr)
	if _, err := strconv.ParseUint(refStr, 10, 64); err == nil {
		return refStr, nil
	}
	u, err := url.Parse(refStr)
	if err != nil {
		return "", cli.Fail(cli.ExitUsage, "cannot parse %q as a page id or URL", refStr)
	}
	if id := u.Query().Get("pageId"); id != "" {
		return id, nil
	}
	if m := pagesPathRE.FindStringSubmatch(u.Path); m != nil {
		return m[1], nil
	}
	if m := tinyPathRE.FindStringSubmatch(u.Path); m != nil {
		return resolveTiny(conf, m[1])
	}
	if m := displayPathRE.FindStringSubmatch(u.Path); m != nil {
		return resolveDisplay(conf, m[1], strings.Split(m[2], "/"))
	}
	return "", cli.Fail(cli.ExitUsage, "cannot find a page id in %q", refStr)
}

// resolveTiny trusts the decoded id only once the page exists; otherwise it asks
// Confluence where the link redirects, which works whatever the encoding.
func resolveTiny(conf *atlassian.Client, code string) (string, error) {
	if id, err := tinyToPageID(code); err == nil {
		if _, err := conf.Get("/rest/api/content/"+id, nil, nil); err == nil {
			return id, nil
		}
	}
	resp, _, err := conf.Raw("/x/"+code, nil)
	if err != nil {
		return "", err
	}
	if loc, err := url.Parse(resp.Header.Get("Location")); err == nil {
		if id := loc.Query().Get("pageId"); id != "" {
			return id, nil
		}
		if m := pagesPathRE.FindStringSubmatch(loc.Path); m != nil {
			return m[1], nil
		}
	}
	return "", cli.Fail(cli.ExitNotFound, "cannot resolve tiny link /x/%s", code)
}

func resolveDisplay(conf *atlassian.Client, space string, parts []string) (string, error) {
	title, err := url.PathUnescape(strings.ReplaceAll(parts[len(parts)-1], "+", " "))
	if err != nil {
		return "", cli.Fail(cli.ExitUsage, "bad page title in URL: %v", err)
	}
	params := url.Values{"spaceKey": {space}, "title": {title}, "type": {"page"}}
	if len(parts) == 4 && isDigits(parts[0]) && isDigits(parts[1]) && isDigits(parts[2]) {
		params.Set("type", "blogpost")
		params.Set("postingDay", strings.Join(parts[:3], "-"))
	}
	var res struct {
		Results []page `json:"results"`
	}
	if _, err := conf.Get("/rest/api/content", params, &res); err != nil {
		return "", err
	}
	if len(res.Results) == 0 {
		return "", cli.Fail(cli.ExitNotFound, "no page titled %q in space %s", title, space)
	}
	return res.Results[0].ID, nil
}

func isDigits(s string) bool {
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

const childPageBatch = 50

// maxChildPages bounds a --children walk; a var so tests can lower it.
var maxChildPages = 500

type childPage struct {
	page
	children []childPage
}

func (c childPage) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"id": c.ID, "title": c.Title, "updated": c.Version.When, "webui": c.Links.WebUI, "children": c.children,
	})
}

// childPages lists a page's children depth levels down, at most *budget pages
// in total; truncated reports that the budget ran out before the tree did.
func childPages(conf *atlassian.Client, id string, depth int, budget *int) (kids []childPage, truncated bool, err error) {
	for start := 0; ; {
		if *budget <= 0 {
			return kids, true, nil
		}
		limit := min(childPageBatch, *budget)
		var res struct {
			Results []page `json:"results"`
			Links   struct {
				Next string `json:"next"`
			} `json:"_links"`
		}
		params := url.Values{"expand": {"version"}, "limit": {strconv.Itoa(limit)}, "start": {strconv.Itoa(start)}}
		if _, err := conf.Get("/rest/api/content/"+id+"/child/page", params, &res); err != nil {
			return nil, false, err
		}
		for _, r := range res.Results {
			kids = append(kids, childPage{page: r})
		}
		*budget -= len(res.Results)
		start += len(res.Results)
		if len(res.Results) == 0 || (res.Links.Next == "" && len(res.Results) < limit) {
			break
		}
	}
	if depth > 1 {
		for i := range kids {
			sub, cut, err := childPages(conf, kids[i].ID, depth-1, budget)
			if err != nil {
				return nil, false, err
			}
			kids[i].children = sub
			if cut {
				return kids, true, nil
			}
		}
	}
	return kids, false, nil
}

func countPages(ps []childPage) int {
	n := len(ps)
	for _, p := range ps {
		n += countPages(p.children)
	}
	return n
}

// pageURL links to a page by id. Confluence's own web links end in the
// percent-encoded title, which for non-Latin titles gets long enough that
// agents trim it and hand out links that no longer open.
func pageURL(base, id string) string {
	return base + "/pages/viewpage.action?pageId=" + url.QueryEscape(id)
}

func writeChildren(b *strings.Builder, base string, ps []childPage, indent string) {
	for _, p := range ps {
		fmt.Fprintf(b, "%s- %s — id %s · updated %s\n%s  %s\n", indent, p.Title, p.ID, atlassian.ShortDate(p.Version.When), indent, pageURL(base, p.ID))
		writeChildren(b, base, p.children, indent+"  ")
	}
}

// RunPage implements `atl page`.
func RunPage(args []string, stdout, stderr io.Writer) error {
	fs := cli.NewFlags("page", "<id|url>", `A Confluence page as Markdown. Accepts a page id or any page URL:
viewpage.action?pageId=, /pages/<id>, /display/SPACE/Title, /x/<tiny>.

--toc prints the headings with their sizes instead of the body, and --section
prints only the matching sections. --children lists child pages instead of the
body; add --toc or --section to get those as well.`, stderr)
	maxChars := fs.Int("max-chars", 40000, "truncate the body or sections to this many characters (0 = no limit)")
	toc := fs.Bool("toc", false, "print the outline of headings with section sizes, not the body")
	var sections stringsFlag
	fs.Var(&sections, "section", "print only the section with this heading (repeatable; exact, then prefix, then substring)")
	children := fs.Bool("children", false, "list child pages instead of the body")
	depth := fs.Int("depth", 1, "levels of child pages to list with --children (1-3)")
	asJSON := fs.Bool("json", false, "print the raw API response (with --children, the page and its child tree)")
	pos, err := cli.ParseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return cli.Fail(cli.ExitUsage, "page takes exactly one id or URL")
	}
	if *depth < 1 || *depth > 3 {
		return cli.Fail(cli.ExitUsage, "--depth must be 1, 2 or 3")
	}
	if *asJSON && (*toc || len(sections) > 0) {
		return cli.Fail(cli.ExitUsage, "--json prints the raw page; it does not combine with --toc or --section")
	}
	conf, err := atlassian.NewClient("confluence")
	if err != nil {
		return err
	}
	id, err := pageIDFromRef(conf, pos[0])
	if err != nil {
		return err
	}
	needBody := !*children || *toc || len(sections) > 0
	expand := "space,version,ancestors"
	if needBody {
		expand = "body.view," + expand
	}
	var p page
	raw, err := conf.Get("/rest/api/content/"+id, url.Values{"expand": {expand}}, &p)
	if err != nil {
		return err
	}
	var kids []childPage
	var truncatedKids bool
	if *children {
		budget := maxChildPages
		if kids, truncatedKids, err = childPages(conf, p.ID, *depth, &budget); err != nil {
			return err
		}
	}
	if *asJSON {
		if !*children {
			return cli.WriteJSON(stdout, raw)
		}
		if kids == nil {
			kids = []childPage{}
		}
		combined, jsonErr := json.Marshal(map[string]any{"page": json.RawMessage(raw), "children": kids, "truncated": truncatedKids})
		if jsonErr != nil {
			return cli.Fail(cli.ExitAPI, "encoding JSON: %v", jsonErr)
		}
		return cli.WriteJSON(stdout, combined)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", p.Title)
	fmt.Fprintf(&b, "Space: %s (%s) · id %s\n", cli.Or(p.Space.Name, "?"), cli.Or(p.Space.Key, "?"), p.ID)
	fmt.Fprintf(&b, "Version %d by %s at %s\n", p.Version.Number, cli.Or(atlassian.NameOf(p.Version.By), "?"), atlassian.ShortDate(p.Version.When))
	if len(p.Ancestors) > 0 {
		titles := make([]string, len(p.Ancestors))
		for i, a := range p.Ancestors {
			titles[i] = a.Title
		}
		fmt.Fprintf(&b, "Path: %s\n", strings.Join(titles, " / "))
	}
	fmt.Fprintf(&b, "URL: %s\n\n", pageURL(conf.Base, p.ID))

	if needBody {
		md, convErr := markdown.FromHTML(p.Body.View.Value)
		if convErr != nil {
			return cli.Fail(cli.ExitAPI, "converting page %s to Markdown: %v", id, convErr)
		}
		body := markdown.DemoteHeadings(md, 2)
		lines := strings.Split(body, "\n")
		hs := markdown.ScanHeadings(lines)
		if *toc {
			markdown.WriteTOC(&b, lines, hs)
			fmt.Fprintf(&b, "\n(read one with: atl page %s --section \"<heading>\")\n", p.ID)
		}
		if len(sections) > 0 {
			var parts []string
			for _, q := range sections {
				found, findErr := markdown.FindSections(hs, q)
				if findErr != nil {
					return findErr
				}
				for _, h := range found {
					parts = append(parts, strings.TrimSpace(strings.Join(lines[h.Start:h.End], "\n")))
				}
			}
			if *toc {
				b.WriteString("\n")
			}
			fmt.Fprintln(&b, truncateBody(strings.Join(parts, "\n\n"), *maxChars))
		} else if !*toc {
			fmt.Fprintln(&b, cli.Or(truncateBody(body, *maxChars), "(empty page)"))
		}
		// Attachments are context; a page whose attachments cannot be listed still prints.
		if len(sections) == 0 {
			if atts, attErr := attachment.ForPage(conf, p.ID); attErr != nil {
				fmt.Fprintf(&b, "\n(attachments unavailable: %v)\n", attErr)
			} else {
				attachment.Write(&b, atts, "atl attachment <id>")
			}
		}
	}

	if *children {
		if needBody {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "## Child pages (%d)\n\n", countPages(kids))
		if len(kids) == 0 {
			b.WriteString("(none)\n")
		}
		writeChildren(&b, conf.Base, kids, "")
		if truncatedKids {
			fmt.Fprintf(&b, "\n(stopped at %d pages; list a child page's children instead)\n", maxChildPages)
		}
	}
	_, err = io.WriteString(stdout, b.String())
	return err
}

func truncateBody(body string, maxChars int) string {
	if runes := []rune(body); maxChars > 0 && len(runes) > maxChars {
		return fmt.Sprintf("%s\n\n[truncated at %d of %d characters; --max-chars 0 for all]", string(runes[:maxChars]), maxChars, len(runes))
	}
	return body
}

// stringsFlag collects a repeatable string flag.
type stringsFlag []string

func (s *stringsFlag) String() string { return strings.Join(*s, ", ") }

func (s *stringsFlag) Set(v string) error { *s = append(*s, v); return nil }
