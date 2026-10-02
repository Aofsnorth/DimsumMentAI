# Affordances: letting the bot do what a player can do

**Status:** layer implemented, catalogue at 54 verbs, safety gate enforced.
**Not yet:** live verification of the last four changes (see Verification below).

This is the plan for the second half of Candidate B. The layer itself is
described first because the plan only makes sense against it.

---

## 1. What the layer is

The bot can do 169 things. It could previously choose among about nine. That
gap is why it reads as rigid: a model offered "gather or rest" cannot express
"gather, but put a block between me and the creeper first", because that answer
has no vocabulary to live in.

`internal/bot/affordance/` closes the gap in four parts.

### 1.1 A catalogue that says what each verb can prove

Every entry carries an effect described in plain language and a
**confirmation class**:

| Class | Meaning | Example |
|---|---|---|
| `Confirmed` | waited for a server-observed change | `place` — the block appears, or it did not happen |
| `Assumed` | reported after writing a packet | `attack` — swing sent, no damage observed |
| `Unverifiable` | nothing observable exists | `rest`, `wave` — the world did not move |

The distinction is the whole safety story. `docs/FEATURE_GAP_ROADMAP.md` names
"optimistic success reporting" as this project's number-one gap, and the audit
found 64 confirmed against 59 assumed and 46 unverifiable. Half the registry
reports success it has not checked.

### 1.2 A gate: a world-changing verb must be confirmed

> An unverifiable verb may be offered for a self-changing intent, and never for
> a world-changing one.

`Verb.Offerable()` enforces it, and the test walks the **entire catalogue**
rather than a few cases — so a catalogue that grows later still satisfies it.

This is not a stylistic preference. A bot told it placed a block it did not
place will stand in the blast next time, confident it is protected.

### 1.3 Derivation from the world, not from a list

`Derive(world, changesWorld)` narrows the catalogue by what is actually
possible: no axe means no `gather`, no block in hand means no `place`, no water
means no `fish`. Every refusal carries a reason that reads in a log
("not carrying a block to place"), so a bot that declines to act is
explainable rather than mysterious.

### 1.4 Two vocabularies, kept apart

`Activity` (the brain does it: rest, wander, gesture) and `Action` (a registry
label). They overlap — `explore`, `look` and `attack` exist as both — and
conflating them produced five verbs the registry cannot resolve. `Action` is
the zero value deliberately: an entry that forgets its `Kind` fails the drift
check **by name**, which is better than failing quietly.

---

## 2. Plan to full player parity

### Stage 1 — expand the catalogue (54 → every confirmed verb)

**Done for the current surface.** The catalogue covers 27 confirmed
world-changing verbs and 26 unverifiable self-changing ones.

**Next:** the remaining confirmed behaviours from the audit — container
interactions the `interact` family performs through the chat-only channel,
husbandry verbs that confirm by inventory delta (`milk`, `breed`), and the
farming verbs (`plant`, `hoe`, `harvest`) which are already in but whose
prerequisites are not yet derived from the snapshot.

**Rule for every addition:** the entry must name a real registry label, and
`Check()` fails the build-by-test otherwise. `SupportedLabels()` now reads the
live registry rather than a hand-kept list, which is what stopped the drift the
old list had accumulated silently.

### Stage 2 — DONE: derive prerequisites from the snapshot

Shipped. `craft` now reads `Craftable` from the snapshot, which it never did: the
rule only ever asked whether there was room, so a bot with nothing to make was
offered `craft` every tick and the handler failed on it. Two live bugs surfaced
while doing it, both invisible to the affordance unit tests because those build
their own `World`:

- `craftable` was declared on the adapter and never assigned, which made `craft`
  permanently illegal in a live run.
- `freeSlots` counted empty *entries in the inventory map* rather than empty
  slots in a 36-slot inventory. A map only holds occupied slots, so every bot
  carrying anything reported zero free slots — which refused `craft` and `give`
  outright. The snapshot's own `freeInventorySlots` already walked all 36 and was
  right; the two now agree.

`water` and `container` stayed on the reach scan deliberately. `NearBlocks` is a
sight range and `affordanceReach` is an interaction range, and the difference is
the whole point of the existing rule: a chest across the room is visible and
unopenable. Both questions are now answered in one sweep of the same 486 cells
instead of two.

### Stage 3 — DONE: the verb carries an argument

Shipped. `take` is offered as `take:oak_log`; `SplitVerb` keeps the gate, the
catalogue and the drift check speaking in bare verb names. A parameter the world
cannot supply is never offered, because the handler reads it as an instruction
and the model reads it as a choice.

### Stage 4 — DONE: the interact family reports back

Shipped. `ReportActionStatus` was chat-only, so eight labels told the player what
happened and told the planner nothing — every step sat out its ninety-second
timeout and failed on work that had worked. `PublishActionStatusFunc` closes it,
and a headless bot with no `AiClient` now publishes too.

### Stage 2 — derive prerequisites from the snapshot, not the world scan

Today `legal()` asks the world: is there water, is there a chest, is there a
tool in hand. Several of those are already in `Snapshot` and are cheaper and
more accurate there (`Features.Water`, `FreeSlots`, `Craftable`).

**Move them**, so a verb is offered because the bot's own perception says so,
and a perception gap shows up as a missing verb rather than as a failed action.

### Stage 3 — let the verb carry an argument

The catalogue currently answers *what*, not *with what*. `place` takes no
target; `gather` takes no count; `take` takes no item.

**Add a parameter** derived from the situation: what is in the selected slot,
what is in the chest being looked at, how many free slots remain. The action
registry already accepts a parameter string on every handler, so this is a
rendering change rather than a new dispatch.

Without it the model can still only pick a verb, not a target — which is the
remaining shape of the original rigidity.

### Stage 4 — close the interaction channel

The audit found the eight-label `interact` family reports through
`ReportActionStatus`, which is the **chat-only** path. `ExecuteAndWait`
subscribes to `publishStatus` and never sees it, so those verbs always time the
step out at ninety seconds even when they work.

**Fix the channel first, then offer the verbs.** Offering a verb whose verdict
cannot reach the planner is worse than not offering it.

### Stage 5 — composition

The point of the layer is that the model can say "do A, then B". The natural
next step is a small grammar over the confirmed verbs — sequences of two or
three, with the confirmation of the first gating the second.

**Not yet, and deliberately so:** free-form chaining over unconfirmed verbs.
That is Candidate C, which the deep-thinking pass rejected on the grounds that
a post-hoc gate reports a broken world rather than preventing it.

---

## 3. The personality axis

The complaint that started this was that a creeper produces the same panic every
time. A switch on mob name is rigid by construction: the decision is a lookup
rather than anybody's opinion.

`QRisk` (`careful` / `bold` / `reckless`) is asked on every tick and installed on
the bot, so the movement layer and the combat layer read the same disposition.
It is deliberately asked on **busy** ticks too — a bot gathering wood when a
creeper walks up is not a bot that needs asking what to do next, it is a bot
whose sense of what is reasonable has just changed.

`CreeperPlan` moves the distances with the disposition, and
`TestTheThreeTemperamentsReactDifferentlyToACreeper` fails if they ever become
equal again. That is the definition of "a switch is not a person".

`attack` is deliberately **not** an affordance: `handleAttack` reports at
dispatch, so by this layer's own rule it could never be offered — which would
leave the bot unable to fight at all. Combat keeps ownership until it can prove
a kill.

---

## 4. Verification

### Proven

- `go build ./...`, `go vet ./...`, `go run ./cmd/archcheck` (Findings: 0)
- 62 test packages green
- The gate invariant is walked over the whole catalogue
- The drift check fails by name on an unregistered label
- The cliff gate is proven **load-bearing**: with the gate disabled,
  `TestTheBodyRefusesToWalkOffACliff` fails, and its harness reaches the real
  steering step rather than returning early
- Live, before the last four changes: 0 fatal, 0 deadlock, 0 gather failures,
  78 narration suppressions against 0 chat replies, and `input_tokens` rising
  from 806 to ~2041 — direct evidence the new questions are sent and answered

### Not proven

The last four changes have **not** been run in-game: the Minecraft server at
`192.168.1.18:7551` stopped listening part-way through. Unverified live:

- the affordance answer being dispatched
- `QRisk` arriving on busy ticks
- the corrected drop gate refusing a real cliff
- the corrected watchdog on a real session

Each is unit-tested with a fails-before proof except the last, which is
behaviour whose falsifier is a live server rather than a line of code.

### Known, unexercised

The ledge gate has never fired in a live run — no cliff was ever approached.
It now logs at INFO, so the first encounter will be visible rather than silent.
