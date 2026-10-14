# atl

Read-only Jira and Confluence Data Center (REST API v2) lookups for coding
agents, plus the opencode skill that tells an agent when to use them.

```bash
make install          # vet, test, build to ~/.local/bin/atl, install the skill
mkdir -p ~/.config/atl && install -m 600 config.example.ini ~/.config/atl/config.ini
$EDITOR ~/.config/atl/config.ini
atl issue PROJ-123    # first real check
```

| Command | Returns |
| --- | --- |
| `atl issue [KEY]` | fields, open blockers, linked issues (one level), subtasks, Confluence and other links; KEY defaults to the current branch's |
| `atl page <id\|url>` | a page as Markdown; accepts `pageId=`, `/pages/<id>`, `/display/SPACE/Title` and `/x/<tiny>` URLs |
| `atl search <text>` | pages and blog posts matching `text ~`, or raw CQL with `--cql` |

Tokens are sent as `Authorization: Bearer` unless a section sets
`auth = basic` with a `username`. atl refuses a config file other users can
read, and reports a redirect instead of following it, because Data Center
answers a rejected token by redirecting to its login page.

`go test ./...` runs against a fake Jira and Confluence; no instance or token
is needed.
