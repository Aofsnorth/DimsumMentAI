---
name: gophertunnel-arm-swing-pacing
description: Use when tuning arm swing animations (Animate packets) in this Go bot so they look human — mining/chopping/dropping swings that look twitchy, robotic, or machine-gun fast, or when adding new swing sources (mine, attack, interact, drop).
---

# Gophertunnel Arm Swing Pacing

## When to Use

- A viewer says the bot's arm "vibrates", "twitches", or looks robotic while it mines or chops.
- Adding or changing `packet.Animate` swing rhythm (mining, chopping, obstacle breaking, drops, attacks).
- Deciding how often to re-send `Animate` during a long action.

## When NOT to Use

- Attack swing timing in combat (single swings per attack, not a loop).
- Crack/break progress rendering — that is server-driven from `PlayerAuthInput.BlockActions` (`ContinueDestroy` per tick), not from `Animate` cadence.
- Idle nudges or movement physics.

## The Core Rule

Every `packet.Animate{ActionType: AnimateActionSwingArm}` makes each viewer's client
**replay the full arm-swing cycle from the start** (~300 ms). The pacing rules follow:

1. **Never swing faster than the animation cycle.** An interval below ~250 ms restarts
   the cycle before it finishes, so viewers see the arm vibrate, not swing. The old
   70-110 ms chop cadence and the 150 ms fixed miner tick both had this bug.
2. **Human pace is 2.5-4 swings/second** — 260-400 ms between swings.
3. **Vary the beat.** A metronome reads as a bot even at the right speed: use a short
   burst (2-3 swings), a longer recovery (460-760 ms), randomize inside both ranges,
   and add a wind-up (100-220 ms) before the first swing.
4. **Always send the matching `SwingSource`**: `AnimateSwingSourceMine` for breaking,
   `AnimateSwingSourceInteract` for clicks, `AnimateSwingSourceDropItem` for tosses,
   `AnimateSwingSourceAttack` for hits. A mine-swing reads as digging, not clicking.

## Where the Working Implementation Lives

- `internal/bot/movement/animation/rhythm.go` — **the one rhythm**: `WindUp`,
  `Cadence`, `JitteredAim`, and `Beats` (the whole break laid out as a list of
  timed beats). Unit-tested in `rhythm_test.go`.
- `internal/bot/movement/animation/swing.go` — the swing packet builders.
- `internal/bot/gathering/chop_action.go` — `swingUntilBreak` walks
  `animation.Beats`; `chopWindUp`/`chopCadence`/`chopAim` are thin aliases kept
  for the existing tests.
- `internal/bot/gathering/miner.go` — `mineSingle` walks the same beats.
- `internal/bot/break_obstacle.go` — the unstick break uses the same beats.

Use `animation.Beats(breakTime, aim)` for any new break path. Do not hand-roll
a loop with a fixed interval: that is the bug this skill exists for.

## Adding a Break Path

`Beats` returns a wind-up beat followed by one beat per swing. The first entry
is the wind-up: sleep it and send **no** swing. `breakTime` is the break
duration, and the beats are arranged not to overshoot it — a PredictDestroy
that arrives early is silently rejected on a server-authoritative host.

`Beats` never emits a swing whose pause is under the swing floor. A final swing
trimmed to the leftovers (as an earlier version did, producing an 86 ms gap)
restarts the viewer's arm cycle mid-flight, which is the exact vibration the
rhythm exists to prevent.

## Common Pitfalls

- Clamping the final wait to the exact break duration is fine, but account for the
  wind-up conservatively (`elapsed += chopWindUpMin`) so `PredictDestroy` is not sent
  early on server-auth-block-breaking hosts (they silently reject early finishes).
- Aim jitter (`chopAim`, ±0.12 blocks) keeps the head from being welded to one pixel;
  without it even a good rhythm looks automated.
- The arm swing is cosmetic: block break timing is governed by
  `sabdBreakDuration(serverAuthBreaking, block, tool)`, never by the swing loop.

## Verification

1. `go test ./internal/bot/movement/animation/` — pins the bounds, the variation,
   the wind-up, the jitter bound, and that no break length ever ends on a swing
   faster than the animation.
2. `go test ./internal/bot/gathering/ ./internal/bot/` — covers the chopper and
   the obstacle path.
3. Live check: watch the bot chop a tree — the arm should complete each swing with
   visible pauses every few swings, roughly 3 swings per second.
