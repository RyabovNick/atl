// Package diff compares texts line by line: the edits between two versions
// and unified-diff hunks around them.
package diff

import "slices"

// Kind marks a line as kept, deleted or inserted, as in unified diff output.
type Kind byte

// Line kinds.
const (
	Equal  Kind = ' '
	Delete Kind = '-'
	Insert Kind = '+'
)

// Op is one line of a diff.
type Op struct {
	Kind Kind
	Text string
}

// maxEdits bounds the Myers search, whose memory grows with the square of the
// edit count; past it the changed middle is reported as replaced wholesale.
const maxEdits = 1000

// Lines returns a shortest edit script turning a into b, deletions before
// insertions where they meet.
func Lines(a, b []string) []Op {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ops := make([]Op, 0, len(a)+len(b)-pre-suf)
	for _, l := range a[:pre] {
		ops = append(ops, Op{Equal, l})
	}
	ops = append(ops, myers(a[pre:len(a)-suf], b[pre:len(b)-suf])...)
	for _, l := range a[len(a)-suf:] {
		ops = append(ops, Op{Equal, l})
	}
	return ops
}

// myers is Myers' O((N+M)D) algorithm. trace[d] keeps the furthest x reached
// on each diagonal k in -d..d after d edits, indexed by k+d, for the backtrack.
func myers(a, b []string) []Op {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return replace(a, b)
	}
	off := n + m
	v := make([]int, 2*off+2)
	var trace [][]int
	for d := 0; ; d++ {
		if d > maxEdits {
			return replace(a, b)
		}
		done := false
		for k := -d; k <= d && !done; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
			}
			v[off+k] = x
			done = x >= n && y >= m
		}
		trace = append(trace, slices.Clone(v[off-d:off+d+1]))
		if done {
			break
		}
	}

	var ops []Op
	x, y := n, m
	for d := len(trace) - 1; d > 0; d-- {
		prev := func(k int) int { return trace[d-1][k+d-1] }
		k := x - y
		pk := k - 1
		if k == -d || (k != d && prev(k-1) < prev(k+1)) {
			pk = k + 1
		}
		px := prev(pk)
		py := px - pk
		for x > px && y > py {
			x, y = x-1, y-1
			ops = append(ops, Op{Equal, a[x]})
		}
		if x == px {
			y--
			ops = append(ops, Op{Insert, b[y]})
		} else {
			x--
			ops = append(ops, Op{Delete, a[x]})
		}
	}
	for x > 0 {
		x--
		ops = append(ops, Op{Equal, a[x]})
	}
	slices.Reverse(ops)
	return ops
}

func replace(a, b []string) []Op {
	ops := make([]Op, 0, len(a)+len(b))
	for _, l := range a {
		ops = append(ops, Op{Delete, l})
	}
	for _, l := range b {
		ops = append(ops, Op{Insert, l})
	}
	return ops
}

// Hunk is a run of changes with up to context unchanged lines around them.
// FromLine and ToLine are 1-based; for an empty side, the line before.
type Hunk struct {
	FromLine, FromCount int
	ToLine, ToCount     int
	Ops                 []Op
}

// Hunks groups ops into hunks, merging changes whose context would touch.
func Hunks(ops []Op, context int) []Hunk {
	// ai[i] and bi[i] count the lines of a and b before ops[i].
	ai, bi := make([]int, len(ops)+1), make([]int, len(ops)+1)
	for i, op := range ops {
		ai[i+1], bi[i+1] = ai[i], bi[i]
		if op.Kind != Insert {
			ai[i+1]++
		}
		if op.Kind != Delete {
			bi[i+1]++
		}
	}
	var hunks []Hunk
	for i := 0; i < len(ops); {
		if ops[i].Kind == Equal {
			i++
			continue
		}
		end := i + 1
		for j := end; j < len(ops) && j-end < 2*context+1; j++ {
			if ops[j].Kind != Equal {
				end = j + 1
			}
		}
		start, stop := max(0, i-context), min(len(ops), end+context)
		h := Hunk{
			FromLine: ai[start] + 1, FromCount: ai[stop] - ai[start],
			ToLine: bi[start] + 1, ToCount: bi[stop] - bi[start],
			Ops: ops[start:stop],
		}
		if h.FromCount == 0 {
			h.FromLine--
		}
		if h.ToCount == 0 {
			h.ToLine--
		}
		hunks = append(hunks, h)
		i = stop
	}
	return hunks
}
