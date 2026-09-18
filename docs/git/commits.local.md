# Commit scopes — switchboard

Additions to [commits.md](commits.md), proper to this repository.

## Vocabulary

A scope names the area a change lands in. It follows the package layout, so the
scope and the directory are the same word.

| Scope | Area |
|---|---|
| `api` | HTTP surface, routing table and the OpenAPI contract |
| `store` | SQLite, the schema and its migrations |
| `cli` | the commands every session drives the tool with |
| `web` | the PO's page |
| `wake` | the grouped signal: batching, the floor, the cursor |
| `richtext` | what a description may contain |
| `issueref` | how an issue is shown and where it points |
| `deploy` | justfile, systemd unit, packaging |
| `ci` | GitHub Actions, dependency and security scanning |
| `docs` | design documents, ADRs, README |

A change touching several of these takes the one it is *about*, not the longest
list: adding a route touches `api`, `cli`, `web` and `docs`, and its scope is
`api`. When no single area is about it, the scope is omitted.

## Examples

```
feat(api): let an open event be withdrawn
fix(web): keep what the PO typed when an ask leaves the list
fix(store): back up before migrating a populated database
chore(ci): scan secrets on every push
docs: move the git conventions out of CLAUDE.md
```
