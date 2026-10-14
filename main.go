// Command atl reads Jira and Confluence Data Center (REST API v2) for coding agents.
// It has no write verbs on purpose.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

const usage = `atl: read-only Jira and Confluence lookups

  atl issue [KEY] [--comments] [--json]       issue, links, subtasks, Confluence refs
  atl page <id|url> [--max-chars N] [--json]  Confluence page as Markdown
  atl search <text> [--space KEY] [--cql] [--limit N] [--json]

KEY defaults to the issue key in the current git branch.
Config: $ATL_CONFIG or ~/.config/atl/config.ini (mode 600).
Exit codes: 0 ok, 1 API or network, 2 usage, 3 config, 4 not found.
`

func newFlags(name, args, help string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: atl %s %s\n\n%s\n\n", name, args, help)
		fs.PrintDefaults()
	}
	return fs
}

// parseFlags allows flags after positional arguments, as in `atl issue KEY --comments`.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, &atlError{code: 0}
			}
			return nil, &atlError{msg: err.Error(), code: exitUsage}
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	commands := map[string]func([]string, io.Writer, io.Writer) error{
		"issue":  cmdIssue,
		"page":   cmdPage,
		"search": cmdSearch,
	}
	cmd, ok := commands[args[0]]
	if !ok {
		if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
			fmt.Fprint(stdout, usage)
			return 0
		}
		fmt.Fprintf(stderr, "atl: unknown command %q\n\n%s", args[0], usage)
		return exitUsage
	}
	if err := cmd(args[1:], stdout, stderr); err != nil {
		var ae *atlError
		if errors.As(err, &ae) {
			if ae.msg != "" {
				fmt.Fprintf(stderr, "atl: %s\n", ae.msg)
			}
			return ae.code
		}
		fmt.Fprintf(stderr, "atl: %v\n", err)
		return exitAPI
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
