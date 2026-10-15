package confluence

import (
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/RyabovNick/atl/internal/atlassian"
	"github.com/RyabovNick/atl/internal/cli"
)

func cqlString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// RunSearch implements `atl search`.
func RunSearch(args []string, stdout, stderr io.Writer) error {
	fs := cli.NewFlags("search", "<text>", "Search Confluence pages and blog posts.", stderr)
	space := fs.String("space", "", "limit to one space key")
	rawCQL := fs.Bool("cql", false, "treat <text> as a raw CQL query")
	limit := fs.Int("limit", 10, "maximum results")
	asJSON := fs.Bool("json", false, "print the raw API response")
	pos, err := cli.ParseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return cli.Fail(cli.ExitUsage, "search needs text to look for")
	}
	text := strings.Join(pos, " ")
	cql := text
	if !*rawCQL {
		cql = "text ~ " + cqlString(text) + " AND type in (page, blogpost)"
		if *space != "" {
			cql += " AND space = " + cqlString(*space)
		}
	}
	conf, err := atlassian.NewClient("confluence")
	if err != nil {
		return err
	}
	var res struct {
		Results []page `json:"results"`
		Size    int    `json:"size"`
	}
	params := url.Values{"cql": {cql}, "limit": {strconv.Itoa(*limit)}, "expand": {"space,version"}}
	raw, err := conf.Get("/rest/api/content/search", params, &res)
	if err != nil {
		return err
	}
	if *asJSON {
		return cli.WriteJSON(stdout, raw)
	}
	if len(res.Results) == 0 {
		fmt.Fprintf(stdout, "no results for: %s\n", cql)
		return nil
	}
	for _, r := range res.Results {
		fmt.Fprintf(stdout, "- %s — %s · updated %s · id %s\n  %s%s\n", r.Title, cli.Or(r.Space.Key, "?"), atlassian.ShortDate(r.Version.When), r.ID, conf.Base, r.Links.WebUI)
	}
	if res.Size >= *limit {
		fmt.Fprintf(stdout, "(first %d; raise --limit or narrow the query)\n", *limit)
	}
	return nil
}
