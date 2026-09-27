---
name: gophertunnel-pnx-invalidskin
description: Use when this Go bot is rejected with disconnectionScreen.invalidSkin by a server (especially Anvil-MC / PowerNukkitX hosts), when diagnosing login rejections that Geyser servers accept, or when touching skin/engine-version claims in internal/skin/builder.go.
---

# PNX invalidSkin — engine-version claim must be base64

## When to Use

- The bot is kicked with `disconnectionScreen.invalidSkin` right after dialing.
- A server accepts what Geyser front-ends accept but rejects the same login.
- Editing `internal/skin/builder.go` skin/ClientData fields.
- A target server runs Anvil-MC (phone host) — its Bedrock app is PowerNukkitX,
  whose login validation is much stricter than Geyser or a vanilla host client.

## When NOT to Use

- Protocol mismatch (`OUTDATED_CLIENT/SERVER`) — check `cmd/lanprobe` ping first.
- `notAuthenticated` / `notAllowed` — that is xbox-auth / whitelist, not skin.
- Skin looks wrong after spawn — that is the PlayerSkin packet path, not login.

## Root cause (found 2026-09-28)

PNX `ClientSkinData.readSkin` base64-**decodes** the claim
`SkinGeometryDataEngineVersion` with the standard decoder. The bot sent the
plain string `"1.12.0"`; `Base64.getDecoder().decode("1.12.0")` throws
(illegal character `.`), the catch-all returns null skin, and `completeLogin`
kicks `INVALID_PLATFORM_SKIN` → `disconnectionScreen.invalidSkin`.

Because every skinprobe variant preserved this field, 11 variants failed
identically — the failure happened before the skin image was ever read.

Geyser and vanilla host clients treat the field as an opaque string, which is
why those servers never rejected it. Real clients send base64 (`MS4xMi4w`), so
base64 is the correct, universally accepted form. Fix: `builder.go` uses
`b64([]byte("1.12.0"))` for `login.ClientData.SkinGeometryVersion` (the
`protocol.Skin.GeometryDataEngineVersion` field stays raw bytes — that is the
post-spawn PlayerSkin path and is encoded by gophertunnel).

## PNX login chain (what it validates, in order)

1. Login serializer maps JSON `AuthenticationType` byte n → `values()[n+1]`:
   0=FULL, 1=GUEST, 2=SELF_SIGNED. gophertunnel offline (EncodeOffline) sends 2
   → SELF_SIGNED → lenient OFFLINE_CONSUMER (requires an `exp` claim only).
2. Identity claims for the token path read the public key from the **`cpk`**
   claim (also `xid`, `xname`, `mid`) — gophertunnel's tokenClaims matches.
3. Client JWT must verify (ES384) against that cpk — same key signs both, OK.
4. `ClientChainData.from` requires EVERY listed claim with an exact Java type;
   enum claims go through `from()` which throws out-of-range → null → kick:
   InputMode 0-3, GraphicsMode 0-3, MemoryTier 0-4, PlatformType 0-2,
   UIProfile 0-2, BuildPlatform (never throws), `ClientRandomId` must be Long,
   `ClientEditorConnectionIntent` must exist.
5. `ClientSkinData.readSkin`: strict types + base64 decodes on SkinData, Cape,
   GeometryData, GeometryDataEngineVersion, ResourcePatch; any exception → null.
6. `SkinUtils.isValid`: SkinId non-empty <100, image ≥32×32 and ≥16384 bytes,
   `SkinAnimationData` <1000, resource patch JSON needs `geometry.default`.

PNX source: `LoginHandler`, `ClientChainData`, `ClientSkinData`, `SkinUtils`
(raw.githubusercontent.com/PowerNukkitX/PowerNukkitX/beta/...), protocol fork:
`Kaooot/Protocol` branch 3.0 (PNX uses `org.powernukkitx.protocol` Beta7).

## Procedure

1. `go run ./cmd/pnxcheck` — replays the bot's exact login payload through a
   local Go replica of the PNX chain and prints the first failing check.
2. Fix the flagged field in `internal/skin/builder.go` (or device fields in
   `internal/connection/client_data.go`), rerun until all checks pass.
3. `go build ./... && go test ./...`.
4. Live: `go run ./cmd/lanprobe -w 5s -host <server>` first (is the server up?),
   then run the bot. Note: the 1.19 Anvil-MC server is NOT a LAN world; LAN
   discovery failing there and falling back to RakNet is normal.

## Pitfalls

- Do not bisect skin IMAGE fields when variants fail identically — the failing
  check is probably earlier in the chain (claims/enums/signature).
- jose4j maps integer JSON numbers to Java Long; a claim that marshals as a
  float breaks `instanceof Long` checks (ClientRandomId).
- Anvil-MC "Bedrock server" ≠ vanilla BDS. Always fetch the actual PNX sources
  for the version the host runs instead of guessing from vanilla behavior.
- Geyser accepting a login proves nothing about PNX; the two stacks differ.

## Verification

- `go run ./cmd/pnxcheck` exits 0 with every check PASS.
- Bot login to a PNX server is accepted (no invalidSkin) — pending live test
  while the user's server is down; re-run when it is back up.
