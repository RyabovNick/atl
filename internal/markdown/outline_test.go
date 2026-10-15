package markdown

import (
	"strings"
	"testing"
)

func TestDemoteHeadings(t *testing.T) {
	for _, tc := range []struct {
		in   string
		min  int
		want string
	}{
		{"# A\n## B", 3, "### A\n#### B"},
		{"### A\n#### B", 2, "### A\n#### B"},
		{"# A\n###### F", 3, "### A\n###### F"},
		{"no headings", 3, "no headings"},
		{"```\n# code\n```\n# Real", 2, "```\n# code\n```\n## Real"},
		{"~~~~\n# code\n~~~\n# still code\n~~~~\n#Tag", 2, "~~~~\n# code\n~~~\n# still code\n~~~~\n#Tag"},
		{"  # indented\n- # in list", 2, "  # indented\n- # in list"},
	} {
		if got := DemoteHeadings(tc.in, tc.min); got != tc.want {
			t.Errorf("DemoteHeadings(%q, %d) = %q, want %q", tc.in, tc.min, got, tc.want)
		}
	}
	if got := plainTitle(`**Bold** [link](http://x) \_under\_ ` + "`code`" + ` ##`); got != "Bold link _under_ code ##" {
		t.Errorf("plainTitle = %q", got)
	}
	hs := ScanHeadings(strings.Split("## Closed ##\ntext", "\n"))
	if len(hs) != 1 || hs[0].Title != "Closed" {
		t.Errorf("closing hashes: %+v", hs)
	}
	md, err := FromHTML(`<table><tr><th>Name</th></tr><tr><td><h2>Cell</h2><p>a</p><p>b</p></td></tr></table><p>x <del>y</del></p>`)
	if err != nil || md != "| Name |\n|---|\n| **Cell**<br>a<br>b |\n\nx ~~y~~" {
		t.Errorf("FromHTML table = %q, %v", md, err)
	}
}
