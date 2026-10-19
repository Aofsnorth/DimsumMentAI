---
name: agi-passive-idle-loop
description: "Use when this Go bot's AGI runs and Jev decides every tick but the body stays passive or \"stuck\" — activity=rest forever, \"hungry, eating\" spam, repeated \"acknowledged a player\" lines, A* giving up short on routes through the bot's own feet, dictated !remember rules recited in chat but never executed, or \"nyangkut/stuck\" self-reports during explore. Distinct from a deadlock: decisions ARE produced, they are just consumed, poisoned, or never reach a model that can act on prose."
---

# AGI passive while alive: the idle/reflex consumption loop

## When to Use

- Log shows `AGI: jev decided ... activity=rest` on nearly every tick while players wait for the bot to act.
- `AGI: hungry, eating` fires every tick (hunger can never be satisfied — e.g. lobby, no food) and the Jev answer bought that tick is never used.
- `AGI: acknowledged a player` repeats for the same person every ~15s (gaze cooldown) like a metronome.
- Bot ignores dictated `!remember` rules when deciding (it recites them in chat fine).
- The skill `agi-perception-deadlock` does NOT apply: Tick() is returning, Observe is not wedged — the decisions are just being thrown away or turned into stillness.

## When NOT to Use

- `AGI: naturalTick` or Jev log lines stop entirely → deadlock, use agi-perception-deadlock.
- Decisions produce actions but the server rejects motion → movement/packet problem, not the brain.

## Root causes found in one live session (all fixed; check for regressions)

1. **Idle window reopening (idle.go `Idle`)**: each expired rest window reopened a fresh one.
   The idle nudges move the body a block or two, refreshing `lastMoved`, so `ShouldDrift`
   (90s) never fired → statue forever. Fix: `maxConsecutiveIdles` cap; when spent, drift and
   reset. Reset streak in `doActivity` for any non-rest activity and in `Wander`.
   Rule encoded in tests: **rest must NOT reset its own streak**.
2. **Routine reflexes swallowing paid-for Jev verdicts (runner.go `Tick`)**: ReflexEat fires
   every tick when hunger is unsatisfiable; the old code returned immediately, discarding the
   Jev answer paid for that second (~2.4k tokens/s wasted). Fix: only survival-critical
   reflexes (Flee, Surface) still consume the tick; Eat/Look run the already-paid-for activity
   afterwards.
3. **Memory never reached the decider (brain.go Snapshot.MemoryText + DescribeState)**:
   `!remember` rules went to the chat LLM system prompt only. Jev — the per-tick decider —
   never saw them, so "kalau masih di lobby, klik NPC" could not influence behaviour.
   Fix: `Snapshot.MemoryText` filled in Observe, rendered in `DescribeState` under the goal.
4. **Acknowledgement window = gaze cooldown (runner.go `lookAt`/`greetedRecently`)**: with a
   15s window the look reflex re-fired on the same player 4x/minute, each firing consuming a
   whole tick. Fix: acknowledgement window now the social cooldown (120s), gaze cooldown still
   gates the actual head turn.
5. **Thresholds too timid in configs/bot.yaml** (operator-owned file — do not commit changes
   to it blindly): speak_threshold 0.75 was above every observed score (0.15–0.32) and
   engage_threshold 0.4 above most. Live Jev scores sat at speak≈0.2, so the bot never spoke
   and never counted as engaged. Local-only change: speak 0.55, engage 0.3.
6. **Damage poisoning the world model (network/player/recipes.go)**: every damage event
   permanently marked the block under the bot's feet as hazard. Falls produced by the explore
   route's own fall/step_jump links count as damage — so the bot marked the grass it STOOD ON,
   A* refused routes through its own feet ("A* gave up short" + "A* frontier" warnings), and
   every explore waypoint became "stuck". Fix: `LocalWorldModel.SetTempHazard(x,y,z,dur)` —
   a `tempHazardBlocks map[int64]time.Time` that expires on read — used by the damage path
   (10s). Permanent `SetHazard` remains for scanned hazards (lava/fire).
7. **The prose-reading model never consulted in natural free play (natural.go)**: Jev answers
   in a fixed vocabulary (rest/explore/…) and CANNOT execute dictated rules like "klik NPC di
   depan". `deliberate()` is the only path carrying !remember memory into a prompt, and the
   natural loop never called it. Fix: `deepThinkInterval` (45s) — `FreePlay` now hands the
   decision to `deliberate` via `DueForDeepThink` (gated on: client exists, not busy, not
   exploring, window elapsed). Result: dictated navigation instructions are acted on within
   a minute or two instead of only being recited in chat.

## Reading the log (fast triage)

- `jev decided ... engaged=false activity=rest confidence=0.9` forever → thresholds or FreePlay
  drift guard; check speak/engage thresholds against the observed score ranges in the log.
- `hungry, eating` every tick + no movement → ReflexEat swallowing ticks; verify Tick applies
  the Jev activity after routine reflexes.
- `idling mode=... for=...` repeating every 6–55s without a wander in between → idle streak
  cap missing or being reset by rest.
- `acknowledged a player` same name, every 15s → greeted window too short.
- `bot took damage! marking block below feet as hazard` followed by repeated
  `A* gave up short` / `A* frontier: what stopped the climb` and self-reported "stuck" chat →
  permanent damage hazards poisoning the world model; damage path must use SetTempHazard.
- Bot recites a dictated rule in chat but keeps choosing explore/rest → deep-think turn
  missing from natural FreePlay; check DueForDeepThink wiring and that AiClient is non-nil.

## Procedure

1. Reproduce from the log only if possible; else run with `log_level: info` and capture 2–3 min.
2. Write the failing-shape test first in `tests/bot/agi/` (bare runner suffices — see
   idle_streak_test.go; use `RecordActivityForTest`).
3. Fix the narrowest cause; keep rest REACHABLE (a bot that never rests is also a bug —
   TestRestPeriodsEndOnTheirOwn pins that).
4. Bare-runner safety: `Idle`'s drift path reaches `maybeWander`, which must nil-check `r.b`
   and `r.b.Explorer` — Idle is called from tests with no bot attached.

## Verification

- `go build ./...`, `go vet ./...` clean.
- `go test -count=1 ./tests/bot/agi/... ./tests/agi/... ./tests/pathfinder/...` pass, including
  TestIdleRefusesToOpenAFourthConsecutiveWindow, TestDeepThinkIsDueBeforeTheFirstAndThenPaced,
  and TestTempHazardBlocksWhileLiveAndClearsAfter.
- `golangci-lint run --timeout 5m` → 0 issues.
- Live: within ~4 minutes of idle the bot must log `rest budget spent, drifting instead` or a
  wander, dictated `!remember` lines must appear in Jev's decision context, an
  `AGI: unprompted message` or an executed action must follow a deep-think turn within ~90s,
  and "A* gave up short" must not repeat right after a damage log line.
