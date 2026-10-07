---
name: gophertunnel-whisper-reply
description: Use when this Go Bedrock bot ignores a private message ("/msg", "/tell", "/dm") sent by its linked player, or when a reply to a DM has to go back privately instead of appearing in public chat. Covers the relayed-whisper banner on Geyser servers, why the reply transport is the /tell command rather than a protocol packet, and the privacy leak that a public reply causes.
---

# Gophertunnel Whisper Reply

## When to Use

Use when the bot is silent in response to a DM from its linked player, when the
log shows a system chat line that names both participants, or when a reply that
belongs in a DM shows up in public chat.

## When NOT to Use

Do not use for ordinary public chat the bot ignored (`respond_only_to_linked_player`,
`respond_only_when_tagged`), for a whisper between two other players, or for a
server advertisement relayed through the same text channel.

## The shape of the problem

A whisper arrives as `text_type=6` (system) with an **empty source name**, and
the whole conversation is written into the body:

```
✉⬇ MSG (Synterix ➺ .ShigaLucifer) Halo, kamu pakai model apa?
```

Because the source is empty, `shouldProcessChat` rejects it as
`chat ignored: not linked main player` even though the sender is right there in
the message. The bot looks like it ignored a DM it received correctly.

## Procedure

1. **Recognise the banner before generic sender extraction.** Run
   `ExtractWhisper` in `internal/handler/chat.go` *before*
   `ExtractChatSenderAndMessage`. The generic extractor reads `➺` as a chat
   separator and produces a sender literally named `✉⬇ MSG (Synterix`.
2. **Require a banner label.** A leading label from `MSG/MESSAGE/DM/PRIVATE/
   WHISPER/TELL/CHAT/W`, a `(` within 24 runes, and a space after `)`. This is
   what keeps ordinary prose such as `Halo (Synterix ➺ Papua) Apa kabar?` from
   being read as a whisper.
3. **Check the recipient is this bot.** The same banner relays whispers between
   other players, so answering one would put the bot in a conversation it was
   never part of. `isBotRecipient` → `Bot.IsBotName`.
4. **Carry the whisper flag.** `event.ChatEvent.Whisper` marks the line so
   `shouldProcessChat` can skip `RespondOnlyWhenTagged`: a player who already
   sent a DM does not also have to write the bot's name inside it.
5. **Record the conversation before any filter.** `NoteWhisperFrom` runs right
   after `LastChatPartner` and ahead of the filters, because a message that was
   filtered out still proves the channel is private. A public message from the
   same player closes it (`ClearWhisperWith`).
6. **Route every reply through `SendReply`, never `SendSafeChat`.** `SendReply`
   whispers when the conversation is open and the player matches, and sends
   publicly otherwise.
7. **Send with the `/tell` command**, via `writeCommandLine`, bypassing the
   auto-detect retry. The server answers a whisper with silence (the reply goes
   to the recipient, not the sender), so waiting for an acknowledgement would
   cost a second per message and then send the same line twice.

## Prerequisite: the bot must know the name the server gives it

A Bedrock account is frequently greeted under a name that is not the configured
one — Floodgate/SimpleLogin hand back something like `.ShigaLucifer` for an
account configured as `Luna`. The `PlayerList` entry that carries that name is
matched by UUID and entity ID (`IsSelfEntry`), so a server that sends neither
leaves `b.Name` still saying `Luna`.

Symptom in the log, and it is easy to misread as working:

```
chat event received source=.ShigaLucifer message="Halo Arthenyxx! ..." bot_name=Luna
chat ignored: bot echo detected
```

The echo is caught, but only because the message *text* matched a recent bot
message. Compare the source against `b.Name` and it does not match at all.

This breaks whisper handling before any of the banner work matters:
`isBotRecipient` → `IsBotName` returns false for `.ShigaLucifer`, so the DM is
treated as a whisper between two other players and dropped.

Fix: learn the name from the echo itself, in `shouldProcessChat`, before the
echo is discarded — the server stamping its name on a line the bot is known to
have just sent is the one reliable signal available. Store it as
`serverAssignedName` and consult it in both `IsBotName` and `IsOwnChatSource`.
Do **not** overwrite `b.Name` or `b.ConfiguredName`; the persona and the tag
matcher still use the configured name.

## Common Pitfalls

- A public reply to a DM is not a style problem. The server relays whispers to
  its other clients as visible chat, so a public answer puts the content of a
  private conversation in front of the whole server. A player who discovers that
  once stops using whispers, and it cannot be undone.
- Split whispers at **120 runes**, not public chat's 220. A command line is
  capped at 256 bytes and the server counts the name and the wrapper against
  that.
- Match the bot name on both `Name` and `ConfiguredName`, drop a leading dot on
  both sides, case-insensitively. The server's name for the bot is whatever
  `PlayerList` returned — often `.ShigaLucifer`, not the configured `Luna` — so
  a single-field comparison silently drops every DM.
- The whisper window is **3 minutes**. Long enough for a follow-up exchange
  given the model's latency, short enough that a public message much later is
  not swallowed into a DM that ended.
- Do not let a stranger's message close somebody else's private conversation.
- Guard `b.Logger` calls; a nil `slog.Logger` panics on any `b.Logger.Info`.

## Verification

```sh
go build ./... ; go test -count=1 ./tests/... 2>&1 | grep -E "^(FAIL|---|panic)|\.go:[0-9]+"
go vet ./internal/... ./tests/...
```

Empty output means pass. `tests/bot/whisper_test.go` and
`tests/handler/whisper_test.go` cover the routing; neither needs a connection.

Then have the operator DM the bot **without** naming it. Expect in the log:

```
chat packet received source=Synterix message=Halo, kamu pakai model apa?
private conversation opened with=Synterix
whisper sent to=Synterix message=...
```

and the reply must arrive in the player's DM, not public chat. If `whisper sent`
is absent, `SendWhisper` returned an error and the fallback logged
`whisper reply failed, falling back to public chat` — chase whether the server
accepted `/tell`.

Passing unit tests does not prove the server accepts `/tell`. Only an operator's
log does.