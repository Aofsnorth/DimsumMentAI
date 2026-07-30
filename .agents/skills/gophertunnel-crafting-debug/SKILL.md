---
name: gophertunnel-crafting-debug
description: Debug Bedrock crafting failures in this Go bot when ItemStackRequest is rejected, especially status 7 InvalidCraftRequest or wrong wood-variant fallback.
---

# Gophertunnel Crafting Debug

## When to Use

Use when `CraftItem` receives an `ItemStackResponse` rejection, crafting works for one item but fails in a chain, a generic recipe ingredient resolves to the wrong wood variant, or crafting succeeds but the bot's local ingredient counts remain stale.

## When NOT to Use

Do not use for connection shutdowns unrelated to `ItemStackRequest`, world placement failures, or recipes requiring furnace/stonecutter-specific UI.

## Procedure

1. Map the numeric response status to `gophertunnel/minecraft/protocol/item_stack.go`; do not infer from the number alone.
2. Inspect the first stack request action:
   - `CraftRecipeStackRequestAction` means the crafting grid is already populated.
   - `AutoCraftRecipeStackRequestAction` means recipe-book auto-crafting from inventory; include `NumberOfCrafts`, `TimesCrafted`, and `Ingredients`.
3. Prefer a packet capture from the same BDS/protocol version over trial-and-error action changes.
4. Open the personal inventory before transfers. Send `InteractActionOpenInventory` with `TargetEntityRuntimeID` set to the bot's own runtime entity ID; BDS rejects a cursor destination when the target is left at zero.
5. Reproduce the vanilla manual-grid sequence as separate awaited requests:
   - `Take` inventory/hotbar to `ContainerCursor`.
   - `Place` cursor to `ContainerCraftingInput`.
   - `CraftRecipe`, `CraftResultsDeprecated`, grid `Consume`, then created-output `Place`.
6. Use authoritative stack IDs from each `ItemStackResponse`. Use a negative request ID only when the server response omits a predicted stack ID.
7. Match personal-grid placement: slots start at `28`; captured 1x1 uses `29`; a vertical 1x2 recipe uses `29` and `31`.
8. Map player slots `0..8` to `ContainerHotBar`; map slots `9..35` to `ContainerInventory`. BDS status `49` confirms that `ContainerInventory` is invalid for hotbar slot zero.
9. Treat response slots as global `0..35`; do not apply the `+9` offset used by partial `InventoryContent`.
10. For chain crafting, retain every recipe candidate per output. Score candidates using current inventory before selecting a wood variant.
11. Normalize only generic tags such as `minecraft:planks`; never turn a specific variant like `minecraft:oak_planks` into a fallback list.
12. If an accepted craft leaves stale ingredient counts locally, snapshot each source slot before staging ingredients. After the final craft request is accepted, reconcile only matching stacks whose local count is still above `snapshot count - consumed count`. Preserve lower server-authoritative counts and slots replaced with another item.
13. Run `gofmt`, focused bot tests, then `go test ./...`. Run the clean BDS harness repeatedly for protocol changes.

## Common Pitfalls

- `status=7` is `ItemStackResponseStatusInvalidCraftRequest` in the pinned gophertunnel version.
- `status=50` is `ItemStackResponseStatusFailedToValidateDstSlot`; with a valid hotbar source and cursor destination, check that inventory-open targets the player itself.
- `ContainerCreatedOutput` source must use the current negative request ID; an empty destination must use stack network ID `0`. `-1` is not a generic prediction placeholder.
- Replacing auto-craft with normal craft silently fails because the server expects pre-filled grid input.
- Sending `Take`, `Place`, and `CraftRecipe` without awaiting each response uses stale stack IDs.
- A rejected final craft leaves staged ingredients in the grid unless they are explicitly returned. Do not issue recovery after a timeout because the delayed original response may still succeed.
- Bidirectional substring matching can let `oak_log` satisfy `dark_oak_log`; generic matching must require a word-boundary suffix.
- Some accepted transfer responses do not leave `InventoryMap` with the final ingredient count. Do not blindly decrement on every response: reconcile once after final acceptance, and make the operation idempotent so authoritative lower counts are never increased.
- Do not edit `data/bot_state.json` when runtime position changes are unrelated to the fix.

## Verification

- Focused tests assert the standalone transfer actions, 1x1/1x2 grid slots, final manual craft action order, current request ID, empty destination ID `0`, and global `ContainerInventory` response slots.
- `go test ./internal/bot ./internal/bot/action ./internal/bot/network/player` passes.
- `go test ./...` passes.
- Re-run the clean BDS harness with one oak log and verify exactly four sticks are removed using a limit greater than four; a clear limit of four cannot prove there were no extra sticks.
- Verify the server has exactly two planks left and the bot's local inventory summary reports the same count after crafting four sticks.
- Run at least three clean cycles. Each must accept oak planks before sticks with no rejection, timeout, desync, disconnect, or crash.
