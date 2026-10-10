---
name: gophertunnel-geyser-drop-normal-transaction
description: Use when this Go Bedrock bot cannot drop items on a Geyser-fronted server — the bot reports a drop but nothing appears in the world, or the drop "succeeded" while the inventory count never changed. Covers why PlayerAction DropItem and DropStackRequestAction both fail on Geyser and the only wire shape that works.
---

# Geyser Drop = NORMAL 2-Action InventoryTransaction

## When to Use

Use when drops do not appear in the world on a server with `is_geyser_server: true`
(or any host where `GeyserCompat` is set), when the bot logs
`dropped via held slot (geyser)` but the item stays in the inventory, or when you
are about to touch `internal/bot/inventory_actions.go` `DropItem` /
`dropViaHeldSlot` or `internal/bot/drop_transaction.go`.

## When NOT to Use

Do not use for the non-Geyser `DropStackRequestAction` path (kept for other
hosts), for `give` (chest-based handoff, different machinery), or for a drop that
DOES spawn but lands in the wrong spot (that is aiming — see
`memory/bedrock-drop-direction.md`).

## The three shapes and why only one works

A real Bedrock client pressing Q sends an `InventoryTransaction` with
`NormalTransactionData` and exactly TWO `InventoryAction`s: a container action out
of the held hotbar slot, and a world action carrying the `DROP_ITEM` source flag.
Geyser's `BedrockInventoryTransactionTranslator` handles exactly this shape.

Everything else is silently ignored by Geyser's translators:

1. **`packet.PlayerAction{ActionType: PlayerActionDropItem}`** — the legacy Q
   packet. Geyser's `BedrockPlayerActionTranslator` handles only RESPAWN,
   STOP_SLEEP and DIMENSION_CHANGE_SUCCESS. The packet is read and discarded.
   This was the live bug: the bot sent one per item and the item never moved.
2. **`DropStackRequestAction` in an ItemStackRequest with the inventory closed** —
   that action is the inventory-SCREEN drop; with no screen open the host never
   processes it (the request sat unanswered until timeout).
3. **Take-to-cursor then drop-from-cursor** — Geyser answers both requests OK but
   spawns no item.

## Wire shape (verified against Geyser master, 2026-10)

```go
protocol.InventoryTransaction{
    TransactionData: &protocol.NormalTransactionData{},
    Actions: []protocol.InventoryAction{
        { // container hand-off
            SourceType:    protocol.InventoryActionSourceContainer,
            WindowID:      protocol.Option(int8(protocol.WindowIDInventory)),
            InventorySlot: heldSlot, // MUST be 0-8; Geyser maps it to slot+36 on Java
            OldItem:       full stack, NewItem: remaining (empty instance when 0),
        },
        { // world drop
            SourceType:  protocol.InventoryActionSourceWorld,
            SourceFlags: protocol.Option(uint32(1)), // InventorySource.Flag.DROP_ITEM
            OldItem:     protocol.ItemInstance{},
            NewItem:     the dropped stack — COUNT IS THE SEMANTICS (below),
        },
    },
}
```

Geyser's count rule: world action `Count == 1` → Java `DROP_ITEM` (drop one);
`Count > 1` → Java `DROP_ITEM_STACK` (drop the WHOLE stack). A partial toss of N
out of a bigger stack therefore travels as N packets with count 1, ~120ms apart;
a whole-stack toss is ONE packet with count = stack size.

## Procedure

1. Select the stack into the hand first with `b.EquipItem(slot)` — it is
   server-confirmed on Geyser (ItemStackRequest for slots ≥ 9, MobEquipment for
   the hotbar). The container action's slot must match Geyser's held slot or
   Geyser falls into its click-workaround path.
2. Refuse to continue if `b.HeldSlot >= 9` after equipping.
3. Send the packet(s) per the count rule, re-reading `InventoryMap[held]` before
   each packet (the server decrements and re-issues stack network IDs).
4. Pair each packet with `BuildDropSwing` (Animate, SwingSource DropItem) so
   viewers see the throw.
5. **Verify against the authoritative inventory**: wait up to 2.5s for the held
   slot's count to fall by the requested amount. Geyser sends the slot/content
   resync after the Java server confirms; a silent host must surface as an error,
   not a success report. `dropVerifyTimeout` in `inventory_actions.go`.

## Common Pitfalls

- **The zero-stack-ID trap.** Geyser's slot syncs can carry `StackNetworkID = 0`
  (the live nexusone run: `inventory slot synced ... network_id=17` is the ITEM
  id; the stack ID was zero, so the tracker DELETED the entry). Any builder that
  rejects a zero stack network ID must never run before the Geyser branch — the
  old order called `BuildDropStackAction` first and every Geyser drop/give died
  with "authoritative stack network ID is missing" before one packet was sent.
  The give path then swallowed the error, so the model improvised
  "deketan sini dong" as the reason. Log the error at the give call site.
- Do NOT mutate `InventoryMap` locally after sending — the authoritative
  resync drives it; local guesses desync the held-item visual (ghost items).
- `MobEquipment` with `HotBarSlot >= 9` is invalid on the Java side; never use it
  to "select" a main-inventory slot — use `EquipItem`.
- The top-of-translate `switchHeldSlot(packet.getHotbarSlot())` reads 0 for
  NORMAL transactions (the wire has no hotbar field outside ITEM_USE); Geyser
  re-syncs the hand from the next ITEM_USE transaction, so this self-heals — do
  not "fix" it by stuffing fields into NORMAL data.
- A `LegacyRequestID` is unnecessary on Geyser (it ignores it). Leave it 0.
- If the drop lands but is instantly re-collected by the bot, that is aiming and
  standoff distance (`AimAtPlayerForDrop`), not the transaction.

## Verification

```sh
go build -buildvcs=false ./...
go test -count=1 ./tests/bot/ -run "TestBuildDrop"
```

`TestBuildDropTransactionIsTheNormalTwoActionShape`,
`TestBuildDropTransactionPartialDropIsOneItemPerPacket` and
`TestBuildDropTransactionWholeStackLeavesAnEmptySlot` pin the wire shape.
Live check: stand next to the player, say `drop 1 oak_log` — the item must
appear at the player's feet AND the inventory count must fall by one; the log
line is `dropped via held slot transaction`.
