---
name: gophertunnel-vertical-anchor
description: Debug a bot body that drifts or bobs up and down in this Go Bedrock bot, especially head tremor or aim bias when watching a nearby player after joining a LAN world.
---

# Vertical Position Anchor

## When to Use

Use when the bot's head visibly bobs up and down, when it aims above a player's
head, or when `posY` in `logs/debug-090ce4.log` changes while `mState=idle` and
`moveVec=0/0`. Also use for "why does the bot keep falling / snapping back on a
LAN world".

## When NOT to Use

For LAN join/discovery failures (use `gophertunnel-lan-discovery-debug`), crafting
or placement bugs, or head motion while genuinely walking — walking look
behaviour is intentional.

## Key Facts

- **The head symptom is a physics symptom.** `applyFollowLookTarget` derives pitch
  from `CurrPos.Y()`. A body that sags and gets snapped back by the server moves
  the target's apparent height by the same amount, so fixing pitch constants only
  hides the oscillation. Confirm the body is stable *first*.
- **Silence from the server means agreement.** With server-authoritative movement
  the host simulates the bot's input and corrects any position it disagrees with.
  Therefore a server-confirmed Y may be held indefinitely while the bot stays
  stationary — a time-based expiry on the anchor is wrong. The first version
  expired after 400 ms, so the bot sagged, got snapped back, and repeated: a
  ~1.5 Hz body bob that reads as the head trembling while tracking.
- **Partial-height floors never ground locally.** Snow layers (6 layers = +0.75),
  slabs and carpets are not classified solid by the world model, so gravity always
  wins locally and only the server's correction holds the bot up. A feet Y that
  is not block-aligned (e.g. 60.75 on top of block 60) is the signature.
- **Follow walk/stop needs hysteresis.** One 2.0-block threshold on both the
  engage and release side makes the state flap when the target hovers there,
  alternating the look target between the walking pose and the tracked pose.
- `updateGroundedState` only reports grounded when `WorldModel.IsSolid` says the
  block below is solid, and `SetChunkQuerier(b.WorldCache)` is already wired in
  `initSubsystems`, so IsSolid does consult server block data — for blocks it
  classifies as solid.
- The chicken-and-egg trap: recovering the floor is gated on `!isMidAir`, but
  `isMidAir` is already true once the bot starts falling, so the recovery can
  never fire. Do not try to fix this by re-gating that check.
- `LocalWorldModel` is **not** empty when unloaded. With no path bounds it assumes
  sea level (`y <= 62` is solid); with bounds set, only the start node's floor
  cell is solid. A test that uses a bare `NewLocalWorldModel()` therefore reports
  grounded and cannot reproduce the bug — set bounds to a distant node.
- Bedrock sends other players' positions with an eye-height offset
  (`playerType.NetworkOffset() == 1.621`), so `trackedPlayerFeetPosition` must
  subtract ~1.62 exactly once. Double-subtracting aims the bot at the shins;
  not subtracting at all aims above the head.

## Procedure

1. Read `posY` in `logs/debug-090ce4.log` at `tick%200==0` (and every 10 ticks
   while `trackingLook` is true). A stationary bot must hold a constant Y. A
   repeating cycle (e.g. 60.76 → 59.32 → 60.76) is the fall/snap signature.
2. Confirm the server is fighting the local sim: `CorrectPlayerMovePrediction`
   entries every ~6 ticks while `posY` still drifts. The entries carry `srvY` and
   `localY` — compare them to see who disagrees and by how much.
3. Anchor a stationary bot to the last server-confirmed Y
   (`applyServerPositionAnchor` in `internal/bot/movement/physics.go`). Record
   `ServerGroundY`/`ServerGroundAt` in `handleCorrectPrediction` and self
   `handleMovePlayer` in `internal/bot/network/player/move.go`.
4. **Self-renew the anchor every tick it engages.** The server speaks up whenever
   the reported position is physically invalid, so holding its confirmed Y needs
   no expiry. Never add a time-based staleness cut to a stationary anchor.
5. Keep the anchor inert whenever the bot owns its own vertical model: skip when
   grounded, on a ladder, jumping, parkour-jumping, walking, or on a path.
6. Bound the correction in **both** directions — compare `abs(delta)`, not
   `delta > limit`, or a bot teleported *below* its anchor gets pinned.
7. Give follow walk/stop a dead band (walk at 2.2, stop at 1.8; height 1.7/1.3)
   so a target hovering at the threshold cannot flap the look pose.
8. Only then revisit look angles, and re-check the follow-look eye-height maths.

## Common Pitfalls

- **Never add a time-based expiry to the stationary anchor.** The server corrects
  any position it disagrees with (it simulates the bot's input), so silence is
  agreement. An expiring anchor creates a metronome: hold → sag → snap → hold,
  which is exactly the "head tremor" symptom.
- Do not widen `naturalLookAngles` deadzones or shrink drift amplitudes to mask a
  moving body. That hides the real bug and costs aim accuracy.
- **Never re-introduce a hard deadzone in the look pipeline.** A discontinuous
  zero ("|pitch| < 3 deg → 0") chatters whenever a small real height difference
  puts the raw pitch on the threshold, which reads as a head tremor while
  tracking. Noise rejection must be continuous: EMA the target, then attenuate
  quadratically inside a soft level band (`smoothStationaryLookTarget`).
- **Never freeze the look target to the current angle** (the old
  `dampenLookJitter` did this for diffs < 1.4 deg). The head then never reaches
  its true angle and keeps a permanent tilt, which reads as aiming above the
  player's head.
- During follow, `LookTargetName` is set (24 h), so `applyTrackedLookTarget` →
  `naturalLookAngles` is the live path. `applyFollowLookTarget` only runs when
  the tracked player is out of view. Fixes applied only to `applyFollowLookTarget`
  will look correct in review and change nothing in game.
- Pinned static looks (`IdleLookTargetType == "static"`, used by drop aiming)
  must bypass the smoothing EMA, or action code that sleeps ~200 ms waiting for
  a forced angle stops converging in time.
- Bedrock pitch is negative looking up. A target above the bot produces a
  negative pitch; get the sign wrong in a test and it will tell you the head
  looks down when it looks up.
- Do not add a second `1.62` when converting tracked player positions to eye
  height; the offset is already stripped on ingest.
- Do not use a bare `NewLocalWorldModel()` in a regression test for this bug —
  it reports grounded and the test passes for the wrong reason.
- `abs32` in `internal/bot/movement/speed_pos.go` is integer-typed. A float guard
  needs its own helper or the comparison silently truncates.
- `go build ./cmd/bot` needs `-buildvcs=false`; git needs
  `-c safe.directory=D:/Work/Projects/MyProject/DimsumMentAI`.

## Verification

- `go test ./internal/bot/movement/` passes, including the anchor regression
  tests: stops idle free-fall, holds across 30 s of ticks, self-renews while
  stationary with no further packets, ignores real displacement, stays inert
  while moving, and the follow walk/stop latch does not flap in the dead band.
- `go build -buildvcs=false ./...` and `go vet ./internal/bot/...` are clean.
- Live proof: with `mState=idle` or tracking, consecutive `posY` values in
  `logs/debug-090ce4.log` are identical, `anchorDelta` sits at 0, and
  `CorrectPlayerMovePrediction` entries stop arriving (the server no longer
  disagrees). Requires `log_level: debug` in `configs/bot.yaml`.
