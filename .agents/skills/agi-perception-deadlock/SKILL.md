---
name: agi-perception-deadlock
description: Debug AGI enabled but motionless, especially when a nearby player makes Observe block before Jev or naturalTick logs.
---

# When to use
AGI starts but stops producing decisions, particularly with nearby players.

# When NOT to use
Decisions and routes complete normally but the server rejects motion; investigate movement packets instead.

# Procedure
1. Run `go test ./tests/agi -run TestObserve -count=1 -timeout 15s`.
2. Reproduce with a populated PlayerEntityIDs and PlayerPositions map, not an empty world alone.
3. Trace lock ownership through Observe, nearbyPeople, SeesPoint, InFieldOfView, GetCoords, and HeadYaw.
4. Copy mutable player state under b.Mu, release it, then call perception helpers. They acquire b.Mu themselves.
5. Retain distance, line-of-sight, field-of-view, chat-partner and greeting semantics.
6. Add a bounded regression asserting Observe returns and releases the mutex with a nearby player.

# Pitfalls
- sync.Mutex is non-reentrant. Moving runner greeting checks outside the lock does not fix visibility checks still inside it.
- Empty-player tests skip the failing branch and give false confidence.
- Do not disable terrain readiness or visibility to conceal a deadlock.
- Passing offline tests does not prove motion on a live LAN host.

# Verification
Run `go test ./... -count=1 -timeout 90s`, `go build ./...`, `go vet ./...`, and `go run ./cmd/archcheck`. Verify live that Jev/naturalTick decisions resume with a player nearby.
