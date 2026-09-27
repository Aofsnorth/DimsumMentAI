---
name: gophertunnel-chest-sign-natural
description: Build or debug chest open/store/take and sign reading in this Go Bedrock bot when a chest will not open, contents come back wrong or empty, sign text is unavailable, or the bot needs to open chests one at a time in a labelled storage room.
---

# Gophertunnel Chest & Sign (Natural Storage)

## When to Use

Use when the bot should open a chest, put items away, take items out, search several chests, or read a sign. Also when a chest "opens" but reports fake/empty contents, when `ContainerClose` is sent with the wrong window ID, or when sign text is needed but never arrives.

## When NOT to Use

Do not use for block placement (`gophertunnel-placement-debug`), for doors/buttons/levers (those are `interact` clicks, not storage), or for a chest that will not open at all — that is a click problem, see `gophertunnel-interact-click-debug` first.

## The Three Rules That Make It Look Human

1. **Never open a chest the bot cannot see.** Check line of sight from the eyes to the chest centre, walking the ray through the *world model*, not just a distance check. An unknown cell must NOT count as an occluder (the bot has no idea what is in it), but a *known solid* cell must. Skip the chest entirely when blocked.
2. **Walk, stop, look, wait, then click.** `LookAt` + ~120ms before the click. A click sent without the crosshair on the chest is ignored by some hosts and looks wrong to viewers on all of them.
3. **Open one chest at a time, signage first.** Read signs, open the chest the label points at, only then fall through to the rest. Never "scan several at once" — a player can only have one open.

## Procedure

1. **Discover**: walk a box around the bot, `GetBlockName` → `IsContainerBlock` (vanilla list + `_chest`/`shulker_box`/`barrel`/`_storage` suffixes for modded storage). Reject any with no line of sight.
2. **Approach**: target a cell *adjacent* to the chest, not the chest cell (players cannot stand on a chest). Try the 8 neighbours in a fixed order starting on the bot's side; a random order makes the walk look arbitrary and a wrong order walks into a neighbouring chest.
3. **Arm before clicking**: `BeginContainerWatch()` MUST be called *before* the click. The server's `ContainerOpen` can arrive within one frame of the click; a watch armed afterwards misses the window entirely.
4. **Click via the interactor**, never by hand: `b.Interactor.ClickBlockAt(ctx, pos)`. It already owns aim convergence, the arm swing, the wire `BlockRuntimeID`, and the inline `PlayerAuthInput` retry. Re-deriving the packet means a chest opens on a different set of servers than a door does.
5. **Read the window**: `WaitContainerOpen` → `WaitContainerContent` → keep the map as `map[uint32]protocol.ItemInstance`, **not** `ItemStack`.
6. **Transfer** with `TakeStackRequestAction` / `PlaceStackRequestAction` and the **authoritative** stack IDs. Close with `CloseContainerWindow` (a real client always closes what it opens).
7. **Signage**: parse block entities, then `ReadSign` (approach, `LookAt`, ~350ms glance, read text), `LabelChests` (sign within ~3.5 blocks and in the same sightline), `OrderByLabelHint` (reorder only — **never filter**, a label is a hint not a verdict).

## Common Pitfalls

- **`ItemStack` vs `ItemInstance`**: the transfer actions need BOTH `Stack.NetworkID` (item type, for the name lookup) and `StackNetworkID` (this stack instance, for the server to validate the move). Storing only the `ItemStack` and passing `Stack.NetworkID` in the `StackNetworkID` field is the single most likely cause of "the server rejected the item stack request" on a take/store.
- **Sign text lives in block entities**, appended to the chunk payload *after* the block data. `chunk.NetworkDecode` throws the remainder away — use `chunk.NetworkDecodeBuffer` and scan the leftover bytes with `nbt.NewDecoderWithEncoding(..., nbt.LittleEndian)`. Shapes: modern `FrontText`/`BackText` compounds, legacy flat `Text`, oldest `TextObject1..4`.
- **`§` is two UTF-8 bytes (C2 A7) plus a format code.** Skipping only the two sign bytes leaves `"§lBAHAN"` as `"lBAHAN"` and makes the label unmatchable. Skip all three.
- **LOS between a sign and its chest is blocked by the sign's own cell.** Exclude BOTH endpoints from the occlusion walk.
- **`ContainerClose{WindowID: 0}`** is wrong for storage — use the window ID the server assigned in `ContainerOpen`. Window 0 is the player inventory.
- **Chest slot count is 27** for a single chest. Reading past that indexes a region whose numbering depends on the host and invents items.
- **A chest whose contents never arrive is not an empty chest.** Distinguish "server sent zero items" from "nothing came", and remember the latter so a multi-chest search does not re-open the same silent window in a loop.
- **Never claim success you cannot verify.** A chest across a gap or up a wall cannot be opened from where the bot stands; report that rather than storing nothing and saying it worked.

## Verification

1. `go build -buildvcs=false ./...`
2. `go test ./internal/bot/storage/...` — pins: chest behind a wall is not a candidate, visible chests sort nearest-first, approach avoids the chest cell, empty sign text is not offered, label attaches to the right chest, and `OrderByLabelHint` never drops a chest.
3. `go test ./internal/bot/world/...` — pins: modern/legacy sign shapes decode, non-sign block entities are ignored, a garbage tail does not discard signs already read.
4. Live: put a labelled double chest in a room, ask the bot to take an item. It should read the sign, walk to the labelled chest, open one, take the item, and report honestly when the item is not there.
