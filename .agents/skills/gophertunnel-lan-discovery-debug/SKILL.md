---
name: gophertunnel-lan-discovery-debug
description: Debug Bedrock single-player LAN joins in this Go bot when NetherNet discovery finds no world, fails to log in, or the world is opened to LAN but the bot still falls back to RakNet.
---

# Gophertunnel LAN Discovery Debug

## When to Use

Use when the bot targets a Bedrock single-player world opened with **Visible to LAN Players** and one of these happens: discovery times out with "no compatible NetherNet LAN world discovered", a world is discovered but login/handshake fails, or `server.port` is `7551` and the bot drops back to RakNet.

## When NOT to Use

Do not use for dedicated servers (`19132`/`19133`) that speak RakNet, for dragonfly/hosted servers, or for in-game action and crafting bugs. Those are covered by `gophertunnel-crafting-debug` and `gophertunnel-placement-debug`.

## Key Facts

- Bedrock LAN is **NetherNet (WebRTC signalling)**, not RakNet. Only `github.com/df-mc/go-nethernet/discovery` finds it. Raw UDP/RakNet pings to `7551` always return nothing and prove nothing.
- **The client must send back the host's `Nonce`.** The host publishes it in the advertisement and silently drops the SCTP association if the `Login` ClientData omits it. Symptom: ICE, DTLS and SCTP all start, then the connection closes ~30 ms later with `use of closed network connection`.
- **`invalidSkin` on a client-hosted world means wrong connection path, not a bad skin.** With `lan_discovery: false` the bot dials the world directly over RakNet; the host requires the advertisement `Nonce` echoed in `ClientData`, and reports the missing nonce as `disconnectionScreen.invalidSkin`. Verified 2026-09-27: every login variant (offline OIDC, online Mojang token, legacy, all skin/geometry/trust-flag permutations) was rejected identically over direct RakNet while the same build joined the same world fine over NetherNet earlier the same day. When a direct-RakNet login to a single-player world fails with `invalidSkin`, stop bisecting skin fields and restore `lan_discovery: true`.
- `discovery.ServerData` has a wire `version` byte and its layout changed completely at version 7: version 4 used fixed-width fields plus `TransportLayer`, version 7 uses varints with `Protocol`, `Version`, `Nonce` and `AcceptsOnlineAuth`, and has **no** `TransportLayer`. An outdated `go-nethernet` silently fails to decode current worlds.
- **`gophertunnel` must match the host's protocol version.** Old versions are rejected with `client outdated`. `go-nethernet` alone is not enough.
- The wire format of `RequestPacket` is stable, so an empty discovery scan is never a packet-format problem.
- Minecraft owns UDP `7551` while the game runs. The discovery listener **must** use an ephemeral `:0` port.
- `discovery.Listener` broadcasts a `RequestPacket` on a 2s ticker, so a 5s window only yields ~2 attempts.
- The `Listener` must stay open for the whole session: `Listener.Signal` resolves peers through an address map that is dropped after 15s of inactivity.
- Discovery and WebRTC negotiation need separate time budgets. A 5s discovery window is fine, but ICE/DTLS/SCTP needs ~60s.

## Procedure

1. Confirm the LAN path, not the bot. Get the game's socket: `Get-NetUDPEndpoint -LocalPort 7551`. A bound socket does **not** mean the world is advertised.
2. Prove local broadcast works before blaming the bot. Bind a receiver on the LAN IP with an ephemeral port, send a datagram to `192.168.1.255` and `255.255.255.255` from a second socket, and confirm the receiver gets it. Windows does loop self-originated broadcast back to local sockets.
3. Decisive probe: `go run ./cmd/lanprobe -host <host-ip>`. It unicasts a real `RequestPacket` to `127.0.0.1:7551` and `<host-ip>:7551`, and pings RakNet unicast `<host-ip>:19132` plus subnet broadcast. If an open world does not answer discovery on unicast while RakNet unicast answers, the world is open but not NetherNet-advertised (old build, or closed between runs — RakNet ping answering earlier and timing out later means the game was closed in between). If nothing answers anywhere, the world is simply not opened to LAN. Stop; no code change helps.
4. Only after step 3 succeeds, suspect the code. `lanDiscoveryEndpoint` picks the interface whose exact IP matches `server.host` first (pinned local address), then the interface sharing the host's subnet (using the interface's real mask, so the broadcast reaches the host's `/24` instead of `255.255.255.255`, which routers swallow), then the first usable interface. A remote host IP therefore lands on the subnet match — if the broadcast target in the debug log is not the host's subnet broadcast, investigate that first.
5. Keep login address and transport address separate. `minecraft.Dialer.DialContextNetwork(ctx, network, address)` copies `address` into `ClientData.ServerAddress`, so the bare discovered numeric network ID yields an invalid login. Wrap the network and swap the address only for the transport:

```go
type lanNetwork struct {
    minecraft.Network
    NetworkID string
}

func (n lanNetwork) DialContext(ctx context.Context, _ string) (net.Conn, error) {
    return n.Network.DialContext(ctx, n.NetworkID)
}
```

6. Copy the dialer and set `ClientData.Nonce` from the advertisement before dialling. This is mandatory for current game versions.
7. Pass the discovered `listener` as `minecraft.NetherNet{Signaling: listener}` and close it only after `conn.Context()` is done.
8. Set `nethernet.Dialer{AllowIdentitylessServer: cfg.Offline}` so offline LAN worlds accept an identityless signalling peer.
9. Keep the RakNet fallback. Discovery runs only when `LANDiscoveryEnabled()` is true (port `7551`, or an explicit `lan_discovery` value) and `BOT_DIAL_ADDR` is unset.
10. Build and test with `go build -buildvcs=false ./cmd/bot` and `go test ./internal/connection ./internal/config`.

## Common Pitfalls

- Never bind the discovery listener to `:7551`; the game already holds it and `Listen` fails.
- Never use `server.ServerName` as a host IP. It is the player's name, not an address.
- Never pass the raw network ID as the dialer address. Login validates `ServerAddress` as a URL or UDP-style address.
- Never filter on `ServerData.TransportLayer` on `go-nethernet` v1.0.18+. That field was removed in ServerData version 7; filter on `ConnectionType` and `EditorWorld` instead.
- Do not conclude "the world is not open" from an empty scan until you have checked the `ServerData` wire version the installed `go-nethernet` expects.
- `minecraft.NetherNet` does **not** implement `DialContextIdentity`, so wrapping `minecraft.Network` does not silently break online auth. The returned `*nethernet.Conn` is passed through untouched, so optional packet-transport methods survive.
- Binding `:0` (IPv6 wildcard) does not reliably receive IPv4 broadcast replies on Windows. Bind the specific LAN IPv4 and broadcast the computed subnet address.
- `net.Dial` and `net.DialUDP` return a **connected** socket, so `WriteTo` at another destination is silently dropped and a probe looks like a total network failure. Use `net.ListenPacket` for the sender in any loopback/broadcast probe.
- A missing broadcast response and a missing unicast response mean different things. Only the unicast result is conclusive.
- When `dialLAN` reports `saw N incompatible advertisement(s): ...`, the scan DID hear the world but skipped it — the listed reason (usually `world name does not match lan_world`) is the fix, not discovery.
- `go build ./cmd/bot` fails with "dubious ownership" on this filesystem. Add `-buildvcs=false`. Git commands need `-c safe.directory=D:/Work/Projects/MyProject/DimsumMentAI`.
- `gofmt -l` also lists pre-existing unformatted files (`client_data.go`, `device_identity.go`, `loader.go`). Do not reformat them inside a feature change.
- Do not run `go mod tidy` here; the tree carries unrelated dependency work.
- **Do not tune look angles to fix a body that moves.** A bobbing/tremoring head on a
  freshly joined LAN world is almost always a physics bug, not a look bug. Check
  `posY` in `logs/debug-090ce4.log` first: a stationary bot must not change Y. See
  `gophertunnel-vertical-anchor` for the full diagnosis.

## Verification

- `go test ./internal/connection ./internal/config` passes, and the discovery/network-ID tests pass with `-count=10`.
- `go vet ./internal/connection ./internal/config ./cmd/bot` is clean.
- The bot logs the chosen `listen` and `broadcast` endpoints at debug level, so a failed scan names the exact endpoint pair.
- Live proof requires an actually opened world: discovery returns a `ResponsePacket`, `ServerData` shows the expected `LevelName`, and the bot appears in-game without touching the RakNet fallback.
