---
name: gophertunnel-interact-click-debug
description: Debug Bedrock block clicks (buttons, doors, levers, signs) in this Go bot when the arm swings and packets are written but the host never activates the block, or when activation works but the bot claims failure.
---

# Gophertunnel Interact Click Debug

## When to Use

Use when "klik stone button / pintu / lever" logs `interacting` and the swing animation shows on other clients, but the block never activates; when a door toggles twice per click; or when the bot reports success for a click that visibly did nothing.

## When NOT to Use

Do not use for block placement (see `gophertunnel-placement-debug`), crafting rejection, entity/NPC clicks (`Interact` packets are a different path), or targets the bot never resolved (`interact target not found` is a perception problem, not a click problem).

## Procedure

1. Confirm the target resolved from `WorldCache` (name, `BlockPos`, face). If `GetBlockName` fails, the cache lacks the sub-chunk — fix terrain before blaming the click.
2. Wait for aim convergence before clicking: poll `bot.GetLastSentAim()` (the yaw/pitch of the last sent `PlayerAuthInput`) against `aimAngles(eye, target)` with ~12° yaw / ~10° pitch tolerance. `movement.LookAt` snaps (does not ease) `Yaw`/`Pitch`, so one heartbeat (~50ms) normally suffices; timeout 600ms, then click anyway.
3. Build the `UseItemTransactionData` with vanilla field shapes:
   - `Position` = eye = `GetCoords().Add(0, 1.62, 0)` — must match the `PlayerAuthInput` position, or the host sees two different player positions.
   - `BlockRuntimeID` = `bot.GetBlockNetworkID(...)` — the WIRE ID (numeric, or the FNV network hash when `UseBlockNetworkIDHashes=true`). Zero reads as "clicked an unknown block" and gets silently dropped on 1.21.100+ hosts.
   - `ClientPrediction` stays 0; never wrap the click in `PlayerActionStartItemUseOn`/`StopItemUseOn` — that is the hold-to-use shape, and it reads as a predicted use that gets rolled back.
   - `HeldItem` = `{Stack: item}` only; never put a type NetworkID into `StackNetworkID`.
4. Send swing (`AnimateSwingSourceInteract`, NOT the mine-swing source) + standalone `InventoryTransaction`.
5. Verify activation by watching the block's network ID change: `GetBlockNetworkID` before vs after (the host broadcasts `UpdateBlock`/`UpdateSubChunkBlocks`, which `internal/bot/network/world` applies to the cache). Poll ~40ms for ~400ms (LAN).
6. If the state did not change, retry the SAME transaction through the inline path: `bot.QueueItemInteractionData(tx)` — the movement loop embeds it in the next `PlayerAuthInput` with `InputFlagPerformItemInteraction`, which is what a stock touch client actually sends since 1.21.90.
7. Gate the fallback with `activationChangesState(name)` (button/lever/door/trapdoor/fence_gate, except iron): state-changers retry only when no state change was observed, so doors never toggle twice. UI blocks (chest, sign, crafting table) always queue the fallback — nothing is observable there and a double open is harmless.
8. Report failure honestly when a state-changer never reacts: `"<name> tidak merespons klik"`. Never report success for a button the player watched stay silent.

## Common Pitfalls

- Feet position in the transaction while `PlayerAuthInput` carries eye position — a 1.62-block disagreement between the two views of the player.
- Forgetting `BlockRuntimeID` on hash-ID hosts: it must be the hash from `WorldCache.GetBlockNetworkID`, not the dragonfly local RID, and not zero.
- Double-firing both transports unconditionally: a lever/door toggles twice and appears unchanged.
- Claiming success when nothing was verifiable: state-changers must fail loudly; UI blocks cannot be verified and may honestly report success.
- A cancelled context must abort before the fallback, not after sending it.
- Test timing: the state-change wait uses real sleeps (~400ms per attempt); fakes should flip the block ID synchronously in `WritePacket` to keep tests fast.

## Verification

1. `go build -buildvcs=false ./...`
2. `go test ./internal/bot/interact/` — pins: swing+transaction sequence, `BlockRuntimeID` echoed, eye position, inline fallback queued exactly once when ignored, honest failure report.
3. `go test ./internal/bot/movement/` — pins the `QueueItemInteractionData` → `PlayerAuthInput` embedding with `PerformItemInteraction`.
4. Live check on the LAN host: ask the bot to click a stone button twice; the click should visibly press it (or the inline fallback fires ~0.5s later), and the reply must not claim success for a silent button.
