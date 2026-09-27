---
name: gophertunnel-placement-debug
description: Debug Bedrock block placement and stale held-item rendering in this Go bot when writes succeed but BDS does not place the block, inventory counts drift, or other clients still see the consumed item.
---

# Gophertunnel Placement Debug

## When to Use

Use when a placement action logs success without a visible server-side block, BDS consumes an item but the bot never confirms placement, the held item remains visible after placement, or a remaining stack displays the wrong count.

## When NOT to Use

Do not use for crafting recipe rejection, pathfinding target selection, generic item drops, or block placement failures caused only by missing inventory items.

## Procedure

1. Reproduce against an isolated BDS port. Keep production BDS processes untouched.
2. Verify the LLM emitted `place:<item>` rather than `drop:<item>`. Every registered action must be documented in `internal/ai/prompts.go`.
3. Build a placement request from a loaded air destination and an adjacent solid support block. Keep item selection generic.
4. Read the support block's network ID from `WorldCache`. When `UseBlockNetworkIDHashes` is enabled, send the BDS wire/hash ID rather than the local Dragonfly runtime ID.
5. Equip the source slot and include its authoritative `StackNetworkID` in `UseItemTransactionData.HeldItem`.
6. Send the vanilla placement sequence:
   - `PlayerActionStartItemUseOn`
   - `InventoryTransaction` with `UseItemActionClickBlock`
   - `PlayerActionStopItemUseOn`
7. Set `TriggerTypePlayerInput` and `ClientPredictionSuccess`. Prediction value zero means failure.
8. Subscribe to the destination before sending the transaction. Report success only after an authoritative `UpdateBlock` changes it to a non-air block.
9. Do not mutate the local world optimistically and do not treat `WritePacket(nil)` as server acceptance.
10. Let `InventoryContent`, `InventorySlot`, normal `InventoryTransaction`, or accepted `ItemStackResponse` update `InventoryMap` and `StackNetworkIDs`.
11. Snapshot the held slot before and after each authoritative inventory mutation. Compare rendered identity and count fields, then send `MobEquipment` only when the held state actually changed.
12. For an empty held slot, send an empty `ItemInstance`. For a remaining stack, preserve the current item identity, count, and stack network ID.
13. Never decrement placement inventory locally when the tested BDS already sends an authoritative inventory update.
14. Use a second client probe to map Luna's runtime ID from `AddPlayer` and record her `MobEquipment` packets.

## Common Pitfalls

- Local runtime IDs are not valid wire IDs on hash-ID BDS sessions.
- Omitting `StackNetworkID` produces invalid or silently rejected interactions.
- `ClientPrediction=0` reports failure, not success.
- A successful packet write says nothing about server acceptance.
- Optimistic world updates produce false-positive placement logs.
- Blindly clearing the hand after placement breaks stacks with more than one item.
- Refreshing equipment whenever a packet merely touches the held slot creates redundant packet loops; compare before and after state.
- An LLM may choose `drop` if the registered `place` action is absent from the system prompt.
- Persistent E2E worlds must reset the platform, air space, position, and inventory between runs.
- Do not accept an initial empty-hand event as proof. Require an observed non-empty state followed by the expected transition.
- Picking a drop/equip slot from raw `InventoryMap` iteration is non-deterministic: with several matching stacks the action can hit a slot nobody is watching while the held item stays rendered (ghost item). Prefer the held slot, then the lowest matching slot (`dropTargetSlotLocked`).
- A client cannot hold "nothing": an unequip that fakes `MobEquipment{HotBarSlot: 0, NewItem: empty}` without updating `b.HeldSlot` desyncs the server-side selection from local state, and the next held-equipment echo flips the hand back to the old slot's item. Always switch to a genuinely empty hotbar slot and update `b.HeldSlot` (`emptyHotbarSlotLocked`).

## Verification

1. Run focused and race tests:

   ```sh
   go test ./internal/ai ./internal/bot ./internal/bot/network/player
   go test -race ./internal/ai ./internal/bot ./internal/bot/network/player
   ```

2. Run the full suite and build:

   ```sh
   go test ./...
   go build -o cmd/bot/bot.exe ./cmd/bot
   git diff --check
   ```

3. Build the isolated bot and probe, then run both E2E cases:

   ```sh
   go build -o .runtime-bds/luna-e2e.exe ./cmd/bot
   go build -o .runtime-bds/probe-e2e.exe ./cmd/runtimeprobe
   powershell.exe -NoProfile -ExecutionPolicy Bypass -File .runtime-bds/run-placement.ps1
   powershell.exe -NoProfile -ExecutionPolicy Bypass -File .runtime-bds/run-placement.ps1 -ItemCount 2
   ```

4. Require all three confirmations in each run:
   - BDS `testforblock` finds the placed block.
   - BDS inventory contains the exact expected remaining count.
   - The probe observes `1 -> 0` or `2 -> 1` through Luna's `MobEquipment` packets.
