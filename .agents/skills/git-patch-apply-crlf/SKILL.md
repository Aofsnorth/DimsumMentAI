---
name: git-patch-apply-crlf
description: Use when `git apply <file>.patch` fails with "patch does not apply" for every file in this repo, when a patch file is checked out with CRLF line endings on Windows, or when a patch contains a corrupted hunk line. Covers the LF-vs-CRLF context mismatch caused by .gitattributes `eol=lf`, and how to repair a patch whose working-tree copy drifted from the committed blob.
---

# Applying `.patch` files in this repo (Windows CRLF trap)

## When to Use

- `git apply some.patch` reports `patch failed: <file>:<line>` / `patch does not apply`
  for **every** file in the patch.
- `git apply --check` fails but the patch content looks correct.
- A patch file in the working tree differs from the committed blob (corrupted hunk).

## When NOT to Use

- A single file fails to apply while others succeed → that is a real content
  conflict, not a line-ending problem; resolve it by hand.
- The patch was generated against a different base commit.

## Root cause

`.gitattributes` forces LF for sources (`*.go text eol=lf`) while `* text=auto`
leaves other files (e.g. `*.patch`) at the platform native ending. With
`core.autocrlf=true` on Windows the patch file is checked out with **CRLF**,
but the `.go` files it targets are **LF**. `git apply` matches hunk context
byte-for-byte, so every context line fails to match.

Diagnostic tell: `git apply --check --verbose` prints the searched context with
a trailing `?` on each line — that marker means the patch line ends with CR.

## Procedure

1. Confirm the line-ending mismatch:
   ```sh
   od -c fix-chat-and-actions.patch | sed -n '1,4p'   # look for \r\n
   od -c internal/ai/nvidia.go | sed -n '1,4p'        # look for bare \n
   ```
2. Restore the patch file if the working-tree copy was hand-edited and drifted:
   ```sh
   git restore fix-chat-and-actions.patch
   ```
   The committed blob is the source of truth. A corrupted line such as
   `"wave":   as b   {name: "nod", ...}` is not valid Go and will silently
   break the build once the patch lands.
3. Normalize the patch to LF before applying:
   ```sh
   sed -i 's/\r$//' fix-chat-and-actions.patch
   ```
4. Dry-run, then apply:
   ```sh
   git apply --check fix-chat-and-actions.patch
   git apply fix-chat-and-actions.patch
   ```
5. Make it permanent so the file never checks out as CRLF again — add to
   `.gitattributes`:
   ```
   *.patch text eol=lf
   ```
   With that rule a plain `git checkout -- <patch>` already yields LF.

## Pitfalls

- **`git apply` is atomic.** One bad hunk aborts the whole patch; a partially
  applied tree is not a thing. If it fails, the tree is untouched.
- **A reverse apply (`git apply -R`) needs the tree to be exactly
  post-patch.** Any hand-fix you made on top (e.g. a `gofmt` correction to a
  file the patch touches) breaks the reverse hunk's context. `git checkout --`
  that file first.
- **`git apply` does not touch the index.** After applying, `git status` shows
  `MM` (staged + unstaged) if the index held an older state. `git add` to sync.
- **Do not use `git apply --ignore-whitespace` as a workaround**: it matches,
  but it writes the patch's CRLF line endings into the `.go` files, which then
  fail the `gofmt`/lint gates.

## Verification

- `git apply --check <patch>` exits 0.
- `gofmt -l .` is empty for the files the patch touched.
- `go build ./cmd/... ./internal/...` succeeds.
- `git --no-pager diff HEAD --stat` shows only what you intended.
