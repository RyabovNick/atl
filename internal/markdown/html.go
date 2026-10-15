// Package markdown converts Jira and Confluence HTML to Markdown and works
// with the headings of the result: outlines, sections and nesting.
package markdown

import (
	"regexp"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var (
	blankLinesRE = regexp.MustCompile(`\n{3,}`)
	cellBreaksRE = regexp.MustCompile(`(?:<br />)+`)
)

// mdConverter adds GitHub-style tables: Confluence and Jira lay out much of
// their content in tables, which the default converter flattens into text.
var mdConverter = converter.NewConverter(converter.WithPlugins(
	base.NewBasePlugin(),
	commonmark.NewCommonmarkPlugin(),
	strikethrough.NewStrikethroughPlugin(),
	table.NewTablePlugin(
		table.WithNewlineBehavior(table.NewlineBehaviorPreserve),
		table.WithHeaderPromotion(true),
		table.WithCellPaddingBehavior(table.CellPaddingBehaviorMinimal),
	),
))

// FromHTML converts rendered Jira or Confluence HTML to Markdown.
func FromHTML(src string) (string, error) {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return "", err
	}
	flattenCellHeadings(doc, false)
	out, err := mdConverter.ConvertNode(doc)
	if err != nil {
		return "", err
	}
	md := cellBreaksRE.ReplaceAllString(string(out), "<br>")
	return strings.TrimSpace(blankLinesRE.ReplaceAllString(md, "\n\n")), nil
}

// flattenCellHeadings turns headings inside table cells into bold paragraphs. A
// Markdown table cannot hold a heading, so the converter would otherwise flatten
// the whole table and its cell headings would pose as sections of the document.
func flattenCellHeadings(n *html.Node, inCell bool) {
	if n.Type == html.ElementNode {
		switch n.DataAtom {
		case atom.Td, atom.Th:
			inCell = true
		case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
			if inCell {
				strong := &html.Node{Type: html.ElementNode, Data: "strong", DataAtom: atom.Strong}
				for c := n.FirstChild; c != nil; {
					next := c.NextSibling
					n.RemoveChild(c)
					strong.AppendChild(c)
					c = next
				}
				n.Data, n.DataAtom, n.Attr = "p", atom.P, nil
				n.AppendChild(strong)
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		flattenCellHeadings(c, inCell)
	}
}
