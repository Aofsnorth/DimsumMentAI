---
name: bedrock-drop-direction
description: How Bedrock computes thrown-item direction/velocity, and the drop-aiming fix for the bot
metadata:
  type: reference
---

Bedrock item-drop mechanics (verified via gophertunnel structs + PocketMine reference):

- The `InventoryTransaction` drop action carries **no direction**. The server computes the
  thrown item's velocity entirely from the **Yaw + Pitch in the bot's last `PlayerAuthInput`**
  (the look/camera rotation — NOT HeadYaw, NOT InteractYaw).
- Velocity = `normalize(lookDir) * ~0.4`, spawned ~1.3 blocks above feet. Direction vector:
  `x = -cos(pitch)*sin(yaw)`, `y = -sin(pitch)`, `z = cos(pitch)*cos(yaw)` (yaw 0 = +Z south).
- Because it's normalized, a level throw travels **~3-4 blocks horizontally** — items go FAR,
  not "a fraction of a block." The old comments claiming "~0.3 m/s, can't travel far" were wrong
  and caused an upward `-28` pitch that overshot the player (item sailed over their head, landed
  behind = the "kejauhan/kebelakang" bug).

Fix applied (drop/give): the bot stands ~1.3 blocks from the player and aims **downward**
(`SetLookAngles(yaw, +30)` — positive pitch = down in this codebase) so the toss drops short at
the player's feet inside pickup range.

**Root cause of the random back/left/right scatter (the hard one):** the old flow computed the aim
yaw ONCE, pinned it, then slept 0.5-1.5s (WaitForYawSync + fixed sleeps) before dropping. A player
moving during that window — especially circling the bot while testing — drifted out from under the
throw, so the item flew toward where they *were*. Random direction = stale yaw, not a pitch/distance
issue. Fixed with `Bot.AimAtPlayerForDrop(target, pitch)` in `internal/bot/query.go`: a loop that
re-reads the player's LIVE position each iteration, turns to face it, waits for the yaw to reach the
server, then re-checks the player moved <0.5 block before committing. Callers MUST call `DropItem`
IMMEDIATELY after it returns — any sleep reintroduces the staleness. Interface method added to both
`internal/bot/inventory/manager.go` and `internal/bot/inventory/chest/chest.go` Bot interfaces.

Code: `internal/bot/action/execute_handlers.go` `handleDrop`, and
`internal/bot/inventory/chest/actions.go` `GiveItem` both call `AimAtPlayerForDrop`. Yaw formula
`atan2(dz,dx)-90` is correct. `SetLookAngles` + the `EaseAngle`/`EasePitch` `mag<0.05` guard means
forced angles are preserved (drift <0.5°/tick), so synced angles do reach the server intact.
Related: [[venity-compat-work]].
