# atl

Read-only Jira and Confluence Data Center (REST API v2) lookups for coding
agents, plus the opencode skill that tells an agent when to use them.

```bash
make install          # vet, test, build to ~/.local/bin/atl, install the skill
export JIRA_URL=https://jira.example.com JIRA_API_TOKEN=...
export CONFLUENCE_URL=https://confluence.example.com CONFLUENCE_API_TOKEN=...
# or: install -m 600 config.example.ini ~/.config/atl/config.ini and edit it
atl issue PROJ-123    # first real check
```

| Command | Returns |
| --- | --- |
| `atl issue [KEY]` | fields, custom fields by name, open blockers, linked issues (one level), subtasks, attachments, Confluence and other links; KEY defaults to the current branch's |
| `atl jql <query>` | issues matching JQL with status, assignee and update time; says how many more exist |
| `atl epic [KEY]` | an epic and the issues under it, grouped by status category; a non-epic KEY resolves to its epic or parent |
| `atl history [KEY]` | who changed which field and when, oldest first; `--since 2026-10-01` or `--since 7d` narrows it |
| `atl attachment [KEY \| ID...]` | downloads all of an issue's attachments, or the given Jira ids / Confluence `att<digits>` ids, and prints the saved paths |
| `atl page <id\|url>` | a page as Markdown, with its attachments; accepts `pageId=`, `/pages/<id>`, `/display/SPACE/Title` and `/x/<tiny>` URLs |
| `atl search <text>` | pages and blog posts matching `text ~`, or raw CQL with `--cql` |

`atl issue` renders the description, long custom text fields and comments
from Jira's rendered HTML into Markdown, falling back to raw wiki markup when
Jira returns none. Custom fields with no readable value (rank, epic colour,
development summary, bare ids) are left out.

`atl epic` finds children with `"Epic Link" = KEY OR parent = KEY` when the
instance has the Epic Link field, and `parent = KEY` otherwise. A query Jira or
Confluence rejects exits 2 with the server's error messages.

`atl history` reads the issue's changelog. Long text edits (description,
long custom fields) show their size change and a line diff capped at 20
lines, instead of both versions; rank changes are left out. When Jira returns
fewer changes than the changelog's total, the output says the history is
partial. `atl issue` does not fetch the changelog, so it stays small.

`atl attachment` saves into `--dir` or a new `atl-attachments-*` directory
under the system temp dir, as mode 0600 files that never overwrite an existing
one (`log.txt` becomes `log-1.txt`). Server-supplied names lose path separators
and leading dots. Files over `--max-bytes` (50 MiB by default) are refused,
whatever the server claims their size is. The token is sent only to the
configured host: a download link or redirect pointing anywhere else is
refused rather than followed.

Environment variables override the config file, which is optional when they
supply the URL and token. Tokens are sent as `Authorization: Bearer`; a
username (`JIRA_USERNAME`, or `username` in the file) switches to basic auth.
atl refuses a config file other users can read, and reports a redirect instead of following it, because Data Center
answers a rejected token by redirecting to its login page.

`go test ./...` runs against a fake Jira and Confluence; no instance or token
is needed.
