"""Export the identifiers the relocated tests need, and qualify the rest.

Two things happen here, in order:

1.  Exported-but-unqualified: a moved test says `NewCombatManager(...)` where the
    external package requires `combat.NewCombatManager(...)`. Fixed by
    qualification only — no production code changes.

2.  Unexported: a moved test says `selectAttackTarget(...)`, which an external
    package cannot reach. Fixed by exporting the symbol.

Step 2 is the only one that changes production code, and it is safe to do
mechanically for a reason worth stating: an unexported identifier is
package-private, so every reference to it already lives inside that package's own
directory. Renaming within that directory is therefore complete — there is no
caller elsewhere that the rename could miss.
"""

import os
import re
import subprocess
import sys

ROOT = os.getcwd()


def collect_vet_errors():
    """Return {symbol: [test files]} from `go vet ./tests/...`.

    vet exits non-zero when undefined symbols exist, which is the expected
    outcome here, so the exit status is not treated as a failure.
    """
    out = subprocess.run(
        ["go", "vet", "./tests/..."],
        capture_output=True, text=True, cwd=ROOT, check=False,
    )
    text = out.stdout + out.stderr
    errors = {}
    # Lines look like: vet.exe: tests\ai\context_test.go:17:45: undefined: defaultContextWindow
    pat = re.compile(r"^(?:vet\.exe:\s*)?(tests[\\/].+?):\d+:\d+:\s+undefined:\s+(\w+)\s*$", re.MULTILINE)
    for m in pat.finditer(text):
        path, sym = m.group(1), m.group(2)
        errors.setdefault(sym, []).append(path)
    return errors


def source_dir_for_test(test_rel):
    """tests/ai/context_test.go -> internal/ai"""
    parts = test_rel.replace("\\", "/").split("/")
    return os.path.join(ROOT, "internal", *parts[1:-1])


def find_symbol(dirpath, sym):
    """Locate a top-level declaration of sym inside dirpath."""
    decl = re.compile(
        "|".join([
            rf"^func\s+{re.escape(sym)}\b",               # func foo(
            rf"^func\s+\([^)]*\)\s+{re.escape(sym)}\b",   # func (r T) foo(
            rf"^type\s+{re.escape(sym)}\b",
            rf"^const\s+{re.escape(sym)}\b",
            rf"^var\s+{re.escape(sym)}\b",
        ]),
        re.MULTILINE,
    )
    hits = []
    for f in sorted(os.listdir(dirpath)):
        if not f.endswith(".go"):
            continue
        p = os.path.join(dirpath, f)
        with open(p, encoding="utf-8") as fh:
            body = fh.read()
        if decl.search(body):
            hits.append(p)
    return hits


def export_symbol(files, sym):
    """Rename sym to its exported form inside the given files only."""
    exported = sym[0].upper() + sym[1:]
    pat = re.compile(rf"\b{re.escape(sym)}\b")
    for p in files:
        with open(p, encoding="utf-8") as fh:
            body = fh.read()
        with open(p, "w", encoding="utf-8") as fh:
            fh.write(pat.sub(exported, body))
    return exported


def main():
    errors = collect_vet_errors()
    if not errors:
        print("no undefined symbols")
        return 0

    print(f"undefined symbols: {len(errors)}\n")

    for sym, tests in sorted(errors.items()):
        tdir = source_dir_for_test(tests[0])
        hits = find_symbol(tdir, sym)
        if hits:
            new = export_symbol(hits, sym)
            print(f"  export {sym:<28} -> {new:<28} ({os.path.relpath(tdir, ROOT)})")
        else:
            print(f"  QUALIFY {sym:<26} (not declared as a top-level symbol)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
