---
name: gophertunnel-arm-swing-pacing
description: Use when the bot's arm swing while mining/chopping does not look like a normal player breaking a block — swinging too slow, pausing mid-break, vibrating, or firing once per block. Also covers adding new Animate swing sources.
---

# Gophertunnel Arm Swing Pacing

## When to Use

- A viewer says the bot "looks like it isn't really mining", stops swinging mid-break,
  or its arm looks twitchy/robotic while it chops or mines.
- Adding or changing `packet.Animate` cadence (mining, chopping, obstacle breaking).
- Deciding how often to re-send `Animate` during a break.

## When NOT to Use

- Attack swing timing in combat (single swings per attack, not a loop).
- Crack/break progress — server-driven from `PlayerAuthInput.BlockActions`
  (`ContinueDestroy` per tick), not from `Animate` cadence.
- Idle nudges or movement physics.

## The Core Rule

**Mining is continuous work.** A player holds the mine button and the arm works the whole
time the block is cracking. The Mojang block-breaking design doc says so directly —
`ClientInstance::tickDestroyBlock` calls `continueDestroyBlock` every simulation tick while
the input is held, and it "continues even if the player is swinging at air after having
broken a block. It only stops if the input is raised". There are no rests in it.

The swing *interval* is roughly 4-5 per second (`SwingMid` 230ms), not the 0.3s swing-arc
length — `minecraft:swing_duration` (default 0.3s) is how long one arc takes, NOT the gap
between arcs. Reading it as the interval is the mistake that left the bot sluggish. Dragonfly
is the in-repo reference: `ContinueBreaking` calls `SwingArm()` every 5th tick, continuously.

Both bounds matter, and the shipped bug crossed the upper one. A pause wider than the arc
parks the arm at rest mid-break — the bot "looks like it isn't really mining". A pause under
`SwingMin` (200ms) restarts the viewer's arm cycle mid-flight, reading as a vibration. So
`Beats` splits a break as a random partition where every pause clears `SwingMin` and the
total equals the break exactly.

`SwingSource` must match the action: `AnimateSwingSourceMine` for breaking,
`Interact` for clicks, `DropItem` for tosses, `Attack` for hits, `Build` for placing.

## Where the Implementation Lives

- `internal/bot/movement/animation/rhythm.go` — **the one rhythm**: `WindUp`, `Cadence`,
  `JitteredAim`, `Beats` (a break split into timed beats), and `Chain` (a rhythm that runs
  across several blocks). Constants: `WindUpMin/Max`, `SwingMin/Max/Mid`, `AimJitter`.
- `internal/bot/movement/animation/swing.go` — the packet builders (`MineSwing`,
  `InteractSwing`, `PlaceSwing`).
- `internal/bot/gathering/chop_action.go` — `swingUntilBreak` walks the chain.
- `internal/bot/gathering/miner.go` — `mineSingle` walks `Beats`.
- `internal/bot/break_obstacle.go`, `internal/bot/scaffold/scaffold.go`,
  `internal/bot/building/**` — same rhythm.
- Tests: `tests/bot/movement/animation/`, `tests/bot/gathering/chop_rhythm_test.go`,
  `tests/bot/animation/break_rhythm_test.go`.

Use `animation.Beats(breakTime, aim)` for a new single-block break, `animation.NewChain`
for a multi-block one. Do not hand-roll a loop with a fixed interval — that is the bug
this skill exists for, and `TestNoBreakPathHandRollsItsOwnSwing` fails the build on it.

## Common Pitfalls

- **The 0.3s swing-arc length is NOT the swing interval.** `minecraft:swing_duration` is how
  long one arc plays. Held-button mining re-triggers far more often (~4-5/sec). Anchoring
  the interval to the arc length makes the bot look sluggish.
- **Do not "fix" a slow-looking swing by making the pause longer.** A pause ≥ the arc length
  parks the arm at rest; that is the original bug. Faster is capped only by the vibration floor.
- **Never total-correct a jittered layout on one beat.** Both the "stretch the last beat" and
  "jitter everything, subtract the overshoot from the last" approaches drive a single pause out
  of bounds (438ms stall / 163ms vibration). `Beats` uses a random partition with a floor instead.
- **Beats must not overshoot the break.** An early `PredictDestroy` on a server-auth host is
  silently rejected. `TestABreakNeverOutlastsItsBlock` asserts the swung time equals the break exactly.
- **Aim jitter is per break, not per swing.** The look ease cannot follow a target that changes
  every ~230ms; re-rolling per swing makes the head chase, and the arm reads as wrong because the
  body it hangs off is twitching.
- **The swing is cosmetic; the break time is not.** Block duration comes from
  `sabdBreakDuration`, never from the swing loop.

## Verification

1. `go test -count=1 ./internal/... ./tests/...` — pins the floor and ceiling, the variation,
   the wind-up, the jitter bound, and that no break ends on a runt swing or overshoots.
2. Run the timing dump to see the actual beat list the viewer is sent:
   `go run ./cmd/beatcheck_main.go` (per-block swing count, total, longest pause).
3. Live check: watch the bot chop a tree. The arm should swing continuously at ~4-5/sec with no
   visible pause until the last log drops.
