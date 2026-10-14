package main

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
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
type heading struct {
	level      int
	title      string
	start, end int
}

// scanHeadings finds the headings that structure a Markdown document. Only
// unindented ATX lines outside fenced code count: a '#' comment in a code
// sample, a list item or a table cell is content, not structure.
func scanHeadings(lines []string) []heading {
	var hs []heading
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
			hs = append(hs, heading{level: len(m[1]), title: plainTitle(m[2]), start: i})
		}
	}
	for i := range hs {
		hs[i].end = len(lines)
		for _, next := range hs[i+1:] {
			if next.level <= hs[i].level {
				hs[i].end = next.start
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

// demoteHeadings shifts embedded content's headings so its shallowest one sits
// at minLevel, nesting it under the section atl prints it in. Relative levels
// are kept until they hit Markdown's floor of six.
func demoteHeadings(md string, minLevel int) string {
	lines := strings.Split(md, "\n")
	hs := scanHeadings(lines)
	if len(hs) == 0 {
		return md
	}
	top := hs[0].level
	for _, h := range hs {
		top = min(top, h.level)
	}
	shift := minLevel - top
	if shift <= 0 {
		return md
	}
	for _, h := range hs {
		rest := strings.TrimLeft(lines[h.start][h.level:], " \t")
		lines[h.start] = strings.Repeat("#", min(h.level+shift, 6)) + " " + rest
	}
	return strings.Join(lines, "\n")
}

func runeLen(lines []string) int {
	return utf8.RuneCountInString(strings.TrimSpace(strings.Join(lines, "\n")))
}

func writeTOC(b *strings.Builder, lines []string, hs []heading) {
	fmt.Fprintf(b, "## Contents (%d headings, %d chars; sizes include subsections)\n\n", len(hs), runeLen(lines))
	if len(hs) == 0 {
		b.WriteString("(no headings; read the whole page)\n")
		return
	}
	if n := runeLen(lines[:hs[0].start]); n > 0 {
		fmt.Fprintf(b, "- (before the first heading) — %d chars\n", n)
	}
	top := hs[0].level
	for _, h := range hs {
		top = min(top, h.level)
	}
	for _, h := range hs {
		fmt.Fprintf(b, "%s- %s — %d chars\n", strings.Repeat("  ", h.level-top), h.title, runeLen(lines[h.start:h.end]))
	}
}

func normTitle(s string) string {
	return strings.ToLower(spacesRE.ReplaceAllString(strings.TrimSpace(s), " "))
}

// findSections matches a heading case-insensitively: exact titles first (every
// section with that title, since pages repeat names like "Notes"), then a
// unique prefix, then a unique substring.
func findSections(hs []heading, query string) ([]heading, error) {
	q := normTitle(plainTitle(query))
	var exact, prefix, sub []heading
	for _, h := range hs {
		t := normTitle(h.title)
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
	for _, candidates := range [][]heading{prefix, sub} {
		switch len(candidates) {
		case 0:
			continue
		case 1:
			return candidates, nil
		default:
			return nil, fail(exitUsage, "section %q is ambiguous; it matches:%s", query, headingList(candidates))
		}
	}
	if len(hs) == 0 {
		return nil, fail(exitNotFound, "no section %q: the page has no headings", query)
	}
	return nil, fail(exitNotFound, "no section %q; headings are:%s", query, headingList(hs))
}

func headingList(hs []heading) string {
	top := hs[0].level
	for _, h := range hs {
		top = min(top, h.level)
	}
	var b strings.Builder
	for _, h := range hs {
		fmt.Fprintf(&b, "\n  %s%s", strings.Repeat("  ", h.level-top), h.title)
	}
	return b.String()
}

// stringsFlag collects a repeatable string flag.
type stringsFlag []string

func (s *stringsFlag) String() string     { return strings.Join(*s, ", ") }
func (s *stringsFlag) Set(v string) error { *s = append(*s, v); return nil }
