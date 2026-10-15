# atl

Read-only Jira and Confluence Data Center (REST API v2) lookups for coding
agents, plus the opencode skill that tells an agent when to use them.

## Install

Each way ends in `atl install`. It copies the binary to `~/.local/bin`
(`--bin-dir` to change), writes the `atlassian` skill to
`~/.config/opencode/skills/`, and adds a short section to
`~/.config/opencode/AGENTS.md` telling agents to check the branch for an issue
key. That section sits between `<!-- atl:begin -->` and `<!-- atl:end -->`.
Rerunning updates it in place and leaves the rest of the file alone.

- **Linux or macOS, no Go:** run [install.sh](install.sh). It downloads the
  latest release and checks it against `SHA256SUMS`. `ATL_VERSION` picks a
  release instead of the latest, and other arguments go to `atl install`.

  ```bash
  curl -fsSL https://raw.githubusercontent.com/RyabovNick/atl/main/install.sh | sh
  ```
- **A release binary, any system:** download `atl-<os>-<arch>` from
  [Releases](https://github.com/RyabovNick/atl/releases), check it against `SHA256SUMS`, and run
  `./atl-<os>-<arch> install`. On Windows, download `atl-windows-amd64.exe`,
  rename it to `atl.exe` and run `atl.exe install`; it installs to
  `%LOCALAPPDATA%\Programs\atl`.
- **With Go:** `go install github.com/RyabovNick/atl@latest`, then
  `atl install`. From a clone, `make install` runs the tests, builds, and
  runs `./atl install`.

Then set the tokens, check them, and restart opencode:

```bash
export JIRA_URL=https://jira.example.com JIRA_API_TOKEN=...
export CONFLUENCE_URL=https://confluence.example.com CONFLUENCE_API_TOKEN=...
# or: install -m 600 config.example.ini ~/.config/atl/config.ini and edit it
atl doctor            # where each setting comes from; does each service accept its token
```

`atl uninstall` (or `make uninstall`) removes the binary, the skill and the
AGENTS.md section, and leaves the config file in place. `atl version` prints
the build's version.

## Commands

`KEY` defaults to the issue key in the current git branch
(`feature/PROJ-123-x` → `PROJ-123`). Every command below takes `--json`: the
raw API response, or for `attachment` and `doctor` their own report.

| Command | Returns |
| --- | --- |
| `atl issue [KEY] [--comments]` | fields, custom fields by name, open blockers, linked issues (one level), subtasks, attachments, Confluence and other links |
| `atl jql <query> [--limit 20]` | issues matching JQL with status, assignee and update time; says how many more exist |
| `atl epic [KEY] [--limit 50]` | an epic and the issues under it, grouped by status category; a non-epic `KEY` resolves to its epic or parent |
| `atl history [KEY] [--since 2026-10-01\|7d\|48h]` | who changed which field and when, oldest first |
| `atl attachment [KEY \| ID...] [--dir D] [--max-bytes N]` | downloads an issue's attachments, or the given Jira ids / Confluence `att<digits>` ids, and prints the saved paths |
| `atl page <id\|url> [--max-chars 40000]` | a page as Markdown, with its attachments; accepts `pageId=`, `/pages/<id>`, `/display/SPACE/Title` and `/x/<tiny>` URLs |
| `atl page <ref> --toc` | the page's headings, indented, with each section's size in characters |
| `atl page <ref> --section <heading>` | only the matching sections, subsections included; repeatable |
| `atl page <ref> --children [--depth 1-3]` | child pages with id, update time and URL, instead of the body |
| `atl search <text> [--space KEY] [--cql] [--limit 10]` | pages and blog posts matching `text ~`, or raw CQL |
| `atl doctor` | each setting and its source, then a login check per service |

Exit codes: `0` ok, `1` API or network, `2` usage or a query Jira/Confluence
rejected (the server's message is shown), `3` config, `4` not found.
`atl doctor` exits `0` when every configured service authenticates, `1` when a
check fails, and `3` when nothing could be checked (nothing configured, or the
config file is unusable).

## Output

- **Rich text.** Descriptions, long custom text fields, comments and pages
  are rendered HTML converted to Markdown, with GitHub-style tables. Jira
  text falls back to raw wiki markup when Jira returns no rendered HTML.
  Embedded headings are shifted to nest under atl's own sections.
- **Custom fields.** Short values go under `## Fields`; long text such as
  acceptance criteria gets its own section. Fields with no readable value
  (rank, epic colour, development summary, bare ids) are left out.
- **Sections.** `--section` matches case-insensitively: every section with
  exactly that title, else a unique prefix, else a unique substring. An
  ambiguous match exits 2, no match exits 4, both listing the headings.
- **Epics.** Children are `"Epic Link" = KEY OR parent = KEY` when the
  instance has the Epic Link field, `parent = KEY` otherwise.
- **History.** Long text edits show their size change and a line diff capped
  at 20 lines; rank changes are dropped; a changelog Jira truncated is marked
  partial. `atl issue` does not fetch the changelog.

## Configuration and auth

`JIRA_URL`, `JIRA_API_TOKEN`, `JIRA_USERNAME` and their `CONFLUENCE_*`
equivalents override `$ATL_CONFIG` or `~/.config/atl/config.ini`. The file is optional when the
environment supplies the URL and token; see
[config.example.ini](config.example.ini) for the other settings (`auth`,
`ca_bundle`, `timeout`). Tokens are sent as `Authorization: Bearer`; a
username switches to basic auth. For Confluence use a native personal access
token (avatar → Settings → Personal Access Tokens) with no username; tokens
issued by API-token apps can be rejected even when Jira accepts its own.

## Security properties

- No write verbs: nothing in atl can change Jira or Confluence.
- The token goes only to the configured scheme and host. Download links and
  redirects anywhere else are refused, not followed.
- atl refuses a config file other users can read. `atl doctor` reports a
  token only as set or unset.
- Attachments are streamed to 0600 files that never overwrite an existing
  one, under `--dir` or a new `atl-attachments-*` temp directory. Names lose
  path separators and leading dots, and files over `--max-bytes` (50 MiB by
  default) are refused, whatever size the server claims.

## Testing

`go test ./...` runs against a fake Jira and Confluence; no instance or token
is needed. `go test -cover ./...` reports coverage.

`make lint` runs [golangci-lint](https://golangci-lint.run) v2 with the
linters in [.golangci.yml](.golangci.yml); CI runs the same version, plus
the race detector and a `go mod tidy` check.

## License

[MIT](LICENSE)
