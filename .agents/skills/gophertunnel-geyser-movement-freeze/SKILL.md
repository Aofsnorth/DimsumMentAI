---
name: gophertunnel-geyser-movement-freeze
description: Debug a bot frozen in place with running particles on Geyser/Bedrock 1.21+ servers, especially when A* generates fall paths and ground solidity checks fail.
---

# Geyser Movement Freeze & Palette Gap

## When to Use

Use when the bot connects to a Geyser bridge or modern Bedrock server without FNV hashes (`UseBlockNetworkIDHashes = false`), shows running particles (sprint/move input flags active), repeatedly recalculates A* paths containing `fall` links, but stays motionless at its spawn coordinates with `WARN msg="bot wants to move and is not" grounded=false foot_solid=false cache_solid=false cache_loaded=true`.

## When NOT to Use

- When the bot is on BDS (`UseBlockNetworkIDHashes = true`) where FNV-1a hashes map to Dragonfly palette correctly.
- When the bot is disconnected or anticheat rubberbands with high snap distance.
- When the bot moves but head bobs vertically (use `gophertunnel-vertical-anchor`).

## Root Cause

1. **Palette Gap**: Dragonfly v0.10.13 only knows block states up to index ~16898 (Bedrock 1.20). Bedrock 1.21+ palette has 22,000+ states with shifted runtime IDs.
2. **Shifted Air, Plant & Floor RIDs**:
   - `minecraft:air` in Bedrock 1.21 is RID `17025`.
   - Passable plants like `minecraft:short_grass` (RID `16851`), flowers (`minecraft:dandelion` RID `22090`, poppies, ferns) shift into high RIDs.
   - Terrain/floor blocks have RIDs $\ge 16899$ (e.g. `20236`).
3. **Pitfall of Blindly Treating All Unknown RIDs as Solid**:
   - Blindly returning `true` for all unknown non-air RIDs causes passable plants (short grass, flowers) to become solid full blocks.
   - The bot steps on top of flowers and floats 1 block in mid-air (`Malah melayang gini botnya`).
   - The bot detects flowers ahead as stone walls in `checkWallCollision`, freezing mid-path.
4. **Accurate Resolution**:
   - Embed the Bedrock 1.21 canonical palette (`internal/bot/world/block_states_121.nbt.gz`) to map all 22,091 states to their exact names and passability.
   - Include non-solid variants in `IsBlockNamePassable`:
     - Bedrock flower aliases: `yellow_flower` (dandelion), `red_flower` (poppy/tulip/etc.), `double_plant` (sunflower, lilac, rose bush, peony, double tallgrass, large fern), `flower` substring.
     - Thin ground covers: `snow`, `snow_layer`, `carpet`, `candle`, `web`/`cobweb`, `frogspawn`, `sea_pickle`, `dripleaf`, `sprout`, `stem`.
     - Failure to classify these as passable causes `IsSolid` to vote true for the block at feet level, making `performAutoJump`, `ApplyGroundLanding`, and `RecalculatePath` bump the bot up to Y+1 and walk 1 block in mid-air over grass.
   - Disable `IdleNudge` on join by default so the bot does not wander randomly on spawn.

5. **False Teleport Acks & Stale Echo Overwrite on Ledges**:
   - Geyser relays 20Hz `MovePlayer` echoes with 100-200ms latency.
   - Stepping down 1 block ($\Delta X=1, \Delta Y=1$) has 3D distance $\sqrt{2} \approx 1.41$ blocks. If `teleportAckDistance` is 1.0, the delayed echo from the ledge top is misidentified as a server teleport.
   - A teleport ack sets `TeleportAckPending`, which drops `CurrentPath` and halts steering physics.
   - Overwriting `b.Pos` with stale echoes mid-stride yanks the bot backwards repeatedly ("aga lag lag").

6. **1.5-Block Obstacles (Fences & Walls)**:
   - Fences (`_fence`, `_fence_gate`) and walls (`_wall`) are 1.5 blocks tall.
   - Normal jumps (1.25 blocks) cannot clear them, and a player cannot step onto them at standard integer elevations.
   - If `canStandAt` or `stepJumpTargetClear` treats fences/walls as solid floor, A* attempts step-jumps onto them, causing the bot to walk into them repeatedly and stutter ("macet-macet").
   - Reject fences and walls in `canStandAt` and `stepJumpTargetClear` (`w.isFenceOrWall(x, y-1, z)`).

7. **Floating on Ledge Descent (Descent Flag Truncation)**:
   - When stepping down a 1-block ledge (e.g. from 66.0 to 65.0), the bot drops to 65.8.
   - If `updateDescendingFlag()` uses integer floor `nextNode.Y < FeetY` (`65 < 65`), `IsDescending` becomes `false` mid-stride.
   - Wide ground checks `[-0.3, 0.3]` probe behind the bot, touch the higher ledge (65, top 66.0), and `ApplyGroundLanding` snaps `NextY` right back up to 66.0, leaving the bot floating in mid-air.
   - `updateDescendingFlag()` must use float check: `float32(nextNode.Y) < tc.CurrPos.Y() - 0.05`.
   - Without an active A* path (`!tc.HasPath`), allow stepping down 1-2 blocks when lower ground support exists (`hasGroundSupportAt(targetX, targetZ, baseY - drop)`).

## Procedure

1. **Embed Bedrock 1.21 Palette** in `internal/bot/world/palette_121.go`:
   - Decode `block_states_121.nbt.gz` into `palette121Names`, `palette121Solids`, and `palette121Airs`.
   - Passable plants and air are identified with `IsBlockNamePassable(name)`.
2. **Resolve Solidity & Names via 1.21 Palette on Geyser / Non-Hash Servers**:
   - In `resolveRIDSolid(rid)`:
     ```go
     if wc.geyserCompat || !wc.useHashes {
         initPalette121()
         if int(rid) < len(palette121Solids) {
             return palette121Solids[rid]
         }
     }
     ```
   - In `BlockName(rid)`:
     ```go
     if wc.geyserCompat || !wc.useHashes {
         initPalette121()
         if int(rid) < len(palette121Names) {
             return palette121Names[rid], true
         }
     }
     ```
   - BDS mode (`wc.useHashes == true && !wc.geyserCompat`) continues using Dragonfly palette + network hashes.
3. **Disable Default Idle Wandering on Geyser**:
   - In `internal/servercompat/profile.go`: set `p.IdleNudge = false`.
4. **Anchor Support Fallback in Movement Physics**:
   - In `internal/bot/movement/speed_pos.go:hasGroundSupportAt`:
     If `(tc.B.VenityCompat || tc.B.GeyserCompat)` and the bot is within 0.5 blocks of `b.ServerGroundY`, treat the support cell under the step as solid if `!WorldCache.IsBlockAir(...)`.
5. **Propagate `GeyserCompat` to `WorldCache`**:
   - Call `b.WorldCache.SetGeyserCompat(b.GeyserCompat)` in `initSpawn`, `WithServerHost`, and `join.go:noteServer`.
6. **Gate `MovePlayer` Echoes & Increase `teleportAckDistance`**:
   - In `internal/bot/network/player/move.go`: set `teleportAckDistance = 2.2`.
   - In `ApplySelfMovePlayer`: only overwrite `b.Pos` if `ack == true` or `b.MovementState == "idle"`. Unflagged delayed echoes during active walking must not overwrite `b.Pos`.
7. **Filter Fences & Walls from Standable Paths**:
   - In `internal/bot/pathfinder/world_neighbors.go`: add `isFenceOrWall` checking suffixes `_wall`, `_fence`, `_fence_gate`.
   - In `canStandAt` and `stepJumpTargetClear`: return `false` if `y-1` is a fence or wall. A* routes around them rather than trying to hop over 1.5-block barriers.

### 8. Ledge Descent Floating & Premature Path Advancement
- **Premature path advance**: In `advancePath`, when `yDiff < -0.05` (descending), do NOT advance `PathIndex++` until `float64(tc.CurrPos.Y()) <= float64(nextNode.Y)+0.25`. Advancing while `Dist < 0.55` horizontally empties `CurrentPath` while the bot is still floating at `Y+1`.
- **Target cell support probe**: In `speed_pos.go:computeTargetPosition`, unconditionally clear `hasSameLevelSupport` when `!WorldModel.IsSolid(targetCellX, baseY-1, targetCellZ)`. If only checked when `plannedDescent` is already true, backward probe offsets in `hasGroundSupportAt` falsely maintain same-level support, skipping controlled descent.
- **Unpathed descending flag**: In `physics.go:updateDescendingFlag()`, check if the cell under feet is air and lower ground exists within 1–3 blocks; if so, set `IsDescending = true` so `groundCheckOffsets` uses `[0.0]` instead of wide `[-0.3, 0.3]` probes that snap `NextY` back up to the ledge behind.
- **Slabs and stairs standability**: In `pathfinder/world_neighbors.go:canStandAt`, do NOT reject `isHalfBlock(x, y-1, z)`. Slabs and stairs are $\le 1.0$-block walkable surfaces. Rejecting them causes A* to expand 0 neighbors on slab floors, freezing the bot when approaching players.
- **Floodgate dot prefix**: On Geyser servers, player names often appear with `.PlayerName`. In `playerNameMatches`, compare names with `strings.TrimPrefix(..., ".")` so commands like "ke tempatku" resolve player locations reliably.

### 9. Step-Jump Freeze From Residue Float (grounded=false, foot_solid=true)
Live signature: `bot wants to move and is not ... grounded=false foot_solid=true cache_solid=true` next to a `(x,y+1,z,step_jump)` node, repeating with `Multiple stuck detections`. The body hovers ~0.1–0.2 above the floor top (echo residue from a mid-stride server correction, or a step-down assist that stopped high) and the 2cm `groundSkin` in `physics.go:groundedCell` reads that as airborne. An airborne tick (a) clears `ShouldJump` in `ApplyVerticalVelocity`, so no hop ever fires, and (b) makes `checkWallCollision` treat the step block as a wall (`groundedStepUp` requires `!isMidJump`), so forward motion is cancelled — the bot freezes pressed against the ledge forever. Fix (implemented):
- `groundRestSkin = 0.15`: a SETTLED body (|VelY| small: not rising > 0.05, not falling < -0.25) gets a 15cm resting window in `groundedCell` via `groundSkinFor()`; rising/falling bodies keep the tight 2cm skin so ledges beside a jump arc still never vote as ground.
- Resting catch at the top of `ApplyGroundLanding`: if the centre probe reports a solid top face within the resting window, snap `NextY = top`, `VelY = 0`, `IsGrounded = true`. The residue is physically absorbed on the next tick instead of hovering forever.
- `updateDescendingFlag` probes below the resting skin (`feetY - groundRestSkin`) instead of 1cm under the feet, so a resting body no longer probes into its own air cell and reads as descending.
- Pitfall: do NOT widen the skin unconditionally — `TestGroundedIsNotTakenFromABlockBesideTheFeet` guards a wall at foot level 0.2–0.6 below a rising body, and an unconditional 15cm window re-creates the mid-air double-jump bug.
- Verification: `go test -v ./tests/bot/movement -run "TestResidueFloat|TestStepJumpFiresFromSettledFloor"` passes.

### 10. Second Trip Freezes On Inherited Stuck Penalty
Signature: first `come` works (after fix #9), a second `come` shortly after stands still. Stuck-recovery markers (`SetTempSolid`, 3–12s via `StuckPenaltyWindow`, plus `markCurrentPathTempSolidLocked` 5s) outlive the trip that earned them. If the new command arrives before expiry, A* refuses the route through the marked step-up cell and the bot never moves — no walk-blocked log either, because `logVenityWalkBlocked` had a second `!tc.B.VenityCompat` early-return that excluded Geyser. Fix (implemented):
- `LocalWorldModel.ClearTempSolid()` in `pathfinder/world.go`: clears all recovery markers; terrain overrides from mining/placing (`solidBlocks`) untouched.
- `ComeToPlayer` (internal/bot/action.go) calls `ClearTempSolid()` and resets `ConsecutiveStuckCount`/`TicksStuck` when a new command arrives — a fresh trip starts with a clean escalation ladder.
- `logVenityWalkBlocked` (speed_pos.go): removed the Venity-only second gate so Geyser walk-blocks reach the debug log (`log_level: debug` in configs/bot.yaml).
- Pitfall: do NOT clear markers inside `handleStuckRecalcLocked` or on every repath — mid-trip recovery needs them to force the next A* around a real blocker. Clear only at trip start.
- Verification: `go build ./... && go test ./tests/...` passes.

### 11. A* Dead End From Network Indices Read Against Dragonfly's Local Palette
Signature (Geyser survival, e.g. `hansprojects.my.id`): `A* pathfinding failed resolve walkable path destination`, then for every cardinal direction `feet_solid=false floor_solid=false`, and `bot wants to move and is not ... grounded=false`. The bot stands at spawn forever and answers "gagal nyampe, jalannya keblokir".

Root cause — **two different numeric spaces collided**:
- The SubChunk palette on a non-hash connection carries **server network palette indices** (palette-121 order, ~22k entries).
- `chunk.RuntimeIDToState` (dragonfly v0.10.13, 1.20 palette, ~17k entries) answers for **local dragonfly RIDs**.
- Feeding one to the other silently returns a *different real block*, never an error. Measured on this server:

| network idx | server palette says | dragonfly says |
|---|---|---|
| 13591 | `mangrove_propagule` | `granite_wall` |
| 15079 | `warped_hanging_sign` | `dead_tube_coral_wall_fan` |
| 17050 | `oxidized_copper_chest` | (unknown) |

A `mangrove_propagule` read as `granite_wall` becomes: a *solid floor* under a cell that is actually empty (`floor_solid=true` at y-1 → bot walks into a wall), and a *fence/wall* at foot level (`isFenceOrWall` vetoes `canStandAt` and `stepJumpTargetClear`). Together they zero out every neighbor and A* returns 0 nodes.

Three fixes, all in `internal/bot/world`:
- `BlockName` and `resolveRIDSolid` consult the **server's StartGame palette first** (`serverBlockName` → `customBlockNames`). It is the authority, including behavior-pack and proxy remaps the embedded vanilla palette cannot know.
- `SetCustomBlocks` derives `customBlockSolid` from `isBlockNamePassable(entry.Name)` only. It previously overrode that with the dragonfly collision model, which only knows vanilla 1.20 states and misclassified decoration as solid.
- An index no table recognizes is **passable**, not solid. The old "unknown ⇒ solid" is what walled off spawn: undecoded proxy indices exceed the embedded palette, and every unrecognised cell became a wall. Unknown is not evidence.

Pitfalls:
- Do not use `chunk.RuntimeIDToState` for diagnostics on a non-hash connection — its `name` in the A* RID dump is the *wrong block*, and the solidity printed next to it belongs to that wrong block too. Read the server palette instead.
- Do not revert "unknown ⇒ solid" to "unknown ⇒ air" broadly. It is safe as a *solidity* answer; keep `IsLoaded`/`CanResolve` as the separate "do I know this cell" question, which is what guards against planning over unloaded terrain.
- Solid state from the collision model is still correct on **BDS** (`useHashes=true`), where network IDs are FNV-1a hashes that do map onto dragonfly's palette.

Verification: `go test ./tests/world -run "TestGeyserProxyPaletteWinsOverLocalDragonflyTables|TestUnknownNetworkIndexIsPassableOnGeyser"`, then `go test ./tests/...`.

### 11b. CORRECTION: the real server RID space is vanilla + custom blocks merged by name hash (verified live)
Section 11 is partly wrong: on a non-hash server the StartGame palette does **not** contain vanilla names, and the embedded palette-121 indices are **also** wrong. The client builds its RID space by sorting vanilla blocks plus StartGame custom blocks by FNV-1 64-bit name hash (unsigned). Every custom state shifts all vanilla states that sort after it.

Evidence (hansprojects, 105 custom blocks): real air 17224 vs embedded 17025 (+199), dirt 13591 vs 13456 (+135), grass_block 15079 vs 14944 (+135). Shifts equal the `geyser_custom:*` state totals (7 blocks, 199 states; 135 = 199 - 64 because `mushroom_stem` sorts after dirt/grass).

Trap: 98 of the 105 customs are `minecraft:*_slab/_stairs/_double_slab` overrides whose names already exist in the vanilla palette. They add **no** states. Counting them gave air 17299 (overcount) and the census still read garbage. `mergeServerPalette` (`internal/bot/world/server_palette.go`) skips custom entries whose name is in `palette121Names`.

How to detect: log `custom blocks detail` (Debug now; `*` = name exists in vanilla, `<` = hash sorts before air) and `rid census` (top RIDs must read air/dirt/grass_block/etc, not green_glazed_terracotta/twisting_vines). Expect merged total = 22091 + non-vanilla custom states.

Tests: `tests/world/cache_test.go` `TestGeyserMergedPaletteShiftsVanillaIndices`, `TestGeyserVanillaNamedCustomBlocksAddNoStates`. Passing unit tests alone proved nothing; only live RIDs did.

### 11c. Geyser host drops the connection when the bot echoes a full ItemStack (eating, equip)
Symptom: bot leaves right after an item packet. Either `disconnected by server reason="Invalid packet received!"` or silence (`connection lost after a long silence`). Verified live by a 3-shape probe (use-item with empty held item / bare stack / full stack, one per live session): empty and bare survived, **full stack was kicked with "Invalid packet received!"**. Server-side stacks carry NBT (`GeyserHash`, `display`) and `BlockRuntimeID`; echoing them back is rejected. Fix: `survival.WireItemStack` keeps only `ItemType`+`Count`; used by `sendUse` and by `EquipItem`'s MobEquipment. (Earlier theories "MobEquipment is the trigger" and "use-item is innocent" were both wrong: the payload is the culprit.) Whether the bare cookie stack actually makes the host eat is confirmed only by a live `consumed=true`.

Geyser handles inline `PERFORM_ITEM_INTERACTION` in PlayerAuthInput only for block break (actionType 2); other types log "Unhandled item use transaction type" and are ignored. Click-air use is honoured only as a standalone `InventoryTransaction` (UseItem, ClickAir, hotbar slot 0-8, real held stack) — see `BedrockPlayerAuthInputTranslator` and the `InventoryTransaction` ITEM_USE case 1. `BedrockMobEquipmentTranslator` only calls `session.switchHeldSlot()`, and rejects anything whose container id is not INVENTORY (0).

Recipe worth reusing: a 1 Hz heartbeat in `PacketLoop` (kept; logs `net heartbeat last_packet_age_ms/last_packet` only when the gap is > 1 s, since healthy play has LevelChunk/LevelEvent constantly), plus a probe that sends the suspect packet in progressively richer shapes, one per live session, logging the full payload. Don't press Ctrl+C: a silent death takes ~30 s to detect. ClickBlock with face 255 is also invalid for Geyser (`Direction.getUntrusted`) — never send 255.

Open: the swap `InventoryTransaction` in `EquipItem` (slot >= 9) and other transactions still carry full stacks; apply `WireItemStack` there if a similar kick shows up.

### 12. Diagonal Corner-Cutting Into Gaps & Trapped in 1-Block Pit
Signature: bot begins walking an A* path (e.g. from spawn), walks ~5 seconds, then falls into a 1-block pit (e.g. at Y=70 surrounded by walkway blocks at Y=70). A* pathfinding thereafter fails every repath with `A* neighbor probe feet_solid=true for N/S/E/W` and `floor_solid=false` (hanging sign/non-solid under pit floor).
Root causes:
1. `appendDiagonalNeighbors` (`diagonalSidesClear`) only checked clearance at `cy` and `cy+1` (no walls), but never checked whether adjacent corner cells `(cx+dx, cy-1, cz)` and `(cx, cy-1, cz+dz)` had solid floors. Cutting diagonally across a corner where one adjacent cell is a gap/chasm caused the bot to walk off the edge into the pit.
2. `CanWalkDirectly` / `canWalkLine` only sampled a zero-width center point at `t=0.5`. Angled or diagonal links with missing corner floors smoothed directly across the hole without validating body bounding box corners.
3. `pathAllowsForwardWithoutGround` in `speed_pos.go` returned `true` for flat walk links as long as the far-off destination node had floor support. The bot walked horizontally off ledges into thin air instead of blocking the step.
4. Once in a 1-block pit with non-solid footing underneath (hanging sign, slab), `stepJumpStartClear` vetoed jumping up because `!IsSolid(cx, cy-1, cz)` at the start cell. The bot could not jump out of the hole onto adjacent blocks.
5. Temp solid markers placed by stuck recovery fenced the bot in.

Fixes:
- `diagonalSidesClear`: require both adjacent cells to have solid floor support (`adj1Floor && adj2Floor`).
- `canWalkLine`: sample bounding box corners `(sx ± 0.25, sz ± 0.25)` to prevent smoothing across corner holes.
- `pathAllowsForwardWithoutGround`: only allow for jump links (`LinkJump`, `LinkStepJump`) or gap jumps (`dist > 1`), never for normal flat walk links.
- `stepJumpStartClear` & `parkourStartClear`: permit jumping out from the bot's own starting cell (`cx == startX && cy == startY && cz == startZ`) even if the floor underneath reads non-solid.
- `LocalWorldModel.IsSolid`: start node footing `(startX, startY-1, startZ)` treated as solid to allow initial node expansion.
- Clear temp-solid penalties if all path passes fail.

Verification: `go test -v ./tests/bot/pathfinder -run "TestDiagonalMovement|TestSmoothingRejects|TestStepJumpOut"` passes.

## 13. Walls and fences as the floor (Geyser lobby built from wall tiles)

**Symptom.** `A* pathfinding failed resolve walkable path destination` with an
`A* neighbor probe` block where every direction shows
`feet_solid=false head_solid=false floor_solid=true`, followed by
`A* neighbor RID dump ... name=minecraft:granite_wall solid=true`. Also
`A* gave up short` partway across the same area. Zero neighbours from the start
node; A* dead-ends on a surface the bot is visibly standing on.

**Cause.** A wall or fence is 1.5 blocks tall, so its collision top sits half a
block above the top of its own cell. `canStandAt` vetoed any cell whose floor is
a wall or fence, on the correct reasoning that a bot cannot *climb* onto one.
But a lobby paved in `granite_wall` / `tuff_brick_wall` decodes its entire
walkway that way, so the bot's own foothold read as unusable — and
`GetNeighbors` returned nothing.

**Two distinct heights, and the wrong comparison for each.** Where the bot's
feet actually rest is not the node level:

- Standing on ordinary ground, feet are at exactly the node level.
- Standing on a wall, feet are half a block above it.

Compare the neighbour wall's top against the bot's *own footing height plus the
0.6 step height*, not against the bare node level:

```go
func (w *LocalWorldModel) wallBlocksStanding(x, y, z int32) bool {
	if !w.isFenceOrWall(x, y-1, z) {
		return false
	}
	return float64(y)+wallTopOverCell > w.footingTopY()+maxStepUp
}
```

`footingTopY()` returns `startY + 0.5` when the bot's own floor is a wall or
fence, else `startY`, and `-Inf` when there are no trip bounds (conservative =
refuse the climb rather than invent a footing).

Without the step height the ordinary-ground-to-wall-surface transition is vetoed
too — and that transition is only half a block, which a player walks up without
jumping. The bot then refuses to walk through the front door of its own lobby.

`stepJumpTargetClear` and `canScaffoldTower` keep the plain `isFenceOrWall`
check: a step-jump gains ~1.25 blocks and must still not plan a hop onto a
1.5-block wall.

Tests: `tests/bot/pathfinder/wall_floor_test.go`. Build the world with a
`ChunkQuerier` double that implements `BlockName` — do **not** route a wall
floor through `worldWithFloors`/`SetSolid`, because the fence rules read block
*names* and a solidity-only double answers "not a wall" for everything.

## 14. A failed repath nulls the route and freezes the bot

**Symptom.** The bot stands still for a few hundred ms mid-walk, then resumes.
In the log, a search failed at 21:44:48 and succeeded again 200ms later from the
same tile.

**Cause.** `RecalculatePath`'s failure branch set `b.CurrentPath = nil`
unconditionally. Searches fail transiently and for reasons unrelated to whether
the route in hand is still walkable — a chunk not decoded yet, a start cell the
bot is halfway out of, a temp-solid marker left by the previous tick. Each
failure discarded a good route for the duration of the next search.

**Fix.** Keep a route the bot has already set off on; only discard one it has
not started (`PathIndex > 0 && PathIndex < len(CurrentPath)`). Leave
`PathIndex` alone so the bot resumes where it was.

## 15. `_Locked` helpers must not re-acquire `b.Mu` (hard freeze)

**Symptom.** The bot stops moving entirely. No error, no panic, no further log
lines from the movement tick. The stall watchdog starts dumping goroutines every
5 seconds, and every goroutine is in `[sync.Mutex.Lock]` on the same address:

```
goroutine 149 [sync.Mutex.Lock]:
bedrock-ai/internal/bot/movement.(*TickContext).handleStuckRecalcLocked
	internal/bot/movement/steering.go:554
bedrock-ai/internal/bot/movement.(*TickContext).handleStuck
	internal/bot/movement/steering.go:441
```

**Cause.** `handleStuck` takes `b.Mu` at line 441 and then calls
`handleStuckRecalcLocked`. The `...Locked` suffix in this package means *the caller
already holds `b.Mu`*. The function re-acquired it to read one field, and
`sync.Mutex` is not reentrant — so the movement tick blocked on a lock it was
holding itself, and every other goroutine (packet loop, threat scan, survival,
nav) queued behind it.

**The convention, and how the siblings honour it.** They release `b.Mu` around the
work they delegate, then re-acquire:

```go
func (tc *TickContext) recalculatePathLocked() {
	tc.B.Mu.Unlock()
	RecalculatePath(tc.B)
	tc.B.Mu.Lock()
}
```

Same in `markCurrentPathTempSolidLocked` and `breakPathObstacleLocked`. Read plain
fields directly; only drop the lock for work that needs it.

**Why it hid.** `RecalculatePath` only runs after `handleStuckRecalcLocked` has
passed rung 3 of the ladder, which needs `ConsecutiveStuckCount >= 3` **and**
`CanDirectSteerToTarget()` — i.e. the destination has to be within
`directSteerWalkToRadius` (16). So the freeze needed a bot that got stuck three
times close to its target, which is why it appeared deep into a walk rather than
at the start.

**Test.** `tests/bot/movement/stuck_ladder_lock_test.go` calls the ladder with
`b.Mu` held (via `TickContext.HandleStuckRecalcForTest`) under a 5s deadline. It
drives the real function rather than a copy: a re-locking implementation wedges.
Verified by reverting the fix — the test fails with the wedge message.

Grep for `b.Mu.Lock()` inside any `*Locked` function when this shape appears.

## 16. Staircases & Wall-Paved Platform Climax Veto

**Symptom.** Bot stops ~4-6 blocks short of its destination (e.g. `closest_reached={X:4 Y:72 Z:-6} blocks_below_target=1`), or skirts around the front stairs taking wide detours instead of climbing directly.

**Cause.**
1. `wallBlocksStanding` compared `float64(y) + 0.5 > w.footingTopY() + 0.6` where `footingTopY()` was statically locked to the search's initial `startY`. Any wall-paved platform higher than `startY + 0.6` had `canStandAt` permanently vetoed during A* expansion.
2. `stepJumpTargetClear` unconditionally refused step-jumps onto any wall floor (`isFenceOrWall`), blocking the transition from the top stair onto the wall-paved platform.
3. Standard `LinkStepJump` cost of 4.5 caused A* to prefer flat detours around the side over climbing stairs.

**Fix.**
- `wallBlocksStanding(x, y, z)` validates ceiling clearance (`IsSolid(x, y+2, z) || IsHazard(x, y+2, z)`) without locking against initial `startY`.
- `stepJumpTargetClear(tx, ty, tz, from)` permits climbing onto a wall platform when approaching from a stair/slab (`isHalfBlock(from.X, from.Y-1, from.Z)`), while continuing to refuse jumps onto 1.5-block walls from ground or another wall.
- Stair transitions (`isHalfBlock` on current or target floor) are priced at low cost `1.2` rather than `4.5` so A* naturally chooses the staircase.

## 17. Slabs: A* level vs physics height (hover / stuck on slab)

Symptoms: bot hops in place over a `*_slab`, or stops at a slab next to full stone.

- 1.21 palettes name the half with the string state `minecraft:vertical_half` (not `top_half_bit`); `TopOffsetFromState` handles both, `*_double_slab` = 1.0.
- A* models every solid as a full cell (standing on slab at cell c = node c+1) but physics rests at c+0.5. Anything comparing a route node to `floor(feet+0.1)` must use `movement.pathLevelY` (reports c+1 on a partial block), otherwise every node looks one level higher -> endless step-up jumps ("floating").
- No auto-step existed: a 0.5 rise (slab -> full block, full -> slab) walked into the block. `applyStepUpAssist` (movement/step_up_assist.go) lifts a grounded body onto ledges <= 0.6 with head room; the wall scan skips such ledges via `surfaceTopY`.
- A* `stepJumpRiseReachable`: a +1 level jump needs `top(target) - top(source) <= 0.25` (slab -> full block one level up is a 1.5 climb). Stairs are not judged.
- Evidence: `path terrain probe` log line (y:rid:name:solid:top around the start cell).

## Verification

- `go test ./tests/...` passes.
- `go test -v ./tests/bot/pathfinder -run "Staircase|Wall|Fence"` passes.
- `go test -v ./tests/bot/movement -run TestDescentNodeDoesNotAdvancePathUntilLanded` passes.
- `go test -v ./tests/bot/movement -run "TestResidueFloat|TestStepJumpFiresFromSettledFloor"` passes (step-jump residue freeze).
- `go test -v ./tests/bot -run TestPlayerNameMatches` passes.
- `go test -v ./tests/world -run TestGeyserPaletteAirAndSolidIdentification` passes.
- `go test -v ./tests/world -run "TestGeyserProxyPaletteWinsOverLocalDragonflyTables|TestUnknownNetworkIndexIsPassableOnGeyser"` passes (network index vs local RID collision → A* dead end).
- `go test -v ./tests/bot/pathfinder -run "TestWallFloorSurfaceIsWalkableAtTheBotsOwnLevel|TestStepUpFromOrdinaryGroundOntoAWallPavedSurface|TestStepJumpOntoAWallIsStillRefused|TestFenceIsStillNotClimbedFromBelow"` passes (wall/fence floor veto, §13).
- `go test -v ./tests/bot/movement -run TestStuckRecalcKeepsARouteWithoutDeadlocking -timeout 60s` passes; reverting the fix makes it fail on the 5s deadline (self-deadlock, §15).
- `go test -v ./tests/bot/movement -run TestBodyClearanceKeepsSlabUnderFeetSolid` passes (§17: per-tick bodyClearance must not erase the slab under the feet, see applyBodyClearance in path_level.go).

### 17b. bodyClearance erased the slab under the feet

SendInputLoop marks feet cell Floor(y) (+1,+2) as body clearance, and IsSolid returns false for those. On a bottom slab the feet cell IS the slab, so grounded/pathLevelY/start-snap all read air: grounded=false, bobbing at slab top+-0.3, A* start_y one level low. Fix: applyBodyClearance skips the feet cell when feet rest on a partial solid (feet >= y+top-0.05). Symptom in logs: stall probe grounded=false with here=...slab top=0.50 and path_level == cell.

### 17c. Server rubber-band on stair steps / slab rims (client model too coarse)

The local model treats stairs as full cubes, so a body overlapping a stair lower step (top 0.5) reads airborne, sinks, and CorrectPlayerMovePrediction snaps it back to the SAME x/y/z every ~1.5 ticks: position alternates A / A+(one tick move, -0.16 y), grounded=false, vel_y 0 / -0.16. Fix: handleCorrectPrediction records small upward corrections (NoteServerSupport, 0.02<lift<=0.75) in Bot.ServerSupportY/At; TickContext.applyServerSupport grounds the body at that Y for 350ms. Info logs: "server corrected position" (local vs server) and "walk progress" every 2s. Test: tests/bot/movement/server_support_test.go.

**Correction (verified live on nexusone):** Geyser does NOT send these resets as `CorrectPlayerMovePrediction`; they arrive as `MovePlayer` mode=2 (teleport). `ApplySelfMovePlayer` therefore also calls `NoteServerSupport` when `ack` is true, and the resting catch in `ApplyGroundLanding` uses `holdServerSupport()` (physics.go) so a body on carpet (top 0.06) / slab is not snapped down every tick (`server moved bot (teleport/reset)` log burst). Test: `TestServerResetAboveFloorHoldsBodyOnCarpet` (its second half must zero `ServerGroundAt`, otherwise the idle-bot anchor holds the body and hides the result).

Other fixes from the same nexusone work, each with a regression test under tests/bot/movement:
- **Stair-edge descent**: `bodyOverlapsSupportAt` + wide ground-check offsets let a body whose bbox still overlaps a lower support count as supported; the step-down assist is guarded so it does not fire against that.
- **Planned-fall exemption**: a node of type `fall` in the path is not treated as an unexpected drop.
- **Ledge-sense slab fix** and **airborne step-up gating**: step-up only fires from a grounded body.
- Known leftover: a one-time `server moved bot` burst when step_jumping onto a stone-brick slab (~3 s, self-recovers). Not investigated.
- Diagnostics `path terrain probe`, `stall probe`, `walk progress` are Info and noisy; downgrade to Debug if logs become unreadable.

## 18. Slab step-jump loop and distant-cliff freeze

- Symptom: bot pinned in front of a slab, MovePlayer mode=2 reset every ~150ms; A* plans `step_jump` over a slab (treats it as a full block). Fix: `aheadIsLowStep()` (step_up_assist.go) makes `handleStepUpJump` skip the jump when grounded so `applyStepUpAssist` walks onto it.
- Symptom: "movement: refused a step over a drop" with ground_ahead=3 and bot frozen on a planned walk link. `stopsAtLedge` refused whenever a cliff existed anywhere in the 4-block probe. Fix: only refuse when `ledge.Distance < 1` (next step is over air).
- Follow: `UpdateDistanceToPlayer` must run each tick or DistToPlayer stays 0 and follow never walks.
- Reset log is rate-limited to 1 per 2s; NavStallPollsLimit raised to 50 polls so recovery can act.
- Jump too short on Geyser: the wall scan skips rows whose top is within 0.6 of the feet, so a RISING body beside a full block slid into it (e.g. 0.42 up, block top 0.58 above). The host refused the buried position and reset the body, truncating the jump. Fix: `holdRisingBodyOffLedge` (speed_pos.go) blocks horizontal motion into any block reaching above the feet while `VelY > 0.05` and not grounded/on ladder; the body keeps rising and walks on once clear.

## 19. Towering under leaves ("the body never cleared the cell")

Leaves/logs in the cell at feet+2 stop the jump after a fraction of a block, so
`PlaceVerified` never sees the body clear the cell. `placeOneBlock` now breaks
`scaffold.TowerHeadroom(feet)` first. `FindScaffoldItem` no longer picks tools/stairs
for names containing "stone" and may use leaves. `chopTree` re-works still-standing
logs (up to 4 rounds, stops when a round removes nothing) instead of abandoning the tree.

## 20. Carpet rubber-band (Geyser only)

- Symptom: repeated `server moved bot (teleport/reset) mode=2`, local X snaps back ~0.5 each time, `stall probe` with `ahead=` showing `moss_carpet solid=false top=0.06` on a `walk` node. The pathfinder treats carpet as passable, but the Java host collides with its 1/16 shape; a box overlapping it at the same Y is rejected, and the host never lifts the bot (so `NoteServerSupport` never engages).
- Fix: `tc.carpetSurface` (step_up_assist.go, gated on `WorldCache.GeyserCompat()`) + `WorldCache.IsCarpetAt`. `applyStepUpAssist` lifts onto the carpet top (exempt from the airborne/route gate); `ApplyGroundLanding` keeps the body on the carpet top while its box overlaps it and lets it sink once clear. Do NOT use `holdServerSupport` as a self-claim: it renews while any solid is under the body and the bot would float 0.0625 over bare floor.
- Tests: `tests/bot/movement/carpet_step_test.go`.

## 21. Floating after a /tpa teleport (Geyser)

Symptom: after a teleport the bot hovers ~1 block above the ground until it walks; `bot took
damage! marking block below feet as hazard` logs a Y one higher than the real floor; A* later
plans a `fall` from start_y = floor+2.

Cause of the *hold*: `ApplyServerPositionAnchor` pins a stationary body to `ServerGroundY`, and
Geyser echoes our own PlayerAuthInput Y back as MovePlayer, which refreshes the anchor (and it
self-renews), so any transient +1 Y error stays forever while idle. It clears as soon as the bot
has a path (anchor is skipped), matching "normal again after walking".
Fix (Geyser only): `floatingOverAir()` in `movement/physics.go` - when every cell under the box
is loaded literally `minecraft:air` and the cell below is solid, do not anchor; gravity lands
the body. Partial floors (snow/slab/carpet) are not air so they still anchor.
Also: `handleChangeDimension` ignores Geyser's fake position (y=32767); new Info logs
`server teleported idle bot` and `self respawn position` expose raw/feet Y to find the origin of
the +1 (still unidentified - read these logs after a /tpa).
