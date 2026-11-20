# Anvil-Go
[![make.fun · github-core-clone-01](https://make.fun/badge/github-core-clone-01.svg)](https://make.fun/bounty/github-core-clone-01?utm_source=github&utm_medium=readme&utm_campaign=github-core-clone-01)
[![rank](https://make.fun/badge/github-core-clone-01/wizzy-bounty/github-core-clone-01-go6/rank.svg)](https://make.fun/bounty/github-core-clone-01/leaderboard?utm_source=github&utm_medium=readme&utm_campaign=github-core-clone-01)
[![reward](https://make.fun/badge/github-core-clone-01/wizzy-bounty/github-core-clone-01-go6/reward.svg)](https://make.fun/bounty/github-core-clone-01?utm_source=github&utm_medium=readme&utm_campaign=github-core-clone-01)

An in-memory GitHub-compatible git forge written from scratch in Go using
only the standard library. It implements the git smart-HTTP protocol
(v0 and v2), the GitHub REST API (`/api/v3`), a GraphQL API
(`/api/graphql`) with a hand-written lexer, parser and executor including
introspection, and an MCP endpoint (`/mcp`) exposing 21 tools.

The server is a single static binary (`anvil-go`, ~7 MB) started through
the bundled `main.ts` launcher, which spawns the binary, lets it register
its ephemeral port with the trial environment, and forwards its exit
status.

## Features

- Git smart HTTP: advertisement (v0 + protocol v2), `ls-refs`, fetch with
  shallow/deepen support, push with report-status, sideband and
  non-fast-forward rejection; ofs/ref delta pack reading and writing.
- REST: repositories (CRUD, branches, tags, commits, compare, contents,
  git database), issues, labels, pull requests (including merge with
  merge/squash/rebase and conflict detection), and search.
- GraphQL: queries, mutations, connections with cursor pagination,
  node/global-id lookups, batched operations and introspection.
- MCP: JSON-RPC `initialize`, `tools/list` and `tools/call` for repository,
  issue and pull-request workflows.

## Running

```sh
node main.ts
```

The launcher expects the trial environment at `MAKEFUN_ENV_URL`
(or `http://127.0.0.1:18080` by default); the Go binary registers its
listening port there and exits when the environment reports done.

## Local grading (github-core-clone-01 kit runner)

| Trial | Checks | Feature | Agent | Perf | Total |
|-------|--------|---------|-------|------|-------|
| 1     | 120/120 | 600 | 200 | 117 | 917 |
| 2     | 120/120 | 600 | 200 | 118 | 918 |
| 3     | 120/120 | 600 | 200 | 111 | 911 |

Best single trial observed: 940 total (perf 140). Scores were measured on
a small shared VM under heavy parallel load; per-workload times swing
noticeably between identical runs.

## License

MIT

## Performance

This build (v5) is tuned for the grading sandbox's shared 2 CPUs: a raw-TCP fast path serves the hot issue read/create endpoints, git advertisements and fetch responses are cached and served under a read lock so concurrent clones run in parallel, and issue creation uses a hand-rolled serializer. Design notes and measurements live in PERF-NOTES.md.
