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
4. Open the personal inventory before transfers. Send `InteractActionOpenInventory` with `TargetEntityRuntimeID` set to the bot's own runtime entity ID; BDS rejects a cursor destination when the target is left at zero. **Do not** send this for a table craft: it replaces the open workbench.
5. Reproduce the vanilla manual-grid sequence as separate awaited requests:
   - `Take` inventory/hotbar to `ContainerCursor`.
   - `Place` cursor to `ContainerCraftingInput`.
   - `CraftRecipe`, `CraftResultsDeprecated`, grid `Consume`, then created-output `Place`.
6. Use authoritative stack IDs from each `ItemStackResponse`. Use a negative request ID only when the server response omits a predicted stack ID.
7. Match personal-grid placement: slots start at `28`; captured 1x1 uses `29`; a vertical 1x2 recipe uses `29` and `31`. A **table** grid is slots `32..40`, the same `ContainerCraftingInput` container with a different offset — see `CraftingTableGridBaseSlot` and `craftingTableGridSlot`.
8. Map player slots `0..8` to `ContainerHotBar`; map slots `9..35` to `ContainerInventory`. BDS status `49` confirms that `ContainerInventory` is invalid for hotbar slot zero.
9. Treat response slots as global `0..35`; do not apply the `+9` offset used by partial `InventoryContent`.
10. For chain crafting, retain every recipe candidate per output. Score candidates using current inventory before selecting a wood variant.
11. Normalize only generic tags such as `minecraft:planks`; never turn a specific variant like `minecraft:oak_planks` into a fallback list.
12. If an accepted craft leaves stale ingredient counts locally, snapshot each source slot before staging ingredients. After the final craft request is accepted, reconcile only matching stacks whose local count is still above `snapshot count - consumed count`. Preserve lower server-authoritative counts and slots replaced with another item.
13. Run `gofmt`, focused bot tests, then `go test ./...`. Run the clean BDS harness repeatedly for protocol changes.

## Crafting a 3×3 Recipe (the table path)

`CraftItemOnTable` stages into the table grid and then reuses the same
`buildCraftActions` as the personal grid. It is deliberately **not** AutoCraft:
AutoCraft sends a marshalled `StackRequestItemDescriptorCount` list, and a host
that disagrees about the descriptor shape loses the stream mid-read and kills
the session with a packet violation (`the input is outside of the accepted
range`, packet 147) rather than a craft rejection.

Two preconditions, both of which used to be skipped:

- **The workbench must be confirmed open.** A click plus a fixed sleep is not
  an open. Arm the container watch *before* the click, click through the
  interactor, and wait for the server's `ContainerOpen`. Staging into slots
  32..40 while the server still has the personal 2×2 grid open is a guaranteed
  rejection, and it strands items in the grid.
- **The window must be closed by the ID the server assigned.** Closing a
  guessed window ID closes nothing, and the server holds the workbench open,
  so the *next* personal craft goes into the wrong grid.

Empty cells in a shaped recipe are still counted positions: a shaped recipe
anchors at the top-left of the 3×3 grid, `slot = 32 + row*3 + column`.

## Common Pitfalls

- `status=7` is `ItemStackResponseStatusInvalidCraftRequest` in the pinned gophertunnel version.
- `status=50` is `ItemStackResponseStatusFailedToValidateDstSlot`; with a valid hotbar source and cursor destination, check that inventory-open targets the player itself.
- `ContainerCreatedOutput` source must use the current negative request ID; an empty destination must use stack network ID `0`. `-1` is not a generic prediction placeholder.
- Replacing auto-craft with normal craft silently fails because the server expects pre-filled grid input.
- Using AutoCraftRecipe for a table recipe at all: its `Ingredients` field is the risky part, and the failure is a disconnect, not a rejection.
- `InteractActionOpenInventory` during a table craft closes the workbench the server just opened.
- Sending `Take`, `Place`, and `CraftRecipe` without awaiting each response uses stale stack IDs.
- A rejected final craft leaves staged ingredients in the grid unless they are explicitly returned. Do not issue recovery after a timeout because the delayed original response may still succeed.
- Assuming a workbench is open because a click went out. Wait for `ContainerOpen`.
- Bidirectional substring matching can let `oak_log` satisfy `dark_oak_log`; generic matching must require a word-boundary suffix.
- Some accepted transfer responses do not leave `InventoryMap` with the final ingredient count. Do not blindly decrement on every response: reconcile once after final acceptance, and make the operation idempotent so authoritative lower counts are never increased.
- Do not edit `data/bot_state.json` when runtime position changes are unrelated to the fix.

## Verification

- Focused tests assert the standalone transfer actions, 1x1/1x2 grid slots, final manual craft action order, current request ID, empty destination ID `0`, and global `ContainerInventory` response slots.
- `internal/bot/inventory_table_craft_test.go` pins the 3×3 grid mapping and the
  consume-per-staged-slot action list; `internal/bot/inventory/crafting/table_test.go`
  pins arm → click → confirm → close-by-assigned-ID.
- `go test ./internal/bot ./internal/bot/action ./internal/bot/network/player ./internal/bot/inventory/...` passes.
- `go test ./...` passes.
- Re-run the clean BDS harness with one oak log and verify exactly four sticks are removed using a limit greater than four; a clear limit of four cannot prove there were no extra sticks.
- Verify the server has exactly two planks left and the bot's local inventory summary reports the same count after crafting four sticks.
- Run at least three clean cycles. Each must accept oak planks before sticks with no rejection, timeout, desync, disconnect, or crash.

## Geyser: "resep tidak diketahui" for a recipe the server advertised

`CraftingData` is a delta: `ClearRecipes=false` means ADD to the book. The handler
used to wipe all tables on every packet, so a short re-send (Geyser after a backend
switch/transfer) erased the whole book. `handleCraftingData` now only resets on
`ClearRecipes` (or first packet) and logs `crafting data received` with counts. If it
still happens, read the `craft: no recipe known for item` warn: `known_recipes=0`
means the book was wiped/never sent; `similar=[...]` lists names with the same suffix.

## Geyser: dropping items (give/drop) times out with no response

A direct `DropStackRequestAction` from a hotbar/inventory slot got no ItemStackResponse
on Geyser (5s timeout, no item in the world). On `GeyserCompat`, `Bot.DropItem` now does
take-to-cursor then `BuildDropCursorAction` (drop from cursor), returning the cursor on
failure. BDS keeps the direct slot drop.

## Geyser: ItemStackRequests need OpenInventory first (live log 12:30-12:33)

Geyser answers ItemStackRequests only after `Interact{OpenInventory}` for the personal
inventory. CraftItem did this (all requests answered); DropItem did not (take-to-cursor
unanswered -> 5s timeout, no "item stack response received"). `openPersonalInventory()`
now runs before CraftItem, `dropViaCursor` and the Geyser equip. Signature of the bug:
`CraftItem awaiting response` with no matching `item stack response received`.

## Personal inventory must be closed (Geyser)

`openPersonalInventory()` (OpenInventory interact) is now paired with `closePersonalInventory()` (ContainerClose window 0, type 255) via defer in CraftItem, dropViaCursor and equipViaStackRequest. Suspected (unconfirmed live) cause of "server did not open crafting table" after earlier inventory ops. `interactBlock` logs `block click sent` (block, face, hotbar slot, held item, rid) for diagnosing unanswered clicks.

## Silence after placing the crafting table (Geyser, unresolved)

Signature: `placing block ... crafting_table`, then `net heartbeat last_packet_age_ms` grows
(1s,2s,3s...), `server did not confirm block placement`. Cause NOT confirmed: candidates are
(a) legacy standalone InventoryTransaction ClickBlock + PlayerAction StartItemUseOn from
`BuildPlacementPackets` not accepted by Geyser, (b) the ContainerClose/MobEquipment before it,
(c) a bot-side deadlock (heartbeat age measures bot reads). `startHeartbeat` now writes
`logs/stall_<HHMMSS>.txt` (all goroutine stacks) once when silence >= 3s: if the packet loop
is in ReadPacket the host went quiet; if it is on a mutex the bot deadlocked. Always ask for
the lines after the heartbeats (`connection lost after a long silence` / `disconnected by server`).
