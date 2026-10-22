---
name: gophertunnel-proportionate-self-defence
description: Use when this Go Bedrock bot over-reacts to being hit — a single bare-handed punch turning into a sword duel, a chase across the map, or the bot attacking the very player who poked it. Covers damage attribution (why "a player was nearby" is not evidence), the warn/defend/duel ladder, and the never-fight list.
---

# Gophertunnel Proportionate Self-Defence

## When to Use

Use when the bot looks aggressive: it draws a weapon over one punch, chases a
player who walked away, keeps swinging until somebody dies, or hits back at the
player who owns it.

## When NOT to Use

Do not use for a bot that ignores being attacked entirely (check attribution —
the `Animate` and `SetLastHurtBy` handlers must be registered), or for
hostile-mob fights, which are a separate path (`ThreatDetector.Scan` →
`EngageTarget`).

## The shape of the bug

`ThreatDetector.OnDamaged` used to call `CombatManager.EngagePlayer` on any
health drop while any player was within 4.5 blocks. `EngagePlayer` is the PvP
entry point: it equips the best weapon, strafes the arena, and the tick loop
chases until the target dies or reaches 32 blocks. So:

```
one bare-handed punch  ->  sword out, 32-block pursuit, no stop condition
```

Two independent defects produced that:

1. **No attribution.** Every health drop is the same packet — falls, mobs,
   poison, players. Reading "nearest player" as the attacker is how fall damage
   taken next to the owner became a duel against the owner.
2. **No proportionality.** Being hit had exactly one response, and it was the
   maximum one. Nothing in between existed.

## Procedure

1. **Attribute before reacting.** `internal/bot/network/player/damage.go`
   registers two corroborating signals:
   - `Animate` with `AnimateActionSwingArm` from a tracked player's runtime ID →
     `NotePlayerSwing(id)`. Ignore `SwingSource` values that are not attack
     (mining next to the bot is work, not a hit) and ignore the bot's own ID.
   - `SetLastHurtBy` with `EntityType == 319` (Bedrock's player actor type) →
     `NotePlayerDamage()`. It carries a type, never a runtime ID, so it
     corroborates but cannot name.
   `ThreatDetector.hitsFromThisAttacker` requires one of them inside its window.
   No signal means no reaction, which is the safe default.
2. **Never fight an ally.** `run.go` installs `SetAllyFilter` over
   `AiCfg.IsMainPlayer(name) || b.IsWhitelistedPlayer(name)`. A poke from the
   owner is a conversation. Explicit `<action>pvp:name</action>` still works —
   that is a request, not a reflex.
3. **Escalate in steps** (`internal/bot/combat/retaliation.go`):
   `ResponseForHits` returns warn (1 hit), defend (2–3), duel (4+). `HitTracker`
   keeps the count per attacker and resets after `retaliationQuietGap` of quiet,
   so two unrelated pokes are two pokes.
4. **Defending is a place you stand.** `EngageDefender` sets `ModeDefend`;
   `tickDefend` looks at the attacker, calls `StopMovement`, swings only when
   they are inside `fairReach`, and disengages past `defendLeash` (4.5), after
   `defendDuration` (6 s), or after `defendMaxSwings` (3). It never calls
   `holdWeapon` or `moveByPlan` — that is the whole difference from a duel.
5. **Do not refill the swing budget.** `EngageDefender` resets `defendSwings`
   only when a *new* answer starts. A hit arriving mid-answer extends
   `defendUntil` and nothing else, or a player on a rhythm gets three free
   swings per punch.
6. **Let the model do the talking.** Each level injects an `InjectAIEvent` that
   says which level it is ("you are NOT fighting back", "standing your ground,
   not chasing") and asks for one short line with no action tags. Never build
   the sentence in Go.

## Common Pitfalls

- **Reading position as guilt.** The nearest player being close is not evidence.
  This single mistake produced the "agresif banget" report.
- **`EngagePlayer` from a reflex.** It is the duel entry point; use
  `EngageDefender` for reflexive answers and reserve `EngagePlayer` for explicit
  requests and the top of the ladder.
- **Forgetting `cm.mode` on the other entry points.** `EngageTarget` must set
  `ModeMob` and `EngagePlayer` must set `ModeDuel`; a stale `ModeDefend` makes a
  real duel behave like a defensive answer (no chase, capped swings).
- **Reporting "Target eliminated" for a defence.** When the attacker leaves
  tracking, a defend-mode tick must disengage quietly. The kill message makes
  the model narrate a fight nobody finished.
- **Assuming the server relays `Animate`.** Some do not; that is why
  `SetLastHurtBy` is a second, independent signal.

## Verification

```sh
go build -buildvcs=false ./... ; go vet -buildvcs=false ./...
go test -buildvcs=false ./tests/bot/combat/ -count=1
```

`tests/bot/combat/pvp_test.go` covers the ladder and attribution;
`tests/bot/combat/retaliation_test.go` covers the defensive answer (no chase, no
weapon draw, swing cap, no budget refill). Mutation-check a new guard: set
`retaliationDuelHits = 1` and `TestFirstHitWarnsInsteadOfFighting` must fail.

Live check: punch the bot once with an empty hand. It must turn, say one line,
and not swing. Punch it three more times and it should fight properly.
