package diff

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func render(ops []Op) string {
	var parts []string
	for _, op := range ops {
		parts = append(parts, fmt.Sprintf("%c%s", op.Kind, op.Text))
	}
	return strings.Join(parts, "|")
}

// apply rebuilds both sides from ops, so any script can be checked for consistency.
func apply(ops []Op) (a, b []string) {
	for _, op := range ops {
		if op.Kind != Insert {
			a = append(a, op.Text)
		}
		if op.Kind != Delete {
			b = append(b, op.Text)
		}
	}
	return a, b
}

func edits(ops []Op) int {
	n := 0
	for _, op := range ops {
		if op.Kind != Equal {
			n++
		}
	}
	return n
}

func TestLines(t *testing.T) {
	for _, tc := range []struct{ a, b, want string }{
		{"a\nb\nc", "a\nb\nc", " a| b| c"},
		{"", "x", "+x"},
		{"x", "", "-x"},
		{"", "", ""},
		{"a\nb\nc", "a\nc\nd", " a|-b| c|+d"},
		{"a\nb", "a\nx\nb", " a|+x| b"},
		{"old\nkeep", "new\nkeep", "-old|+new| keep"},
		{"a\nb\nc\na\nb\nb\na", "c\nb\na\nb\na\nc", "-a|-b| c|+b| a| b|-b| a|+c"}, // Myers' paper example, D=5
	} {
		if got := render(Lines(split(tc.a), split(tc.b))); got != tc.want {
			t.Errorf("Lines(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestLinesRandom checks on random inputs that the script rebuilds both sides
// and is as short as the LCS says it can be.
func TestLinesRandom(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	gen := func() []string {
		s := make([]string, r.IntN(30))
		for i := range s {
			s[i] = string(rune('a' + r.IntN(4)))
		}
		return s
	}
	for range 500 {
		a, b := gen(), gen()
		ops := Lines(a, b)
		ga, gb := apply(ops)
		if strings.Join(ga, "") != strings.Join(a, "") || strings.Join(gb, "") != strings.Join(b, "") {
			t.Fatalf("Lines(%q, %q) = %q does not rebuild its inputs", a, b, render(ops))
		}
		if got, want := edits(ops), len(a)+len(b)-2*lcs(a, b); got != want {
			t.Fatalf("Lines(%q, %q) makes %d edits, want %d", a, b, got, want)
		}
	}
}

func lcs(a, b []string) int {
	t := make([][]int, len(a)+1)
	for i := range t {
		t[i] = make([]int, len(b)+1)
	}
	for i, v := range slices.Backward(a) {
		for j := len(b) - 1; j >= 0; j-- {
			if v == b[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	return t[0][0]
}

func TestLinesTooManyEdits(t *testing.T) {
	var a, b []string
	for i := range maxEdits {
		a, b = append(a, fmt.Sprint("a", i)), append(b, fmt.Sprint("b", i))
	}
	a, b = append(a, "same"), append(b, "same")
	ops := Lines(a, b)
	if len(ops) != 2*maxEdits+1 || ops[0].Kind != Delete || ops[maxEdits].Kind != Insert || ops[len(ops)-1] != (Op{Equal, "same"}) {
		t.Errorf("a full rewrite should be reported as replaced: %d ops, first %v", len(ops), ops[0])
	}
}

func TestHunks(t *testing.T) {
	var a []string
	for i := 1; i <= 20; i++ {
		a = append(a, fmt.Sprint(i))
	}
	b := append([]string{}, a...)
	// Changes 4 lines apart share a hunk with context 2; 5 or more apart they do not.
	b[1] = "two"
	b[6] = "seven"
	b = append(b[:15], b[16:]...) // line 16 deleted, 8 lines after line 7
	b = append(b, "twenty-one")   // 4 lines after line 16
	hs := Hunks(Lines(a, b), 2)
	var got []string
	for _, h := range hs {
		got = append(got, fmt.Sprintf("-%d,%d +%d,%d %s", h.FromLine, h.FromCount, h.ToLine, h.ToCount, render(h.Ops)))
	}
	want := []string{
		"-1,9 +1,9  1|-2|+two| 3| 4| 5| 6|-7|+seven| 8| 9",
		"-14,7 +14,7  14| 15|-16| 17| 18| 19| 20|+twenty-one",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Hunks =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if hs := Hunks(Lines(nil, []string{"x"}), 3); len(hs) != 1 || hs[0].FromLine != 0 || hs[0].FromCount != 0 || hs[0].ToLine != 1 {
		t.Errorf("insertion into an empty text: %+v", hs)
	}
	if hs := Hunks(Lines(a, a), 3); hs != nil {
		t.Errorf("no changes should give no hunks: %+v", hs)
	}
}
