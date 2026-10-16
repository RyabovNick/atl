package confluence

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/RyabovNick/atl/internal/atlassian"
	"github.com/RyabovNick/atl/internal/cli"
	"github.com/RyabovNick/atl/internal/markdown"
)

const commentBatch = 100

// maxComments bounds a --comments fetch; a var so tests can lower it.
var maxComments = 500

type comment struct {
	ID      string `json:"id"`
	History struct {
		CreatedBy   *atlassian.Named `json:"createdBy"`
		CreatedDate string           `json:"createdDate"`
	} `json:"history"`
	Ancestors []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	} `json:"ancestors"`
	Extensions struct {
		Location         string `json:"location"`
		InlineProperties struct {
			OriginalSelection string `json:"originalSelection"`
		} `json:"inlineProperties"`
		Resolution struct {
			Status string `json:"status"`
		} `json:"resolution"`
	} `json:"extensions"`
	Body struct {
		View struct {
			Value string `json:"value"`
		} `json:"view"`
	} `json:"body"`
}

// pageComments fetches up to maxComments footer and inline comments, replies
// included, in the order Confluence returns them; truncated reports the cap.
func pageComments(conf *atlassian.Client, id string) (cs []comment, raws []json.RawMessage, truncated bool, err error) {
	for start := 0; ; {
		if start >= maxComments {
			return cs, raws, true, nil
		}
		limit := min(commentBatch, maxComments-start)
		var res struct {
			Results []json.RawMessage `json:"results"`
			Links   struct {
				Next string `json:"next"`
			} `json:"_links"`
		}
		params := url.Values{
			"expand": {"body.view,history,ancestors,extensions.inlineProperties,extensions.resolution"},
			"depth":  {"all"},
			"limit":  {strconv.Itoa(limit)},
			"start":  {strconv.Itoa(start)},
		}
		if _, err := conf.Get("/rest/api/content/"+id+"/child/comment", params, &res); err != nil {
			return nil, nil, false, err
		}
		// Enforce the cap even when a server returns more than asked.
		if len(res.Results) > limit {
			res.Results, res.Links.Next = res.Results[:limit], "more"
		}
		for _, r := range res.Results {
			var c comment
			if err := json.Unmarshal(r, &c); err != nil {
				return nil, nil, false, cli.Fail(cli.ExitAPI, "decoding comment: %v", err)
			}
			cs = append(cs, c)
			raws = append(raws, r)
		}
		start += len(res.Results)
		if len(res.Results) == 0 || (res.Links.Next == "" && len(res.Results) < limit) {
			return cs, raws, false, nil
		}
	}
}

// rootID is the top comment of c's thread: its first comment ancestor, or c itself.
func (c comment) rootID() string {
	for _, a := range c.Ancestors {
		if a.Type == "comment" {
			return a.ID
		}
	}
	return c.ID
}

// writeComments prints comments as threads: each top-level comment, then its
// replies. A reply whose root is missing (cut off by the cap) stands alone.
func writeComments(b *strings.Builder, cs []comment, truncated bool) {
	fmt.Fprintf(b, "## Comments (%d)\n", len(cs))
	if len(cs) == 0 {
		b.WriteString("\n(none)\n")
		return
	}
	present := map[string]bool{}
	for _, c := range cs {
		present[c.ID] = true
	}
	replies := map[string][]comment{}
	var roots []comment
	for _, c := range cs {
		if r := c.rootID(); r != c.ID && present[r] {
			replies[r] = append(replies[r], c)
		} else {
			roots = append(roots, c)
		}
	}
	for _, root := range roots {
		writeComment(b, root, "###", "")
		for _, r := range replies[root.ID] {
			writeComment(b, r, "####", "Reply: ")
		}
	}
	if truncated {
		fmt.Fprintf(b, "\n(stopped at %d comments)\n", maxComments)
	}
}

func writeComment(b *strings.Builder, c comment, level, prefix string) {
	var tags []string
	if c.Extensions.Location == "inline" {
		tags = append(tags, "inline")
	}
	if s := c.Extensions.Resolution.Status; s != "" && s != "open" {
		tags = append(tags, s)
	}
	meta := ""
	if len(tags) > 0 {
		meta = " (" + strings.Join(tags, ", ") + ")"
	}
	fmt.Fprintf(b, "\n%s %s%s — %s · id %s%s\n\n", level, prefix,
		cli.Or(atlassian.NameOf(c.History.CreatedBy), "?"), atlassian.ShortDate(c.History.CreatedDate), c.ID, meta)
	if sel := strings.TrimSpace(c.Extensions.InlineProperties.OriginalSelection); sel != "" {
		fmt.Fprintf(b, "> %s\n\n", strings.ReplaceAll(sel, "\n", "\n> "))
	}
	md, err := markdown.FromHTML(c.Body.View.Value)
	if err != nil {
		md = "(cannot convert comment: " + err.Error() + ")"
	}
	fmt.Fprintln(b, cli.Or(markdown.DemoteHeadings(strings.TrimSpace(md), 5), "(empty)"))
}
