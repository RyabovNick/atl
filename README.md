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
| `atl issue [KEY]` | fields, custom fields by name, open blockers, linked issues (one level), subtasks, Confluence and other links; KEY defaults to the current branch's |
| `atl page <id\|url>` | a page as Markdown; accepts `pageId=`, `/pages/<id>`, `/display/SPACE/Title` and `/x/<tiny>` URLs |
| `atl search <text>` | pages and blog posts matching `text ~`, or raw CQL with `--cql` |

`atl issue` renders the description, long custom text fields and comments
from Jira's rendered HTML into Markdown, falling back to raw wiki markup when
Jira returns none. Custom fields with no readable value (rank, epic colour,
development summary, bare ids) are left out.

Environment variables override the config file, which is optional when they
supply the URL and token. Tokens are sent as `Authorization: Bearer`; a
username (`JIRA_USERNAME`, or `username` in the file) switches to basic auth.
atl refuses a config file other users can read, and reports a redirect instead of following it, because Data Center
answers a rejected token by redirecting to its login page.

`go test ./...` runs against a fake Jira and Confluence; no instance or token
is needed.
