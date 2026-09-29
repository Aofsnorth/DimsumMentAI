#!/usr/bin/env bash
# Classify each *_test.go under internal/ as MOVABLE or WHITEBOX.
#
# A Go test declared as `package X` beside its code may use unexported
# identifiers. Relocating it to tests/ makes it a separate package, and every
# unexported reference stops compiling. So the question per file is binary:
# does it name an unexported top-level identifier declared by its own package?
set -uo pipefail

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

for dir in $(find ./internal -name '*_test.go' -exec dirname {} \; | sort -u); do
  # One regex per unexported top-level identifier, anchored so a selector
  # (x.foo) or a longer name (foobar) does not count as a reference to foo.
  pat="$tmp/pat"
  : > "$pat"
  for f in "$dir"/*.go; do
    [ -e "$f" ] || continue
    case "$f" in *_test.go) continue ;; esac
    {
      sed -nE 's/^func \([^)]*\) ([a-z][A-Za-z0-9_]*)\(.*/\1/p' "$f"
      sed -nE 's/^func ([a-z][A-Za-z0-9_]*)\(.*/\1/p'                 "$f"
      sed -nE 's/^type ([a-z][A-Za-z0-9_]*).*/\1/p'                   "$f"
      sed -nE 's/^var ([a-z][A-Za-z0-9_]*).*/\1/p'                   "$f"
      sed -nE 's/^const ([a-z][A-Za-z0-9_]*).*/\1/p'                 "$f"
      sed -nE 's/^\t([a-z][A-Za-z0-9_]*)[[:space:]]+[^=]*=.*/\1/p'   "$f"
    } | sort -u | sed -E 's/^/(^|[^A-Za-z0-9_.])/; s/$/([^A-Za-z0-9_]|$)/' >> "$pat"
  done

  for tf in "$dir"/*_test.go; do
    [ -e "$tf" ] || continue
    if [ ! -s "$pat" ]; then
      echo "MOVABLE|$tf|"
      continue
    fi
    hits=$(grep -oE -f "$pat" "$tf" 2>/dev/null | wc -l)
    if [ "$hits" -eq 0 ]; then
      echo "MOVABLE|$tf|"
    else
      names=$(grep -oE -f "$pat" "$tf" 2>/dev/null | tr -d ' \n' | cut -c1-120)
      echo "WHITEBOX|$tf|$names"
    fi
  done
done
