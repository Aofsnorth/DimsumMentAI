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

- `internal/bot/gathering/chop_action.go` — `chopWindUp`/`chopCadence`/`chopAim` +
  the constants block (the canonical rhythm; documented and unit-tested in
  `chop_rhythm_test.go`).
- `internal/bot/gathering/miner.go` — `mineSingle` reuses the same rhythm.
- `internal/bot/break_obstacle.go` — obstacle unstick uses a 300 ms pace.
- `internal/bot/movement/animation/swing.go` — the swing packet builders.

## Common Pitfalls

- Clamping the final wait to the exact break duration is fine, but account for the
  wind-up conservatively (`elapsed += chopWindUpMin`) so `PredictDestroy` is not sent
  early on server-auth-block-breaking hosts (they silently reject early finishes).
- Aim jitter (`chopAim`, ±0.12 blocks) keeps the head from being welded to one pixel;
  without it even a good rhythm looks automated.
- The arm swing is cosmetic: block break timing is governed by
  `sabdBreakDuration(serverAuthBreaking, block, tool)`, never by the swing loop.

## Verification

1. `go test ./internal/bot/gathering/` (covers `chopCadence` bounds and variation,
   wind-up bounds, aim jitter).
2. Live check: watch the bot chop a tree — the arm should complete each swing with
   visible pauses every few swings, roughly 3 swings per second.
