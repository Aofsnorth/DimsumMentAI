---
name: venity-compat-work
description: Ongoing Venity server compat work — camera smoothing and walking fix via reverse engineering
metadata:
  type: project
---

User wants Venity-server-specific (play.venity.net) fixes: (1) less stiff/robotic camera, (2) make walking work. Venity has antibot/anticheat requiring protocol reverse engineering. User CANNOT have me connect to Venity — verification is "user runs & reports back" with debug logs.

**Architecture:** Bot simulates its own physics/position locally each tick, sends `PlayerAuthInput` with computed Position+Delta; Venity's rewind anticheat validates. Venity uses `RewindMovement` but never sends `CorrectPlayerMovePrediction`, so [[venity-compat-work]] handshake force-sets `TickSynced=true`.

**Iteration 1 (done, 2026-06-15):**
- Camera: added `EaseAngle`/`EasePitch` (ease-out) in movement/utils.go; wired via `applyVenityEasedLook` in control.go (Venity-only); loosened `venityLookOnly` throttle in packet.go from 1.0° → 0.1° (sub-degree eased steps were being dropped = robotic).
- Walking: prime suspect H1 — Venity decodes chunks lazily (radius 2, async) so ground cell under next step is *unknown* not air; `hasGroundSupportAt` (uses IsSolid→false for unloaded) made guard in speed_pos.go cancel EVERY forward step. Fix: `groundSupportUnknownAt` (uses WorldCache.IsBlockSolid `loaded` flag) → trust path when unknown.
- Diagnostics: `logVenityWalkBlocked` (speed_pos.go) + server-snap detector (move.go) to disambiguate H1/H2(server pins pos)/H3(input rejected). runId "venity-walk-v1".

**Why:** walking was "sama sekali tidak bisa" (pos never changes); camera "kaku/robotik tapi mulus".
**How to apply:** debug logs need `log_level: debug` in configs/bot.yaml (currently "info"). Logs → logs/debug-090ce4.log. Next: user reports log, confirm H1 vs H2/H3.
