---
name: go-test-relocation-to-tests
description: >-
  Use when moving white-box Go tests from internal/ next to their source into
  tests/ as package X_test, in this repo. Triggers on "relocate tests",
  "black-box test", "external test package", "tests/ migration", or any build
  error of the shape "undefined someUnexportedName" or "unknown field x in
  struct literal of type Y" appearing in files under tests/.
---

# Relocating white-box Go tests from `internal/` to `tests/`

The contract is `docs/TEST_MIGRATION.md`. This skill is the operational
procedure that actually works, including the traps that cost time.

## When to Use

- A `*_test.go` sitting in `internal/...` declares `package X` (not `X_test`)
  and must live at `tests/<same/path>/`.
- After the move the compiler reports `undefined foo` or `unknown field bar in
  struct literal of type Baz` against a file under `tests/`.

## When NOT to Use

- The test is already `package X_test` under `tests/`.
- A pre-existing short-form dir (`tests/action/`, `tests/agi/`, `tests/world/`,
  `tests/entity/`, `tests/pathfinder/`, `tests/network/`, `tests/animation/`)
  is in scope — the contract says leave those alone.

## Procedure

### 0. Read the contract and the precedent

1. `docs/TEST_MIGRATION.md` — the rules and the trap list.
2. `tests/bot/durability/durability_test.go` — the target shape: `package
   durability_test`, a blank line, `"testing"`, blank line,
   `"bedrock-ai/internal/..."` in its own group, then third-party.
3. `internal/evidence/export_test_helpers.go` — the sanctioned shape for a
   test-only exported constructor/accessor.

### 1. Get the exact unexported identifier list

```sh
go run ./scripts/testexport > testexport-worklist.txt
```

It prints `FILE` / `TOP` / `METHOD` blocks to stdout and writes no file on its
own, so the redirect is what creates `testexport-worklist.txt`. Read only the
`FILE` blocks for your own packages.

**Known gap — do not trust the worklist alone.** `testexport` matches a
selector `x.field` only when `x` is a bare identifier *named after the struct
type*. A receiver named `mgr`, `m`, `ic`, `fm`, or `e` is missed. Grep the test
files yourself for `\.` on any manager/controller value you construct by
composite literal. In this repo the ones the worklist missed were
`mgr.windowID` (crafting), `fm.smeltBudget` (furnace),
`Explorer{bot:…, isExploring:…}` (exploration), and `m.mu` / `m.hungerLevel` /
`m.lastEatTime` / `m.deathTime` (survival).

### 2. Export what the test needs

Three categories, in order of preference:

| Case | Do this |
|---|---|
| package-level func / type / const / method | rename `foo` to `Foo`, update every in-package call site |
| unexported struct field a test pokes | add a named exported accessor, do **not** export the field |
| shared test double (`fakeBot`, `stubBot`) | move it into the new test package when the interface it satisfies is fully exported |

Renames are safe and complete — an unexported identifier's references all live in
its own package's directory, so `grep -rn` over that one directory finds all of
them. A `sed -i 's/\bfoo\b/Foo/g'` over the package's non-test `.go` files will
also rewrite a leading `// foo does X` doc comment, which is what `revive`'s
`exported` rule wants.

Add doc comments to anything you export that had none — `revive.exported` is
enabled in `.golangci.yml`.

**Field accessors go in `export_test_helpers.go`**, one small file per package,
with a comment saying why. Do not export a mutex-guarded field: put the lock
inside the accessor.

### 3. Move and requalify

- `package X` becomes `package X_test`
- add `"bedrock-ai/internal/<path>"` to the import block
- qualify every package-level reference

**Qualify by hand, not by regex.** A textual rename hits things it must not:
comments, string literals, `t.Errorf` message text, struct field names in
composite literals, and method declarations on local test doubles. The contract
calls this out; `scripts/qualify_tests.py` has the same bug (it walks *all* of
`tests/`, so it would also corrupt a concurrent agent's files — do not run it
in a shared tree).

### 4. Verify

```sh
gofmt -w <touched dirs>
go build ./...
go vet ./...
go test ./... -timeout 180s
```

Scope your own packages when the tree is shared by several agents:

```sh
go vet ./internal/bot/<yours>/... ./tests/bot/<yours>/...
```

## Pitfalls

- **`tests/bot/` already declares `package bot_test`.** A test moved from
  `internal/bot/` goes there too, not into a new subdirectory.
- **A colon-space in YAML frontmatter breaks the skill.** `description: foo
  "undefined: bar"` is a plain scalar containing a mapping indicator, so the
  loader fails with `mapping values are not allowed here`. Use a `>-` folded
  block scalar, or drop the colon.
- **A migrated copy may already exist** (`tests/bot/fov/fov_test.go` existed
  while `internal/bot/fov/fov_test.go` also did). `diff` the two; if the only
  differences are the package clause and qualification, just delete the
  internal one.
- **Test doubles usually just work.** If the package's `Bot` interface has only
  exported methods, the fake satisfies it from another package unchanged — you
  only qualify references *inside* the fake's own methods' bodies (rarely
  needed) and references to package-level symbols the fake uses.
- **Compare test-function counts before/after** (`grep -c '^func Test'` on the
  `git show HEAD:<path>` version vs. the new file). A relocation that silently
  loses a test is worse than one that doesn't compile.
- **`gofmt -l` will flag many pre-existing files** in this repo. Leave them;
  only format what you touched.
- **Renamed symbols leave stale mentions in comments elsewhere.** For example
  `internal/bot/inventory_actions.go` still says `applyItemStackResponse` in a
  comment. If that file is outside your write scope, leave it and report it.
- **Untracked `internal/bot/agi/*_test.go` files** can be another agent's
  in-flight work. If `go test ./...` fails in a package you do not own, check
  `git status --porcelain` for that path before assuming you broke it.

## Verification Checklist

- [ ] `find <your internal dirs> -name '*_test.go'` returns nothing
- [ ] every new file is `package X_test` and imports the package under test
- [ ] `go vet` clean for `./internal/<yours>/...` and `./tests/<yours>/...`
- [ ] `go test` green for your scope, `-count=1`
- [ ] test-function count per file matches the pre-move count
- [ ] `gofmt -l` flags none of your files
