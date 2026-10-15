package cli

import (
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

var (
	keyRE      = regexp.MustCompile(`[A-Z][A-Z0-9]+-\d+`)
	looseKeyRE = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]+-\d+`)
)

func keyFromBranchName(branch string) (string, bool) {
	if m := keyRE.FindString(branch); m != "" {
		return m, true
	}
	if m := looseKeyRE.FindString(branch); m != "" {
		return strings.ToUpper(m), true
	}
	return "", false
}

func keyFromBranch(stderr io.Writer) (string, error) {
	out, err := exec.Command("git", "branch", "--show-current").Output()
	if err != nil {
		return "", Fail(ExitUsage, "no issue key given and not inside a git repository")
	}
	branch := strings.TrimSpace(string(out))
	key, ok := keyFromBranchName(branch)
	if !ok {
		if branch == "" {
			branch = "(detached HEAD)"
		}
		return "", Fail(ExitUsage, "no issue key in branch %q; pass one explicitly", branch)
	}
	fmt.Fprintf(stderr, "atl: %s from branch %s\n", key, branch)
	return key, nil
}

// KeyArg takes an explicit issue key or falls back to the current branch's.
func KeyArg(pos []string, cmd string, stderr io.Writer) (string, error) {
	switch len(pos) {
	case 0:
		return keyFromBranch(stderr)
	case 1:
		return strings.ToUpper(pos[0]), nil
	default:
		return "", Fail(ExitUsage, "%s takes at most one key", cmd)
	}
}
