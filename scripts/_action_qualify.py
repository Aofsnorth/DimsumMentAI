"""Rewrite the relocated action tests as a black-box `action_test` package.

Two passes, in order:

1.  `package action` -> `package action_test` plus the import of the package
    under test.
2.  Qualify every bare package-level reference with `action.`.

The qualifier pass is the error-prone one: a textual rename also hits comments,
string literals and struct field names, so each symbol is restricted to the
positions where it can legally appear — a call `f(`, a type position, a
selector `.X` after a qualifier, or a declaration.
"""

import os
import re

TESTS = os.path.join(os.getcwd(), "tests", "bot", "action")

# Symbols the source package exported, minus the ones that are only ever
# mentioned in prose. Anything not in this map must already be exported.
MAP = {
    "Abs32": "Abs32",
    "ActionHandler": "ActionHandler",
    "ActionHandlers": "ActionHandlers",
    "ActionHandlersMu": "ActionHandlersMu",
    "BestStrongholdHint": "BestStrongholdHint",
    "BlockLookup": "BlockLookup",
    "BotBlockLookup": "BotBlockLookup",
    "CandidateMaterialAvailable": "CandidateMaterialAvailable",
    "CellNames": "CellNames",
    "ComputeCrafts": "ComputeCrafts",
    "ConvergeOnHint": "ConvergeOnHint",
    "CountInventoryItems": "CountInventoryItems",
    "DimensionChanged": "DimensionChanged",
    "DurationTicks": "DurationTicks",
    "EmptyFrameCells": "EmptyFrameCells",
    "ExecuteAndWait": "ExecuteAndWait",
    "EndPortalInteriorCell": "EndPortalInteriorCell",
    "EndPortalPlan": "EndPortalPlan",
    "EndPortalScanRadius": "EndPortalScanRadius",
    "ExecuteAndWaitWithTimeout": "ExecuteAndWaitWithTimeout",
    "FramesNeedingEyes": "FramesNeedingEyes",
    "IngredientFallbacks": "IngredientFallbacks",
    "IsEyeOfEnder": "IsEyeOfEnder",
    "IsStrongholdCore": "IsStrongholdCore",
    "IsWoodLike": "IsWoodLike",
    "LargestFrameCluster": "LargestFrameCluster",
    "NormalizeCropType": "NormalizeCropType",
    "NormalizeIngredientKey": "NormalizeIngredientKey",
    "NormalizeItemName": "NormalizeItemName",
    "ParseCount": "ParseCount",
    "PlanEndEnter": "PlanEndEnter",
    "PlanEndFill": "PlanEndFill",
    "PlanEndNotPortal": "PlanEndNotPortal",
    "PlanEndPortal": "PlanEndPortal",
    "PlanEnterLit": "PlanEnterLit",
    "PlanLight": "PlanLight",
    "PlanNotPortal": "PlanNotPortal",
    "PortalInteriorCell": "PortalInteriorCell",
    "PortalParts": "PortalParts",
    "PortalPlan": "PortalPlan",
    "PortalScan": "PortalScan",
    "PosLess": "PosLess",
    "RecipeNeedsCraftingBench": "RecipeNeedsCraftingBench",
    "ReportInventoryDelta": "ReportInventoryDelta",
    "ReportStatus": "ReportStatus",
    "ResolveIngredientCandidates": "ResolveIngredientCandidates",
    "RouteEndPortalState": "RouteEndPortalState",
    "RoutePortalState": "RoutePortalState",
    "ScanEndPortal": "ScanEndPortal",
    "SelectAttackTarget": "SelectAttackTarget",
    "SelectIgnitionTarget": "SelectIgnitionTarget",
    "StrongholdHint": "StrongholdHint",
    "StrongholdRingStep": "StrongholdRingStep",
    "StrongholdWaypoints": "StrongholdWaypoints",
    "SubscribeStatus": "SubscribeStatus",
    "UnsubscribeStatus": "UnsubscribeStatus",
}

ORDER = sorted(MAP, key=len, reverse=True)


def qualify(body):
    for sym in ORDER:
        pat = re.compile(rf"(?<![\w.]){re.escape(sym)}\b")
        out = []
        last = 0
        for m in pat.finditer(body):
            # Skip anything inside a string literal or a line comment.
            if in_literal(body, m.start()):
                continue
            out.append(body[last:m.start()])
            out.append("action." + sym)
            last = m.end()
        out.append(body[last:])
        body = "".join(out)
    return body


def in_literal(body, i):
    """True when offset i sits inside a string literal or a // comment."""
    line_start = body.rfind("\n", 0, i) + 1
    line = body[line_start:i]
    # Count unescaped quotes; an odd number means we are inside a literal.
    q = len(re.findall(r'(?<!\\)"', line))
    if q % 2 == 1:
        return True
    if "//" in line and not re.search(r'"[^"\n]*//', line):
        return True
    # Block comment carried over from an earlier line.
    return in_block_comment(body, i)


def in_block_comment(body, i):
    return len(re.findall(r"/\*", body[:i])) != len(re.findall(r"\*/", body[:i]))


def main():
    for f in sorted(os.listdir(TESTS)):
        if not f.endswith("_test.go"):
            continue
        p = os.path.join(TESTS, f)
        with open(p, encoding="utf-8") as fh:
            body = fh.read()

        body = body.replace("package action\n", "package action_test\n", 1)
        if '"bedrock-ai/internal/bot/action"' not in body:
            body = re.sub(
                r"^import \(\n",
                'import (\n\t"bedrock-ai/internal/bot/action"\n',
                body,
                count=1,
                flags=re.MULTILINE,
            )
        body = qualify(body)
        with open(p, "w", encoding="utf-8") as fh:
            fh.write(body)
        print("rewrote", f)


if __name__ == "__main__":
    main()
