---
name: gophertunnel-emote-jump-physics
description: Use when this Go bot's jump is wrong in-game — jumps too short ("pendek bgt"), jumps in the air repeatedly, or does not jump at all when told "lompat". Covers where the jump impulse must be bought relative to the physics phase, the InputFlagJumping wire rules, and this server's grounded=false vel_y=0 standing state.
---

# Gophertunnel Emote Jump Physics

## When to Use

Use when the bot's jump misbehaves on the live server: the hop happens but is a fraction of a block tall, the bot jumps again off the sky with no gravity, a "lompat" command produces a reply but no hop, or a scaffold jump is consumed silently.

## When NOT to Use

Do not use for pathfinding failures ("Could not reach tree base"), step-up assist, or fall damage — those are A*/grounding problems. Do not use for the emote *animation* not rendering; the emote visual is separate from the physical hop.

## The One Rule

A wire flag without its impulse behind it is the bug. `InputFlagJumping` may only go out on a tick whose physics phase actually integrated the 0.42 impulse (`tc.ShouldJump` set BEFORE `runPhysicsAndCollisions`, cleared by the airborne branch). Buying a hop at packet time sends a flag whose position claims never left the floor: the server arces the body up, the bot's own claims stay at ground level, and the tug of war renders as a fraction-block hop.

## Procedure

1. Find the buyer. Every jump path must set `tc.ShouldJump` before physics: `takeRequestedJump` (latched request), `applySprintHop`, `buyEmoteJump` (emote window, one hop per latch). The emote buyer lives in the tick loop inside the `!venityIdle` branch, immediately before `runPhysicsAndCollisions`.
2. Gate identically everywhere: `b.IsGrounded || tc.VelY == 0` (`jumpableNow`). This server reports a standing body as grounded=false with vel_y exactly zero tick after tick, so a grounded-only gate makes "lompat" a no-op. `tc.VelY` is the tick's own copy seeded from the bot at context build — read THAT, never `b.VelY` (the bot mirror is stale and reads 0 for a body rising at 0.42, which turns one hop into climbing the air).
3. Wire derives from physics only: `if tc.ShouldJump { Jumping + JumpDown }`. Never set `InputFlagJumping` from emote state directly at packet time.
4. One impulse per intent: the emote latch (`b.EmoteJumpSpent`) is spent by the tick that buys the hop, survives the whole ~80-tick window (landing mid-emote must NOT re-hop), and is reset only by the next `TriggerEmoteFor`. An airborne refusal leaves the request latch unspent (the landing tick honours it) but leaves the emote latch spent — see `takeRequestedJump` for the distinction.
5. The impulse itself integrates only when the world model agrees (`tc.IsGrounded` in `ApplyVerticalVelocity`); the airborne branch clears `ShouldJump` so the wire can never claim a jump the body did not make.

## Common Pitfalls

- Reading `b.VelY` instead of `tc.VelY` in the jump gate: the bot field is a stale mirror (synced at tick end by `speed_pos.go`, clobbered by `SetActorMotion`), so an airborne body with an all-zero mirror looks jumpable.
- Holding the flag across ticks to "help" the server: a held key is what produced "lompat di udara naik terus lagi". Exactly one flag tick per integrated impulse.
- Testing with a bare `TickContext` and no `VelY`: production always seeds `tc.VelY = b.VelY`; a test that stages "airborne" must stage a nonzero `tc.VelY` (e.g. -0.46 falling) or the gate reads vertical rest and buys the hop.
- `TriggerEmoteFor` needs a non-nil `b.Logger` (it Debug-logs) and resets `EmoteJumpSpent` — staging emote state by writing `EmoteState` directly leaves the latch from a previous window.
- The world model must know the floor for the impulse to integrate; if a server regresses to "no jump at all", check the terrain probe logs (chunk decode) before touching the jump code.

## Verification

1. `go build -buildvcs=false ./...`
2. `go test ./tests/bot/movement/` — pins: `TestJumpEmoteDoesNotPairJumpingWithAirborneCollision` (falling body refuses + no wire flags), `TestJumpEmoteBuysExactlyOnePhysicalHop` (impulse 0.42 integrated on the buying tick, one flag, second grounded tick clean), `TestJumpEmoteRemainsSpentUntilNextEmoteStarts`, `TestRequestedJumpIsNotConsumedWhileAirborne`, and the arc test (apex 1.20–1.40).
3. Live check: tell the bot "lompat"; it must rise a full vanilla-height hop (≈1.25 blocks), land, and stay down. A hop that rises a few tenths of a block and falls back means a flag went out without an impulse — check that `buyEmoteJump` is called before `runPhysicsAndCollisions`.
