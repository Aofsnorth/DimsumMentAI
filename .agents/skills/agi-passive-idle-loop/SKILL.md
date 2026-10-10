---
name: agi-passive-idle-loop
description: Use when this Go bot's AGI runs and Jev decides every tick but the body stays passive — activity=rest forever, "hungry, eating" spam every tick, repeated "acknowledged a player" lines, or dictated !remember rules that the bot seems to ignore. Distinct from a deadlock: decisions ARE produced, they are just consumed or discarded.
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

## Reading the log (fast triage)

- `jev decided ... engaged=false activity=rest confidence=0.9` forever → thresholds or FreePlay
  drift guard; check speak/engage thresholds against the observed score ranges in the log.
- `hungry, eating` every tick + no movement → ReflexEat swallowing ticks; verify Tick applies
  the Jev activity after routine reflexes.
- `idling mode=... for=...` repeating every 6–55s without a wander in between → idle streak
  cap missing or being reset by rest.
- `acknowledged a player` same name, every 15s → greeted window too short.

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
- `go test -count=1 ./tests/bot/agi/... ./tests/agi/...` pass, including
  TestIdleRefusesToOpenAFourthConsecutiveWindow and TestDoingSomethingResetsTheIdleStreak.
- `golangci-lint run --timeout 5m` → 0 issues.
- Live: within ~4 minutes of idle the bot must log `rest budget spent, drifting instead` or a
  wander, and dictated `!remember` lines must appear in Jev's decision context.
