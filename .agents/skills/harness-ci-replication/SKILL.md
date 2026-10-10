---
name: harness-ci-replication
description: Use when the GitHub Actions Harness pipeline (golangci-lint, govulncheck, go vet, gofmt) is red for this repo and you must reproduce or fix it locally on Windows, when gh CLI is unavailable (repo private), when adding or renaming a lint rule in .golangci.yml, or when a CI-only failure appears that does not reproduce locally.
---

# Replicating the Harness CI pipeline locally (Windows)

## When to Use

- The `Harness` workflow (`.github/workflows/harness.yml`) fails on GitHub and you need to reproduce it locally.
- You are about to change `.golangci.yml` or `GO_VERSION` in the workflow.
- `gh` CLI is missing and the repo is private, so you cannot read the Actions log; you must replicate every step locally instead.
- A check passes locally but fails in CI (or vice versa) and you suspect line endings or toolchain version drift.

## When NOT to Use

- The failure is in runtime bot behavior (skills for chest/crafting/movement cover those).
- The failure is in a different workflow (there is only `harness.yml` today; re-check `.github/workflows/`).

## Pipeline steps (in CI order) and their local equivalents

CI runs on ubuntu-latest with `GO_VERSION` from the workflow env. Local Windows:

| CI step | Local equivalent | Gotcha |
|---|---|---|
| `gofmt -l .` | `gofmt -l .` | Only meaningful if working tree is LF (see `.gitattributes`). CRLF tree ⇒ everything "unformatted" |
| `go vet ./cmd/... ./internal/...` | same | |
| golangci-lint v2 (`--timeout 5m`) | download the pinned version from GitHub releases into `%TEMP%` and run it | v1 binaries cannot parse `go 1.26` modules — do not `go install golangci-lint@v1` |
| `govulncheck ./...` (`@latest`) | `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` | `@latest` locally vs CI can drift; findings are the union |
| build + unit tests + arch sensor + `go run ./cmd/harness` | same | |
| `go test -race` | **cannot run locally** — no cgo/gcc on this Windows machine | CI must be the verifier for race; keep race-sensitive changes behaviorally reviewable |
| coverage | `go test -coverprofile=coverage.out ./internal/... ./tests/...` | |

## Root causes this exact debugging uncovered (check these first)

1. **govulncheck stdlib findings follow the CI toolchain, not yours.** GO-2026-6613/6617
   (`net/http` HPACK race, CONNECT desync) fixed in go1.26.9 / go1.27.2. If the workflow pins an
   older `GO_VERSION`, bump it — do not "fix" stdlib findings in code.
2. **golangci-lint pinned version must be ≥ the Go version in go.mod.** v1.x line died before Go
   1.25. The pipeline now uses golangci-lint-action@v9 + `version: v2.14.0`.
3. **`core.autocrlf=true` on this machine** makes every `.go` file CRLF in the working tree while
   the git index is LF, so `gofmt -l` lists every file and lint reports "File is not properly
   formatted" per file. `.gitattributes` pins `*.go text eol=lf`; if a CRLF tree ever recurs,
   `git add --renormalize .` + commit, then convert the working tree files with
   `find . -name '*.go' -print0 | xargs -0 sed -i 's/\r$//'` (never `git reset --hard` here; the
   harness blocks it and `configs/bot.yaml` carries uncommitted local gateway settings).
4. **`configs/bot.yaml` is locally modified on purpose** (gateway `base_url`/`model` for local
   runs). When staging with `git add -A`, always `git restore --staged configs/bot.yaml` before
   committing. Same for `data/` runtime files (now gitignored).

## golangci-lint v2 migration notes (already done — read before re-editing the config)

- `golangci-lint migrate` converts v1 → v2, writes `.golangci.bck.yml`, and **drops comments**.
- `run.go: "1.26"` lives under `run:`; `issues.exclude-*` became `linters.exclusions.*` (presets
  + rules); `output.formats` is now a **map** (`text: {path: stdout}`), not a slice.
- govet analyzer names changed: `assignshift` is invalid; the valid pair is `assign` + `shift`.
- `misspell.locale` accepts only `US` or `UK` — **`GB` fails schema verification**. This
  codebase mixes UK/US spelling consistently (behaviour+normalized+neighbours+armour), so the
  `misspell` linter is disabled entirely; re-enabling it means a dedicated spelling pass.
- `staticcheck.checks: [all, -QF*, -ST*]` is in place to silence style-refactor suggestions;
  SA/S bug checks stay on.
- gosec excludes added with rationale comments: `G104`, `G115` (protocol-layer int conversions),
  `G117` (token-cache files), `G404` (math/rand jitter); `G703` is file-excluded for
  `internal/connection/capture.go` only (operator-set `BOT_PACKET_CAPTURE` path).
- `nolint` directives must sit on the line the issue is *reported* on (for multi-line calls: the
  first line of the call, not the closing paren).

## Procedure

1. Reproduce in CI order: gofmt → vet → golangci-lint (download pinned binary, e.g.
   `curl -L -o %TEMP%\gcl.zip https://github.com/golangci/golangci-lint/releases/download/v2.14.0/golangci-lint-2.14.0-windows-amd64.zip`)
   → govulncheck → build → tests → `go run ./cmd/harness`.
2. If lint reports typecheck errors in *other* files, `unused` results are unreliable for those
   files — finish the typecheck error first, then re-run.
3. Fix findings in this order: `golangci-lint run --fix` for mechanical ones (gofmt/goimports/
   whitespace/prealloc/unconvert), review that diff, then hand-fix the rest. For `unused` items,
   grep for every symbol before deleting — test fakes often look dead only while typecheck is
   broken elsewhere (`takenSlots` came back that way).
4. Never mass-rename British→American spelling; it touches AI prompt strings.
5. Validate: `config verify` (catches schema errors like `locale: GB`), `golangci-lint fmt`,
   then the full lint with the exact CI invocation `run --timeout 5m`.

## Verification steps

- `golangci-lint config verify` → exits 0.
- `golangci-lint run --timeout 5m` → `0 issues.`
- `go build ./cmd/... ./internal/... ./scripts/...` → silent.
- `go vet ./cmd/... ./internal/...` → silent.
- `go test -count=1 ./internal/... ./tests/...` → all `ok`.
- `go run ./cmd/harness` → `Errors: 0`.
- `govulncheck` → only stdlib findings matching the *local* toolchain are acceptable; the CI
  toolchain version must be ≥ the fixed-in version of every reported GO-id.
- Push, then confirm the run on github.com/Aofsnorth/DimsumMentAI/actions (cannot be checked
  locally without a token).
