package main

import (
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
)

var (
	blankLinesRE = regexp.MustCompile(`\n{3,}`)
	cellBreaksRE = regexp.MustCompile(`(?:<br />)+`)
	sprintRE     = regexp.MustCompile(`^com\.atlassian\.greenhopper\.service\.sprint\.Sprint@`)
	sprintNameRE = regexp.MustCompile(`[\[,]name=([^,\]]*)`)
	sprintStRE   = regexp.MustCompile(`[\[,]state=([^,\]]*)`)
	lexoRankRE   = regexp.MustCompile(`^\d\|[0-9a-z]+:[0-9a-z]*$`)
)

// Field types whose values are internal bookkeeping, not something a reader acts on.
var skippedFieldTypes = []string{"gh-lexo-rank", "gh-epic-color", "gh-epic-link", "devsummary"}

const longFieldChars = 120

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

func htmlToMarkdown(src string) (string, error) {
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

// richText prefers Jira's rendered HTML, converted to Markdown, over raw wiki markup.
func richText(rendered json.RawMessage, raw string) string {
	var html string
	if json.Unmarshal(rendered, &html) == nil && strings.TrimSpace(html) != "" {
		if md, err := htmlToMarkdown(html); err == nil && md != "" {
			return md
		}
	}
	return strings.TrimSpace(raw)
}

type fieldDef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Custom bool   `json:"custom"`
	Schema struct {
		Type   string `json:"type"`
		Custom string `json:"custom"`
	} `json:"schema"`
}

type fieldValue struct{ name, value string }

// renderValue turns a custom field value into one line of text, or "" when it
// is empty or has no human-readable form.
func renderValue(raw json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return ""
	}
	return renderAny(v)
}

func renderAny(v any) string {
	switch x := v.(type) {
	case string:
		return renderString(x)
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "yes"
		}
		return "no"
	case []any:
		var parts []string
		for _, item := range x {
			if s := renderAny(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		return renderObject(x)
	}
	return ""
}

func renderString(s string) string {
	s = strings.TrimSpace(s)
	switch {
	case sprintRE.MatchString(s):
		name := sprintNameRE.FindStringSubmatch(s)
		if name == nil {
			return ""
		}
		if state := sprintStRE.FindStringSubmatch(s); state != nil && state[1] != "" {
			return name[1] + " (" + state[1] + ")"
		}
		return name[1]
	case lexoRankRE.MatchString(s), strings.HasPrefix(s, "{summaryBean="):
		return ""
	}
	return s
}

func renderObject(m map[string]any) string {
	// Cascading selects hold the parent option in value and the chosen child in child.
	if value, ok := m["value"]; ok {
		s := renderAny(value)
		if child, ok := m["child"].(map[string]any); ok {
			if c := renderObject(child); c != "" {
				s += " / " + c
			}
		}
		return s
	}
	for _, key := range []string{"displayName", "name", "title", "key"} {
		label, ok := m[key].(string)
		if !ok || label == "" {
			continue
		}
		if state, ok := m["state"].(string); ok && state != "" {
			return label + " (" + state + ")"
		}
		return label
	}
	return ""
}

// customFields renders every non-empty custom field: short values for a list,
// long or multi-line text for sections of their own.
func customFields(defs []fieldDef, values, rendered map[string]json.RawMessage, skipID string) (short, long []fieldValue) {
	for _, d := range defs {
		if d.ID == skipID || !d.Custom || !strings.HasPrefix(d.ID, "customfield_") || skippedType(d.Schema.Custom) {
			continue
		}
		raw, ok := values[d.ID]
		if !ok {
			continue
		}
		value := renderValue(raw)
		if value == "" {
			continue
		}
		if d.Schema.Type == "string" {
			value = richText(rendered[d.ID], value)
		}
		fv := fieldValue{d.Name, value}
		if strings.Contains(value, "\n") || len(value) > longFieldChars {
			long = append(long, fv)
		} else {
			short = append(short, fv)
		}
	}
	byName := func(fs []fieldValue) {
		sort.SliceStable(fs, func(i, j int) bool { return fs[i].name < fs[j].name })
	}
	byName(short)
	byName(long)
	return short, long
}

func skippedType(schemaCustom string) bool {
	for _, t := range skippedFieldTypes {
		if strings.HasSuffix(schemaCustom, ":"+t) {
			return true
		}
	}
	return false
}
