"""One-shot: export the identifiers the relocated action tests need.

An unexported identifier is package-private, so every reference already lives
inside its own directory. Renaming within internal/bot/action is therefore
complete by construction.
"""

import os
import re

DIR = os.path.join(os.getcwd(), "internal", "bot", "action")

MAP = {
    "selectAttackTarget": "SelectAttackTarget",
    "candidateMaterialAvailable": "CandidateMaterialAvailable",
    "computeCrafts": "ComputeCrafts",
    "ingredientFallbacks": "IngredientFallbacks",
    "normalizeIngredientKey": "NormalizeIngredientKey",
    "resolveIngredientCandidates": "ResolveIngredientCandidates",
    "durationTicks": "DurationTicks",
    "isWoodLike": "IsWoodLike",
    "normalizeCropType": "NormalizeCropType",
    "normalizeItemName": "NormalizeItemName",
    "parseCount": "ParseCount",
    "recipeNeedsCraftingBench": "RecipeNeedsCraftingBench",
    "botBlockLookup": "BotBlockLookup",
    "actionHandler": "ActionHandler",
    "actionHandlers": "ActionHandlers",
    "actionHandlersMu": "ActionHandlersMu",
    "countInventoryItems": "CountInventoryItems",
    "executeAndWait": "ExecuteAndWaitWithTimeout",
    "reportInventoryDelta": "ReportInventoryDelta",
    "reportStatus": "ReportStatus",
    "subscribeStatus": "SubscribeStatus",
    "unsubscribeStatus": "UnsubscribeStatus",
    "abs32": "Abs32",
    "cellNames": "CellNames",
    "dimensionChanged": "DimensionChanged",
    "emptyFrameCells": "EmptyFrameCells",
    "endPortalInteriorCell": "EndPortalInteriorCell",
    "endPortalPlan": "EndPortalPlan",
    "endPortalScanRadius": "EndPortalScanRadius",
    "framesNeedingEyes": "FramesNeedingEyes",
    "isEyeOfEnder": "IsEyeOfEnder",
    "largestFrameCluster": "LargestFrameCluster",
    "planEndEnter": "PlanEndEnter",
    "planEndFill": "PlanEndFill",
    "planEndNotPortal": "PlanEndNotPortal",
    "portalParts": "PortalParts",
    "posLess": "PosLess",
    "routeEndPortalState": "RouteEndPortalState",
    "scanEndPortal": "ScanEndPortal",
    "blockLookup": "BlockLookup",
    "planEndPortal": "PlanEndPortal",
    "planEnterLit": "PlanEnterLit",
    "planLight": "PlanLight",
    "planNotPortal": "PlanNotPortal",
    "portalInteriorCell": "PortalInteriorCell",
    "portalPlan": "PortalPlan",
    "portalScan": "PortalScan",
    "routePortalState": "RoutePortalState",
    "selectIgnitionTarget": "SelectIgnitionTarget",
    "bestStrongholdHint": "BestStrongholdHint",
    "convergeOnHint": "ConvergeOnHint",
    "isStrongholdCore": "IsStrongholdCore",
    "strongholdHint": "StrongholdHint",
    "strongholdRingStep": "StrongholdRingStep",
    "strongholdWaypoints": "StrongholdWaypoints",
}

# longest first so ActionHandlersMu is rewritten before ActionHandlers
ORDER = sorted(MAP, key=len, reverse=True)


def main():
    for f in sorted(os.listdir(DIR)):
        if not f.endswith(".go"):
            continue
        p = os.path.join(DIR, f)
        with open(p, encoding="utf-8") as fh:
            body = fh.read()
        orig = body
        for old in ORDER:
            body = re.sub(rf"(?<![\w.]){re.escape(old)}\b", MAP[old], body)
        # strongholdHint's two fields are set in a composite literal by the test.
        if f == "stronghold_handlers.go":
            for old, new in (
                (r"(?<![\w.])pos(?=\s+protocol\.BlockPos$)", "Pos"),
                (r"(?<![\w.])strong(?=\s+bool$)", "Strong"),
                (r"(?<![\w.])(hint|h|best)\.pos\b", r"\1.Pos"),
                (r"(?<![\w.])(hint|h|best)\.strong\b", r"\1.Strong"),
                (r"(?<![\w.])pos:(?=\s*protocol\.BlockPos\{)", "Pos:"),
                (r"(?<![\w.])pos:(?=\s*pos\b)", "Pos:"),
                (r"(?<![\w.])strong:(?=\s*isStrongholdCore)", "Strong:"),
            ):
                body = re.sub(old, new, body, flags=re.MULTILINE)
        if body != orig:
            with open(p, "w", encoding="utf-8") as fh:
                fh.write(body)
            print("rewrote", f)


if __name__ == "__main__":
    main()
