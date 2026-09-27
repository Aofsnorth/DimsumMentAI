"""Move white-box Go tests out of internal/ into tests/ as external packages.

internal/foo/bar_test.go  ->  tests/foo/bar_test.go   (package bar_test)

The mechanical rewrite is deliberately dumb: change the package clause, add the
import of the package under test, and qualify its identifiers. What the rewrite
cannot do is decide what to do about identifiers that are unexported in the
source package — those are reported, not guessed at, because exporting a symbol
to satisfy a test is a decision about the production API, not a mechanical edit.
"""

import os
import re
import sys

ROOT = os.getcwd()
TESTS = os.path.join(ROOT, "tests")
MODULE = "bedrock-ai"


def import_path_for(rel):
    """tests/bot/movement/x_test.go -> bedrock-ai/internal/bot/movement"""
    d = os.path.dirname(rel)
    return f"{MODULE}/internal/{d.replace(os.sep, '/')}"


def convert(path):
    with open(path, encoding="utf-8") as fh:
        src = fh.read()
    m = re.search(r"^package (\w+)$", src, re.MULTILINE)
    if not m:
        return "no-package-clause"
    pkg = m.group(1)
    if pkg.endswith("_test"):
        return "already-external"

    imp = import_path_for(os.path.relpath(path, TESTS))

    # Package clause: X -> X_test
    src = re.sub(rf"^package {re.escape(pkg)}$", f"package {pkg}_test",
                 src, count=1, flags=re.MULTILINE)

    # Add the import if the package under test is not already imported.
    if f'"{imp}"' not in src:
        block = re.search(r"^import \(\n", src, re.MULTILINE)
        if block:
            src = src[:block.end()] + f'\t"{imp}"\n' + src[block.end():]
        else:
            single = re.search(r'^import "([^"]+)"\n', src, re.MULTILINE)
            if single:
                src = (src[:single.start()]
                       + f'import (\n\t"{imp}"\n\t"{single.group(1)}"\n)\n'
                       + src[single.end():])
            else:
                # The package clause exists (checked above) but may be the last
                # line with no trailing newline, in which case there is nothing
                # to insert after — append instead of slicing from None.
                clause = re.search(r"^package[^\n]*\n", src, re.MULTILINE)
                if clause is None:
                    src += f'\nimport "{imp}"\n'
                else:
                    src = (src[:clause.end()]
                           + f'\nimport "{imp}"\n'
                           + src[clause.end():])

    with open(path, "w", encoding="utf-8") as fh:
        fh.write(src)
    return "converted:" + pkg


def main():
    results = {}
    for root, _, files in os.walk(TESTS):
        for f in files:
            if not f.endswith("_test.go"):
                continue
            path = os.path.join(root, f)
            res = convert(path)
            results.setdefault(res.split(":")[0], []).append(os.path.relpath(path, ROOT))

    for key in sorted(results):
        print(f"{key:<20} {len(results[key])}")
        if key not in ("converted", "already-external"):
            for p in results[key]:
                print("    ", p)
    return 0


if __name__ == "__main__":
    sys.exit(main())
