package main

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
)

var (
	pagesPathRE   = regexp.MustCompile(`/pages/(\d+)`)
	tinyPathRE    = regexp.MustCompile(`/x/([A-Za-z0-9_-]+)`)
	displayPathRE = regexp.MustCompile(`/display/([^/]+)/(.+)$`)
	blankLinesRE  = regexp.MustCompile(`\n{3,}`)
)

type page struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Space struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"space"`
	Version struct {
		Number int    `json:"number"`
		When   string `json:"when"`
		By     *named `json:"by"`
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

func pageIDFromRef(conf *client, refStr string) (string, error) {
	refStr = strings.TrimSpace(refStr)
	if _, err := strconv.ParseUint(refStr, 10, 64); err == nil {
		return refStr, nil
	}
	u, err := url.Parse(refStr)
	if err != nil {
		return "", fail(exitUsage, "cannot parse %q as a page id or URL", refStr)
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
	return "", fail(exitUsage, "cannot find a page id in %q", refStr)
}

// resolveTiny trusts the decoded id only once the page exists; otherwise it asks
// Confluence where the link redirects, which works whatever the encoding.
func resolveTiny(conf *client, code string) (string, error) {
	if id, err := tinyToPageID(code); err == nil {
		if _, err := conf.get("/rest/api/content/"+id, nil, nil); err == nil {
			return id, nil
		}
	}
	resp, _, err := conf.raw("/x/"+code, nil)
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
	return "", fail(exitNotFound, "cannot resolve tiny link /x/%s", code)
}

func resolveDisplay(conf *client, space string, parts []string) (string, error) {
	title, err := url.PathUnescape(strings.ReplaceAll(parts[len(parts)-1], "+", " "))
	if err != nil {
		return "", fail(exitUsage, "bad page title in URL: %v", err)
	}
	params := url.Values{"spaceKey": {space}, "title": {title}, "type": {"page"}}
	if len(parts) == 4 && isDigits(parts[0]) && isDigits(parts[1]) && isDigits(parts[2]) {
		params.Set("type", "blogpost")
		params.Set("postingDay", strings.Join(parts[:3], "-"))
	}
	var res struct {
		Results []page `json:"results"`
	}
	if _, err := conf.get("/rest/api/content", params, &res); err != nil {
		return "", err
	}
	if len(res.Results) == 0 {
		return "", fail(exitNotFound, "no page titled %q in space %s", title, space)
	}
	return res.Results[0].ID, nil
}

func isDigits(s string) bool {
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

func cmdPage(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("page", "<id|url>", "A Confluence page as Markdown. Accepts a page id or any page URL:\nviewpage.action?pageId=, /pages/<id>, /display/SPACE/Title, /x/<tiny>.", stderr)
	maxChars := fs.Int("max-chars", 40000, "truncate the body to this many characters (0 = no limit)")
	asJSON := fs.Bool("json", false, "print the raw API response")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fail(exitUsage, "page takes exactly one id or URL")
	}
	conf, err := newClient("confluence")
	if err != nil {
		return err
	}
	id, err := pageIDFromRef(conf, pos[0])
	if err != nil {
		return err
	}
	var p page
	raw, err := conf.get("/rest/api/content/"+id, url.Values{"expand": {"body.view,space,version,ancestors"}}, &p)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, raw)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", p.Title)
	fmt.Fprintf(&b, "Space: %s (%s) · id %s\n", or(p.Space.Name, "?"), or(p.Space.Key, "?"), p.ID)
	fmt.Fprintf(&b, "Version %d by %s at %s\n", p.Version.Number, or(nameOf(p.Version.By), "?"), shortDate(p.Version.When))
	if len(p.Ancestors) > 0 {
		titles := make([]string, len(p.Ancestors))
		for i, a := range p.Ancestors {
			titles[i] = a.Title
		}
		fmt.Fprintf(&b, "Path: %s\n", strings.Join(titles, " / "))
	}
	fmt.Fprintf(&b, "URL: %s%s\n\n", conf.base, p.Links.WebUI)

	body, err := htmltomarkdown.ConvertString(p.Body.View.Value)
	if err != nil {
		return fail(exitAPI, "converting page %s to Markdown: %v", id, err)
	}
	body = strings.TrimSpace(blankLinesRE.ReplaceAllString(body, "\n\n"))
	if runes := []rune(body); *maxChars > 0 && len(runes) > *maxChars {
		body = fmt.Sprintf("%s\n\n[truncated at %d of %d characters; --max-chars 0 for all]", string(runes[:*maxChars]), *maxChars, len(runes))
	}
	fmt.Fprintln(&b, or(body, "(empty page)"))
	_, err = io.WriteString(stdout, b.String())
	return err
}

func cqlString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func cmdSearch(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("search", "<text>", "Search Confluence pages and blog posts.", stderr)
	space := fs.String("space", "", "limit to one space key")
	rawCQL := fs.Bool("cql", false, "treat <text> as a raw CQL query")
	limit := fs.Int("limit", 10, "maximum results")
	asJSON := fs.Bool("json", false, "print the raw API response")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return fail(exitUsage, "search needs text to look for")
	}
	text := strings.Join(pos, " ")
	cql := text
	if !*rawCQL {
		cql = "text ~ " + cqlString(text) + " AND type in (page, blogpost)"
		if *space != "" {
			cql += " AND space = " + cqlString(*space)
		}
	}
	conf, err := newClient("confluence")
	if err != nil {
		return err
	}
	var res struct {
		Results []page `json:"results"`
		Size    int    `json:"size"`
	}
	params := url.Values{"cql": {cql}, "limit": {strconv.Itoa(*limit)}, "expand": {"space,version"}}
	raw, err := conf.get("/rest/api/content/search", params, &res)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, raw)
	}
	if len(res.Results) == 0 {
		fmt.Fprintf(stdout, "no results for: %s\n", cql)
		return nil
	}
	for _, r := range res.Results {
		fmt.Fprintf(stdout, "- %s — %s · updated %s · id %s\n  %s%s\n", r.Title, or(r.Space.Key, "?"), shortDate(r.Version.When), r.ID, conf.base, r.Links.WebUI)
	}
	if res.Size >= *limit {
		fmt.Fprintf(stdout, "(first %d; raise --limit or narrow the query)\n", *limit)
	}
	return nil
}
