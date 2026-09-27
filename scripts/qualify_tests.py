"""Second pass: point the relocated tests at the exported symbols.

The first pass exported the symbols in production code. This pass rewrites the
moved tests to call them through the package qualifier, which is what an
external test package requires:

    selectAttackTarget(b, ...)   ->   action.SelectAttackTarget(b, ...)
"""

import os
import re
import sys

ROOT = os.getcwd()
TESTS = os.path.join(ROOT, "tests")


def import_path_for(test_rel):
    parts = test_rel.replace("\\", "/").split("/")
    return "bedrock-ai/internal/" + "/".join(parts[1:-1])


def last_segment(imp):
    return imp.rsplit("/", 1)[-1]


def qualify(symbol_map, default_alias=None):
    """symbol_map: {old_name: new_name}"""
    changed = 0
    for root, _, files in os.walk(TESTS):
        for f in files:
            if not f.endswith("_test.go"):
                continue
            path = os.path.join(root, f)
            rel = os.path.relpath(path, TESTS)
            alias = last_segment(import_path_for(rel))
            with open(path, encoding="utf-8") as fh:
                body = fh.read()
            orig = body

            for old, new in symbol_map.items():
                if old == new:
                    continue
                # Bare reference that is not already qualified.
                body = re.sub(
                    rf"(?<![\w.]){re.escape(old)}\b",
                    f"{alias}.{new}",
                    body,
                )
            if body != orig:
                with open(path, "w", encoding="utf-8") as fh:
                    fh.write(body)
                changed += 1
    return changed


def qualify_blockface():
    """BlockFaceTop lives in gophertunnel, not this module."""
    changed = 0
    for root, _, files in os.walk(TESTS):
        for f in files:
            if not f.endswith("_test.go"):
                continue
            path = os.path.join(root, f)
            with open(path, encoding="utf-8") as fh:
                body = fh.read()
            if not re.search(r"(?<![\w.])BlockFaceTop\b", body):
                continue
            new = re.sub(r"(?<![\w.])BlockFaceTop\b", "protocol.BlockFaceTop", body)
            if '"github.com/sandertv/gophertunnel/minecraft/protocol"' not in new:
                m = re.search(r"^import \(\n", new, re.MULTILINE)
                if m:
                    new = (new[:m.end()]
                           + '\t"github.com/sandertv/gophertunnel/minecraft/protocol"\n'
                           + new[m.end():])
            with open(path, "w", encoding="utf-8") as fh:
                fh.write(new)
            changed += 1
    return changed


SYMBOL_MAP = {
    "defaultContextWindow": "DefaultContextWindow",
    "dropYaw": "DropYaw",
    "gatherOutcome": "GatherOutcome",
    "lanConnectionType": "LanConnectionType",
    "mergeMoveActorDeltaPosition": "MergeMoveActorDeltaPosition",
    "selectAttackTarget": "SelectAttackTarget",
    "walkingHeadTarget": "WalkingHeadTarget",
}


def main():
    n = qualify(SYMBOL_MAP)
    m = qualify_blockface()
    print(f"rewrote {n} test files (symbols), {m} (BlockFace)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
