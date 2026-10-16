package main

import (
	"fmt"
	"io"
	"os"

	"github.com/RyabovNick/atl/internal/attachment"
	"github.com/RyabovNick/atl/internal/cli"
	"github.com/RyabovNick/atl/internal/confluence"
	"github.com/RyabovNick/atl/internal/doctor"
	"github.com/RyabovNick/atl/internal/install"
	"github.com/RyabovNick/atl/internal/jira"
)

const usage = `atl: read-only Jira and Confluence lookups

  atl issue [KEY] [--comments] [--json]       issue, custom fields, links, Confluence refs
  atl jql <query> [--limit N] [--json]        Jira issues matching JQL
  atl epic [KEY] [--limit N] [--json]         issues under an epic, by status
  atl history [KEY] [--since DATE|7d] [--json]
                                              who changed what, oldest first
  atl attachment [KEY | ID...] [--dir DIR] [--max-bytes N] [--json]
                                              download attachments, print saved paths
  atl page <id|url> [--toc] [--section H]... [--children [--depth N]] [--comments]
           [--max-chars N] [--json]          Confluence page as Markdown, its outline,
                                              chosen sections, child pages, or comments
  atl search <text> [--space KEY] [--cql] [--limit N] [--json]
                                              Confluence pages and blog posts
  atl doctor [--json]                         where each setting comes from, and
                                              whether each service accepts its token
  atl install [--agents LIST] [--bin-dir DIR] [--no-binary] [--no-instructions] [--force]
                                              copy the binary, write the skill and the
                                              instructions section for Claude Code, Codex
                                              and/or opencode; asks which without --agents
  atl uninstall [--bin-dir DIR] [--keep-binary]
  atl version

KEY defaults to the issue key in the current git branch.
Credentials: JIRA_URL, JIRA_API_TOKEN, CONFLUENCE_URL, CONFLUENCE_API_TOKEN
(plus *_USERNAME for basic auth), over $ATL_CONFIG or ~/.config/atl/config.ini.
Exit codes: 0 ok, 1 API or network, 2 usage or rejected query, 3 config, 4 not found.
`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return cli.ExitUsage
	}
	commands := map[string]func([]string, io.Writer, io.Writer) error{
		"issue":      jira.RunIssue,
		"jql":        jira.RunJQL,
		"epic":       jira.RunEpic,
		"history":    jira.RunHistory,
		"page":       confluence.RunPage,
		"attachment": attachment.Run,
		"search":     confluence.RunSearch,
		"doctor":     doctor.Run,
		"install":    install.Run,
		"uninstall":  install.RunUninstall,
	}
	cmd, ok := commands[args[0]]
	if !ok {
		switch args[0] {
		case "-h", "--help", "help":
			fmt.Fprint(stdout, usage)
			return 0
		case "version", "--version":
			fmt.Fprintf(stdout, "atl %s\n", cli.Version)
			return 0
		}
		fmt.Fprintf(stderr, "atl: unknown command %q\n\n%s", args[0], usage)
		return cli.ExitUsage
	}
	return cli.Report(cmd(args[1:], stdout, stderr), stderr)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
