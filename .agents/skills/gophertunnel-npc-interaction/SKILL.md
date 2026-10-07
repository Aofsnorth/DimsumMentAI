---
name: gophertunnel-npc-interaction
description: Debug Bedrock NPC / Armor Stand interaction, server portals/joiners with floating holographic text, and combat disengagement cancellation in this Go bot.
---

# Gophertunnel NPC & Armor Stand Interaction

## When to Use

Use when:
- The bot is commanded to click/interact with an NPC or Armor Stand (e.g. server join portal or Citizens NPC) but nothing happens or the bot targets background blocks (`hanging_sign`, `buttons`).
- Multiple entities or holographic floating text stands sit stacked vertically at the same location, causing clicks to hit the invisible text lines instead of the physical NPC.
- The server (especially Geyser/Paper) requires a `UseItemOnEntity` transaction instead of Bedrock-only `NPCOpen` / `Interact` packets.
- The bot cannot be stopped from an attack / combat loop ("berhenti attack", "stop attack", "jangan serang").

## When NOT to Use

- Do not use for block clicks like doors, buttons, or levers (use `gophertunnel-interact-click-debug`).
- Do not use for block placement (use `gophertunnel-placement-debug`).
- Do not use for chest inventory movements (use `gophertunnel-chest-sign-natural`).

## Procedure

### 1. Distinguishing Physical NPCs from Floating Holographic Text Lines
In Paper/Spigot hubs with holographic displays:
- Holographic lines are invisible Armor Stands stacked vertically above the physical NPC (at higher Y, e.g. Y + 1.5 ~ Y + 2.5).
- The physical clickable NPC / Armor Stand sits at the base (lowest Y on the ground).
- When clustering candidate entities by horizontal proximity (< 1.0m), ALWAYS select the lowest-Y candidate.
- Always set the aim point to torso/waist height (`Position.Y + 0.9`), never at eye height or above, so the look vector and interaction packets hit the physical hitbox.

### 2. Dual Packet Transport for Entity Interaction
Bedrock and Geyser/Paper servers handle entity clicks differently:
1. `packet.Interact` with:
   - `ActionType: packet.InteractActionMouseOverEntity`
   - `TargetEntityRuntimeID`: target entity runtime ID
   - `Position`: `protocol.Option(mgl32.Vec3{0, 0.9, 0})` (relative to entity base coordinate).
2. Arm swing animation:
   - `animation.InteractSwing(bot.GetEntityRuntimeID())`.
3. Explicit NPC Open:
   - `packet.Interact` with `ActionType: packet.InteractActionNPCOpen`.
4. Standalone `InventoryTransaction`:
   - `UseItemOnEntityTransactionData` with `ActionType: protocol.UseItemOnEntityActionInteract`.
   - `ClickedPosition: mgl32.Vec3{0, 0.9, 0}` (relative to entity base).
   - `Position: eye position (feet + 1.62)`.
   - `HotBarSlot: held slot`.
5. Optional dialogue request:
   - If NPC type, send `packet.NPCRequest` (`NPCRequestActionExecuteAction`).

### 3. Server Transfer & Dimension Teleport Handling
- On server transfer (`packet.Transfer`): extract `Address` and `Port`, call `b.RequestJoin(addr)`.
- On dimension change (`packet.ChangeDimension`): update `ChunkDimension`, `Dimension`, `Pos`, reset `Actors`, and send `PlayerAction` with `PlayerActionDimensionChangeDone`.
- On respawn (`packet.Respawn`): if `p.State == packet.RespawnStateReadyToSpawn`, respond with `packet.Respawn` (`RespawnStateClientReadyToSpawn`).

### 4. Reliable Combat Disengagement
- When stopping ("stop", "stay", "disengage", "berhenti attack", "stop attack", "jangan serang"), ALWAYS call `b.CombatMgr.Disengage()`.
- Lock ordering pitfall: `cm.Disengage()` locks `cm.mu` and calls `b.StopMovement()` (which acquires `b.Mu`). Never call `cm.Disengage()` while holding `b.Mu`, or deadlock will occur.

## Verification

1. `go test ./tests/bot/interact/...` — pins hologram stacking resolution, torso aim point, and UseItemOnEntity packet emission.
2. `go test ./tests/bot/action/...` — pins combat disengagement via actions and parameters.
3. `go test ./tests/bot/chat/...` — pins natural chat intents ("berhenti attack", "stop attack", "jangan serang").
4. `go test ./tests/bot/network/player/...` — pins Transfer, ChangeDimension, and Respawn packet handlers.
