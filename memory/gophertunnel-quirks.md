---
name: gophertunnel-quirks
description: gophertunnel v1.62 gotchas that break the bot — resource packs, non-flushing writes, command wire format, the non-reentrant Bot mutex, and Geyser's unflagged MovePlayer echo
metadata:
  type: project
---

Five non-obvious failure modes in this bot, each found by a real disconnect or
hang rather than by reading docs.

**1. A required resource pack kills the login outright.**
`minecraft.Conn.handleResourcePackStack` refuses to continue unless every pack on
the stack is present, and it hardcodes `hasPack(uuid, version, false)` — so a
pack that *has* behaviour packs can never satisfy the check, even after a
successful download. Servers like play.nexusone.fun send one, and the bot died at
dial with `texture pack (UUID=…, version=…) not downloaded`.
Fix in `internal/connection/dialer.go`: `DownloadResourcePack` returns false, which
makes gophertunnel record the pack as *ignored* — the other thing `hasPack`
accepts. `server.resource_packs: skip|download` chooses.

**2. `WritePacket` only buffers; `Flush()` sends.** gophertunnel's `ReadPacket`
does not flush. A background ticker flushes at `Dialer.FlushRate` (default 50ms),
so ordinary writes do go out — but any code that writes from a one-shot path
should call `Flush()` like `SendSubChunkRequest` does.

**3. `CommandRequest` wire format is ambiguous and servers disagree.**
Geyser strips a leading `/` before translating to Java, so `/register` arrives as
an unknown command; dragonfly refuses anything *without* the slash. Handled by
`server.command_prefix: auto|slash|none` — `auto` sends the bare form, and if the
server says nothing within 1.2s retries with the slash. The retry hook is
`Bot.NoteServerReply`, which the **Text** chat handler also calls, because most
servers answer commands with `Text` rather than `CommandOutput`.

**4. `Bot.Mu` is not reentrant, and `sync.Mutex` is not either.**
`excludedPlayerIDsLocked` held `b.Mu` and called `b.FindPlayer`, which locks
`b.Mu` again — every `attack` action self-deadlocked the whole bot. Fixed by
exporting the pure `bot.PlayerNameMatches` for locked callers. When auditing this
codebase, grep for a `*Locked` helper that reaches for a locking `Bot` method.

**5. A Geyser front-end echoes your own position back as an unflagged
`MovePlayer`, and acking those echoes freezes the body.**
`handleMovePlayer` set `TeleportAckPending` for every self `MovePlayer`, and the
input loop's teleport branch deliberately skips `performActiveSteering()` and
`runPhysicsAndCollisions()` on the ack tick. A relay that echoes at tick rate
re-arms the flag between every tick, so no tick ever steered: the AI layer kept
computing routes (its path logs keep printing) while the body stood still with a
full set of walk inputs on the wire — the "can't move, but it looks like it is
running" report from `hansprojects.my.id`. Fixed in `network/player/move.go`:
`ShouldAckTeleport` acknowledges only a flagged teleport/reset or an unflagged
jump further than `teleportAckDistance` (2.2 blocks), which prevents normal
1-block drops (~1.41 blocks) and network latency from triggering false teleport
acks. Furthermore, `ApplySelfMovePlayer` only overwrites `b.Pos` when a real
teleport is acked or while the bot is `idle`, preventing delayed 20Hz echoes
from yanking the bot backward mid-stride during active walking. Note the ack
decision must be read against the **pre-update** `b.Pos`, because overwriting
it first makes every echo measure as zero distance.

**6. `chunk.StateToRuntimeID` panics on complex block properties.**
Dragonfly's `hashProperties` only accepts `bool`, `uint8`, `int32`, `string`.
Bedrock 1.21+ server palettes in `StartGame.Blocks` carry complex properties
such as `blockTags: []interface{}`, triggering `panic: invalid block property type`.
`safeStateToRuntimeID` in `internal/bot/world/cache.go` sanitizes property types,
recovers from panics, and falls back to `chunk.StateToRuntimeID(name, nil)`.

**Also:** `debuglog.Log` was called from the packet read loop and opened/wrote/
closed the file per call under a global mutex. On a laggy server `logReadGap`
fires per packet, so lag → disk I/O → lag → dropped client ("appears for a
second then leaves"). It now keeps the file open and rate-limits to 250ms.
