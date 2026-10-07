---
name: gophertunnel-stop-cancels-work
description: Use when a player tells this Go Bedrock bot to stop and it says it is stopping but keeps working — keeps felling trees, re-pathfinding, fishing, exploring, building. Covers the uncancellable context.Background() that makes "stop" cosmetic, and how to tell a genuinely stuck bot from one whose job was never cancelled.
---

# Gophertunnel Stop Must Cancel Work

## When to Use

Use when the bot acknowledges a stop and then keeps going, when it replies
"okay, stopping" and then re-navigates, or when `Stop()` appears to do nothing.

## When NOT to Use

Do not use for a bot that is genuinely stuck against terrain (see
`gophertunnel-geyser-movement-freeze` and the stall-probe fields), or for a task
that legitimately has no cancel point yet.

## Tell the two apart before changing anything

A cancelled job and a stuck bot look almost identical in the log. Both show
repeated `recalculating path using A*` and both show `stall probe ...
no_progress=true`.

The giveaway is what happens right after the acknowledgement:

```
19:34:18.006 chat reply sending reply="Yaudah deh, aku stop dulu ya."   <- stop accepted
19:34:21.475 chat packet received message="Hey! Sorry, but you can't break that block here."
19:34:21.605 recalculating path using A* start_x=1 ... target_x=6 ... movement_state=walk_to
```

A stuck bot was already stuck. A bot that starts a *fresh* A* run at a *new*
target after agreeing to stop is still executing its old task. Check whether the
target changed — a stuck bot re-plans the identical route; an uncancelled job
moves on to the next tree.

## Root cause

`internal/bot/action/execute.go` started every background job with
`context.Background()`. That context can never be cancelled, so `b.Stop()` — which
only cleared movement state — had nothing to cancel. The gather loop
(`fellTreesUntilTarget`) checks `ctx.Done()` once per trunk and correctly returns;
it was simply never given a context anybody could cancel.

## Procedure

1. `Bot` owns an activity context (`activityCtx`/`activityCancel`), created lazily
   by `ActivityContext()` and renewed by `CancelActivity()`. Callers hold it in
   `activityMu`; never `b.Mu`, which the job itself locks.
2. Hand long jobs `b.ActivityContext()`, never `context.Background()`.
3. `Stop()` calls `CancelActivity()` first, then clears movement state. Order does
   not matter much, but cancelling first stops the job before the legs stop.
4. `CancelActivity` opens a **fresh** context. Reusing the cancelled one would
   leave the bot permanently unable to accept work.
5. Before wiring a call site up, confirm the callee actually honours `ctx`:
   `grep -rc "ctx.Done()\|ctx.Err()" internal/bot/<pkg>/*.go`. Handing a
   cancellable context to a function that ignores it buys nothing.
6. Gate on movement too — `b.Stop()` clearing `MovementState` is what a "stop" the
   player can see actually is.

## Currently routed through the activity context

Gathering (wood and blocks), collecting drops, fishing, exploring (random,
directional, return-to-origin), sleeping, torch placement, emergency shelter,
death recovery, animal husbandry, mounting.

**Not yet cancellable:** `farmer` and `building` contain **zero** `ctx.Done()`
checks. Routing them through `ActivityContext()` alone will not make stop work —
the checks have to be added inside those loops first. Chest store/give, furnace
smelt, and the NPC interact click are short single operations and are left on
their own contexts deliberately.

## Common Pitfalls

- Fixing only the gather call site. `context.Background()` appeared at ~29 sites;
  a partial fix makes stop work for woodcutting and silently not for fishing.
- Wiring a context into a function that never checks it, then declaring the
  problem solved.
- Reusing the cancelled context. The next `gather` is born dead and the bot
  reports "tidak ada pohon" forever.
- Holding `b.Mu` while cancelling: a running job takes `b.Mu` constantly.
- `NavigateToBlock` does not take a context, so a stop lands at the next tree
  boundary rather than mid-walk. That is acceptable; the job must not resume.

## Verification

```sh
go build ./... ; go test -count=1 ./tests/... 2>&1 | grep -E "^(FAIL|---|panic)|\.go:[0-9]+"
go vet ./internal/... ./tests/...
```

Empty output means pass. `tests/bot/activity_stop_test.go` covers cancellation,
renewal, repeated stops, and context stability without a connection.

Then have the operator ask the bot to chop wood and say "berhenti" mid-run.
The bot must go quiet: no further `recalculating path`, no further
`A* pathfinding completed`, no more break attempts. Passing unit tests prove the
context is cancellable, not that the job returns promptly.