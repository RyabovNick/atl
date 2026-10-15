# AGENTS.md

atl is a read-only CLI that lets coding agents look up Jira and Confluence
Data Center (REST API v2): issues, JQL, epics, history, pages, search and
attachments. `atl install` also sets up the `atlassian` skill for Claude Code,
Codex and opencode. User-facing docs are in [README.md](README.md).

## Commands

```bash
make build        # ./atl, with the version from git describe
make test         # go vet + go test ./...
make lint         # golangci-lint v2 with .golangci.yml
go test -race ./...
go mod tidy -diff # must print nothing
```

CI (`.github/workflows/ci.yml`) runs the tidy check, `go test -race` and
golangci-lint v2.14.0. Run all of them before committing. The tests need no
network, Jira instance or token.

## Layout

- `main.go`: the usage text and the command table, nothing else.
- `skill/`: the agent-facing text that `atl install` writes, embedded into
  the binary. `atlassian/SKILL.md` is the skill, and `agents-section.md` is the
  section added to CLAUDE.md or AGENTS.md.
- `internal/cli`: the `Error` type that carries an exit code (`Fail`,
  `Report`), flag parsing, JSON output, and the issue key from the git branch.
- `internal/atlassian`: config (environment over `~/.config/atl/config.ini`)
  and the HTTP client.
- `internal/markdown`: HTML to Markdown, and heading outlines and sections.
- `internal/attachment`, `internal/jira`, `internal/confluence`,
  `internal/doctor`, `internal/install`: one package per command group. Each
  command is a `Run…(args []string, stdout, stderr io.Writer) error` function
  registered in `main.go`.
- `internal/testenv`: the fake Jira and Confluence server and env helpers
  shared by the tests.

Keep this split. Put code in the package that owns the concept, and do not add
interfaces, wrappers or new packages without a real second user.

## Rules

- **Read-only.** atl only sends GET requests. Never add a request that
  creates, changes or deletes anything in Jira or Confluence.
- **Tokens stay safe.** A token goes only to the configured scheme and host:
  absolute links and redirects to other hosts are refused, not followed. Never
  print a token; `atl doctor` reports it only as set or unset. The config file
  must stay mode 600, and atl refuses it otherwise.
- **Data Center quirks.** A rejected token shows up as a redirect to the login
  page (Jira) or as an anonymous user (Confluence), not as a 401. Report
  redirects instead of following them, and keep `atl doctor`'s anonymous-user
  check.
- **Errors and exit codes.** Return `cli.Fail(code, …)` with `ExitAPI` (1),
  `ExitUsage` (2), `ExitConfig` (3) or `ExitNotFound` (4); main prints the
  message once. Keep these codes stable: the skill tells agents what each one
  means.
- **Output is for agents.** Commands print compact Markdown, and `--json`
  prints the raw API response. Keep output short and stable, since the skill
  and agents depend on its headings (`## Fields`, `## Attachments`, …).
- **Attachments.** Write downloads as 0600 files that never overwrite an
  existing one, strip path separators and leading dots from names, and enforce
  `--max-bytes` while streaming, whatever size the server claims.
- **Installer.** atl owns only the text between `<!-- atl:begin -->` and
  `<!-- atl:end -->` in an instructions file. It must leave the rest of the
  user's file untouched and stay idempotent: a rerun changes nothing.
- **Dependencies.** Stay on the standard library, `golang.org/x/*` and
  html-to-markdown. Ask before adding a module.

## When changing behavior

- **Commands and flags:** update the usage text in `main.go`, the README's
  Commands table, and `skill/atlassian/SKILL.md` if agents should use the
  change.
- **Config and env vars:** update `config.example.ini` and the README.
- **Tests:** add one next to the code, in the same package. Command tests run
  against `testenv.FakeServer`; extend it when you need new endpoints rather
  than mocking the client.

## Style

- Go 1.27. Use modern idioms (the `modernize` linter enforces them), US
  spelling, and `%w` when wrapping errors.
- Comment the why, briefly, and match the existing density. Exported
  identifiers get a one-line doc comment.
- Commit messages are Conventional Commits (`feat:`, `fix:`, `refactor:`,
  `docs:`, `ci:`, `deps:`, `chore:`), optionally scoped as in `feat(page):`,
  with a body explaining why.
