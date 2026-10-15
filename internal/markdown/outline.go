package markdown

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/RyabovNick/atl/internal/cli"
)

var (
	headingRE   = regexp.MustCompile(`^(#{1,6})[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$`)
	fenceRE     = regexp.MustCompile("^[ \t]*(`{3,}|~{3,})")
	mdLinkRE    = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	mdEscapeRE  = regexp.MustCompile("\\\\([!-/:-@\\[-`{-~])")
	mdEmphasisR = strings.NewReplacer("**", "", "__", "", "*", "", "`", "")
	spacesRE    = regexp.MustCompile(`\s+`)
)

// heading is an ATX heading; lines[start:end] is its section, subsections included.
type Heading struct {
	Level      int
	Title      string
	Start, End int
}

// ScanHeadings finds the headings that structure a Markdown document. Only
// unindented ATX lines outside fenced code count: a '#' comment in a code
// sample, a list item or a table cell is content, not structure.
func ScanHeadings(lines []string) []Heading {
	var hs []Heading
	fence := ""
	for i, l := range lines {
		if fence != "" {
			if m := fenceRE.FindStringSubmatch(l); m != nil && m[1][0] == fence[0] && len(m[1]) >= len(fence) {
				fence = ""
			}
			continue
		}
		if m := fenceRE.FindStringSubmatch(l); m != nil {
			fence = m[1]
			continue
		}
		if m := headingRE.FindStringSubmatch(l); m != nil {
			hs = append(hs, Heading{Level: len(m[1]), Title: plainTitle(m[2]), Start: i})
		}
	}
	for i := range hs {
		hs[i].End = len(lines)
		for _, next := range hs[i+1:] {
			if next.Level <= hs[i].Level {
				hs[i].End = next.Start
				break
			}
		}
	}
	return hs
}

func plainTitle(s string) string {
	s = mdLinkRE.ReplaceAllString(s, "$1")
	s = mdEmphasisR.Replace(s)
	s = mdEscapeRE.ReplaceAllString(s, "$1")
	return strings.TrimSpace(spacesRE.ReplaceAllString(s, " "))
}

// DemoteHeadings shifts embedded content's headings so its shallowest one sits
// at minLevel, nesting it under the section atl prints it in. Relative levels
// are kept until they hit Markdown's floor of six.
func DemoteHeadings(md string, minLevel int) string {
	lines := strings.Split(md, "\n")
	hs := ScanHeadings(lines)
	if len(hs) == 0 {
		return md
	}
	top := hs[0].Level
	for _, h := range hs {
		top = min(top, h.Level)
	}
	shift := minLevel - top
	if shift <= 0 {
		return md
	}
	for _, h := range hs {
		rest := strings.TrimLeft(lines[h.Start][h.Level:], " \t")
		lines[h.Start] = strings.Repeat("#", min(h.Level+shift, 6)) + " " + rest
	}
	return strings.Join(lines, "\n")
}

func runeLen(lines []string) int {
	return utf8.RuneCountInString(strings.TrimSpace(strings.Join(lines, "\n")))
}

// WriteTOC lists the headings in hs, indented by level, with each section's size.
func WriteTOC(b *strings.Builder, lines []string, hs []Heading) {
	fmt.Fprintf(b, "## Contents (%d headings, %d chars; sizes include subsections)\n\n", len(hs), runeLen(lines))
	if len(hs) == 0 {
		b.WriteString("(no headings; read the whole page)\n")
		return
	}
	if n := runeLen(lines[:hs[0].Start]); n > 0 {
		fmt.Fprintf(b, "- (before the first heading) — %d chars\n", n)
	}
	top := hs[0].Level
	for _, h := range hs {
		top = min(top, h.Level)
	}
	for _, h := range hs {
		fmt.Fprintf(b, "%s- %s — %d chars\n", strings.Repeat("  ", h.Level-top), h.Title, runeLen(lines[h.Start:h.End]))
	}
}

func normTitle(s string) string {
	return strings.ToLower(spacesRE.ReplaceAllString(strings.TrimSpace(s), " "))
}

// FindSections matches a heading case-insensitively: exact titles first (every
// section with that title, since pages repeat names like "Notes"), then a
// unique prefix, then a unique substring.
func FindSections(hs []Heading, query string) ([]Heading, error) {
	q := normTitle(plainTitle(query))
	var exact, prefix, sub []Heading
	for _, h := range hs {
		t := normTitle(h.Title)
		switch {
		case t == q:
			exact = append(exact, h)
		case strings.HasPrefix(t, q):
			prefix = append(prefix, h)
		case strings.Contains(t, q):
			sub = append(sub, h)
		}
	}
	if len(exact) > 0 {
		return exact, nil
	}
	for _, candidates := range [][]Heading{prefix, sub} {
		switch len(candidates) {
		case 0:
			continue
		case 1:
			return candidates, nil
		default:
			return nil, cli.Fail(cli.ExitUsage, "section %q is ambiguous; it matches:%s", query, headingList(candidates))
		}
	}
	if len(hs) == 0 {
		return nil, cli.Fail(cli.ExitNotFound, "no section %q: the page has no headings", query)
	}
	return nil, cli.Fail(cli.ExitNotFound, "no section %q; headings are:%s", query, headingList(hs))
}

func headingList(hs []Heading) string {
	top := hs[0].Level
	for _, h := range hs {
		top = min(top, h.Level)
	}
	var b strings.Builder
	for _, h := range hs {
		fmt.Fprintf(&b, "\n  %s%s", strings.Repeat("  ", h.Level-top), h.Title)
	}
	return b.String()
}
