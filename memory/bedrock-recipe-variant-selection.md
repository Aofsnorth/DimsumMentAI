---
name: bedrock-recipe-variant-selection
description: Why crafts fail with status 7 — multiple recipes per output collide in the name cache; pick by inventory
metadata:
  type: reference
---

Bedrock crafting bug: a craft command failed with server `status=7` (InvalidCraftRequest) even
though the bot had the right materials. Example: "craft 4 oak_planks" with 2 oak_log in inventory
rejected, because the chosen recipe's ingredient was `oak_wood` (netID -212), not `oak_log`.

Root cause: Bedrock registers MULTIPLE recipes for the same output (oak_planks from oak_log,
oak_wood, stripped_oak_log, stripped_oak_wood…). `handleCraftingData` in
`internal/bot/network/player/recipes.go` keys them by output name in `b.Recipes map[string]uint32`,
so each variant OVERWRITES the previous — the surviving netID is whichever the packet listed last,
often a variant the bot lacks ingredients for. `AutoCraftRecipe` then tells the server to consume
ingredients the bot doesn't have → status 7.

Fix: `Bot.FindCraftableRecipeID(itemName)` in `internal/bot/inventory.go` scans `RecipesByNetID`
for every recipe whose output matches, and returns the first whose ingredients are actually in
inventory (helper `recipeIngredientsAvailableLocked`). Matching is STRICT (exact network ID or
exact name) — deliberately NOT the fuzzy oak_wood↔oak_log canonicalization used by
`itemNameMatches`/`planIngredientConsumption`, because that fuzz is exactly what made the wrong
variant look satisfiable. `handleCraft` (`internal/bot/action/execute_handlers.go`) calls it first,
falling back to the `b.Recipes` name cache only if no recipe outputs the item.

Note: `b.Recipes map[string]uint32` is still single-valued and used widely (GetRecipes, building
acquisition, `inventory/crafting/table.go craftPlanksFromLogs`). Those callers only check "does a
recipe exist," but `craftPlanksFromLogs` MAY have the same latent variant bug if it relies on the
name cache — not hit by the reported chat-craft path, left unfixed.

**SECOND craft bug (also status=7), fixed separately:** even with the correct recipe variant
selected, crafts were rejected because `CraftItem` (`internal/bot/inventory.go`) sent only
`AutoCraftRecipe` + `Place`, NO `Consume` actions — based on a wrong comment claiming auto-craft
consumes ingredients server-side. The real vanilla recipe-book sequence is
`AutoCraftRecipe → Consume (one per ingredient slot) → Place`. Fixed by building `Consume`
(`protocol.ConsumeStackRequestAction`) from `planIngredientConsumption` picks, each referencing the
slot's server-assigned `StackNetworkID` (tracked in `b.StackNetworkIDs`, container
`ContainerCombinedHotBarAndInventory`, slots 0-35 map directly). Both fixes are needed together:
variant selection picks a recipe whose ingredients exist; Consume actions actually remove them.
