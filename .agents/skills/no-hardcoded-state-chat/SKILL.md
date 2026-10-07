---
name: no-hardcoded-state-chat
description: Use when adding or changing a chat line the bot sends after it changes its own state (stop following, stop attacking, episode start/stop), or when a reply in this repo is a bare string literal instead of something the model wrote. Triggers on "hardcoded reply", "hardcoded chat", "Siap, aku", "jangan di hardcoded", "AI decided reply".
---

# No Hardcoded State Replies

## When to Use

Use when a code path performs a state change and then calls `SendSafeChat` with a
literal. The reply about a state change belongs to the model; the state change
itself belongs to the code.

## When NOT to Use

- Usage/format errors (`Formatnya: /episode <nomor> ...`). Those state a command
  syntax the operator must copy exactly. Rewriting them risks a wrong syntax.
- `/say` passthrough (`handleAdminSay`) — that is the operator's own words.
- Data reports built from inventory (`handleListCraftable`).
- Anything the model already wrote: `parsed.CleanReply`, AGI `announce`, status
  narration, proactive replies.

## Root Cause

The literal was never just a style problem. It is the same sentence for every
player, every time, and because it is a string there is nothing in the reply path
that can notice the bot stopped following for a different reason, or not at all.
The state change and the sentence about it were welded together.

## Procedure

1. Keep the state change in code. It must stay deterministic — `b.Stop()`,
   `b.CombatMgr.Disengage()`, `b.BeginEpisodeFunc`. Never gate it on the model.
2. Replace the literal with `replyViaAI(b, user, playerMsg, change)` in
   `internal/bot/chat/state_reply.go`.
3. `change` is a factual English sentence saying what changed. Facts the player
   needs (episode number, objective, budget) go in it verbatim, not paraphrased.
   `playerMsg` is the player's own words so the model can match their tone.
4. `stateReplyText` parses the model's reply and sends `CleanReply` only, so a
   stray `<action>` tag cannot become a task. It drops the reply on narration
   leak, self-correction, script corruption, and `<silent/>`.
5. When the model is absent, fails, or declines, nothing is sent. That is
   deliberate — the change is already visible in the world, and a canned
   reassurance the bot did not mean is worse than silence.

## Pitfalls

- Do not make the state change depend on the model replying. The model is not a
  precondition for stopping following.
- Do not execute `parsed.Actions` on this path. `stateReplyText` returns text.
- `replyViaAI` runs the LLM call synchronously on the chat goroutine, which
  already runs detached (`go HandleIncomingChat`). Do not add a blocking wait.
- `buildChatContext` reads `b.WorldModel`, `b.ConfiguredName`, `b.TagAliases`,
  `b.Planner`, `b.AutonomyContextFunc`. It tolerates nils; a test bot needs
  `Logger`, `RecentBotMessages` and `RecentStatusReports` set.
- A nil `AiClient` must return `""`, not panic, and not fall back to a literal.

## Verification

`go build ./... ; go test -count=1 ./tests/...` — empty grep output means pass.

`tests/bot/chat/state_reply_test.go` asserts the model is consulted, that the
prompt carries both the change and the player's words, that `<silent/>` and a
missing client send nothing, and that an action tag never reaches the player.

A grep for `SendSafeChat("` must return only the episode format-error line.
