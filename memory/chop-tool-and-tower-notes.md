# Chop flow: tools, towers, and the 45-seconds-of-nothing run

Live-run findings from the "cariin 200 kayu" session on hansprojects (Geyser), all fixed:

## Tool matcher
- **"pickaxe" contains the substring "axe"** — the generic axe tier
  (`strings.Contains(n, "axe")`) matched a pickaxe, so the trunk was chopped
  with the wrong tool while the bot believed it had equipped its best axe.
  `gathering.IsAxeName` now excludes "pickaxe" explicitly.
- Digging dirt used whatever was held when no shovel existed (the live run used
  a hoe). `equipBestTool` now has a generic fallback tier
  (`requiredType`, plus "sekop" for shovels) that **refuses hoes**, warns when
  `UnequipItem` fails, and `mineSingle` logs the held tool at Debug
  (`mining block ... tool=...`) so the next run is decisive.
- `ToolTypeForBlock` (exported) is the block→tool mapping; grass_block counts
  as shovel work.

## Axe re-equip after detours
`equipBestAxe` ran once per `chopTree`; every detour (stock mining, tower
equips) left another tool in the hand. `chopLogBlock` now re-calls it after
`repositionForLog`, and `equipBestAxe` early-returns when the held slot already
holds the best axe (slots walked in index order, so the choice is stable).

## Tower placement refusals on an undecoded trunk column
The bot can stand ON a big tree's second trunk column that the local model
never decoded: every pillar placement into that column bounces
("the server never placed the block") while the server holds the body on top of
it. `ReconcileServerPinnedSupport` (bot package; the player package's
`ReconcileServerPinnedSupportNow` shares the core) marks the pinned cell
temp-solid immediately, and `TowerUpTo` retries the placement once after a
refusal. The echo path needs three agreeing corrections — a tower attempt gives
one — which is why reconciliation used to fire only after the tower gave up.
`NoteMissingSupport` no longer wipes its hit counter on in-flight corrections
(a tower's own jumps delayed the reconcile past the point it could help).

## Unreachable-log behaviour
`repositionForLog` returns whether the log is actually reachable; after a
failed/skipped tower it does NOT walk to the log any more (the pathfinder used
to climb the tree via its own leaves and log "A* gave up short" six times per
trunk). `chopLogBlock` defers those logs and re-checks them per round.

Related: [[bedrock-drop-direction]], skill `gophertunnel-geyser-movement-freeze`
(section 11, reset storms), skill `gophertunnel-geyser-drop-normal-transaction`.

## Update 2026-10-09 (second round): the bimodal lift window

Follow-up live run (tree base at y123, bot at 121, server pinning 123.06 on an
undecoded column): BOTH reconcile paths stayed silent, and the reason is
structural. During a Y-fight the body has exactly two quiescent levels — held
at the pin (body-lift ~0) and standing on the model's own ground (body-lift
~2) — and the interior of any body-lift window is only traversed MID-FALL,
where the in-flight skip discarded the echo. A body-lift gate misses
deterministically, not probabilistically.

Fixes:
- `ReconcileServerPinnedSupportNow` gates on **freshness** (ServerGroundAt
  <2s), frac, and gross **displacement** (pin−body in [−0.5, 2.3]) — no
  body-lift window. Fires on the first tower-refusal regardless of where the
  falling body is.
- Echo path: `missingSupportMaxLift` 1.6→**2.2** (the resting-on-model-ground
  echo, lift 2.06, is the strongest evidence and was discarded), and an
  **idle-falling carve-out** at the NoteMissingSupport call site (idle +
  VelY<−0.05 + no knockback counts; rising echoes and walk_to stay skipped so
  tower jumps can't re-arm the counter).
- `ChopTreeAt` now requires the feet within ±1 of the base Y after
  NavigateToBlock — a 3D-sphere tolerance used to accept standing 2 blocks
  below the base (dy 2 leaves horizontal ≤2.87 in a 3.5 sphere) and the chop
  ran from below, pillaring for a step a walk would have climbed.

Lesson: when gating a check on a body coordinate during a position fight,
gate on the STATIONARY quantity (model ground, server pin) — never on the
oscillating body.
