# Gophertunnel - Minecraft Bedrock AI Bot System

> **Library**: [github.com/sandertv/gophertunnel](https://github.com/Sandertv/gophertunnel)  
> **Language**: Go 1.24+  
> **License**: MIT  
> **Stars**: 507 | **Forks**: 149  

---

## Apa Itu Gophertunnel?

Gophertunnel adalah **Swiss Army Knife** untuk Minecraft Bedrock Edition software dalam Go. Library ini memungkinkan pembuatan tools terkait Minecraft seperti:

- **Client Bot** - Connect ke server sebagai player
- **Server** - Buat server Minecraft Bedrock sederhana
- **MITM Proxy** - Intercept dan modify traffic antara client dan server

---

## Package Structure

```
github.com/sandertv/gophertunnel/
├── minecraft/           # Core Minecraft protocol
│   ├── dialer.go        # Client connection (Dialer)
│   ├── listener.go      # Server listening (ListenConfig)
│   ├── conn.go          # Connection handling (Conn)
│   ├── auth/            # Microsoft/Xbox Live authentication
│   └── protocol/packet/ # All packet types
└── query/               # Server query protocol
```

---

## Quick Reference

### Dialer (Client Connection)

```go
dialer := minecraft.Dialer{
    TokenSource: auth.TokenSource,
}
conn, _ := dialer.Dial("raknet", "server:19132")
conn.DoSpawn()
```

### ListenConfig (Server)

```go
listener, _ := minecraft.ListenConfig{
    StatusProvider: minecraft.NewStatusProvider("Server Name", "Description"),
}.Listen("raknet", ":19132")

conn := listener.Accept().(*minecraft.Conn)
conn.StartGame(minecraft.GameData{})
```

### Packet Handling

```go
for {
    pk, _ := conn.ReadPacket()
    switch p := pk.(type) {
    case *packet.Text:
        fmt.Println(p.Message)
    case *packet.MovePlayer:
        fmt.Println(p.Position)
    }
}
```

---

## Key Packet Types

| Packet | Purpose |
|--------|---------|
| `packet.Text` | Chat messages |
| `packet.MovePlayer` | Player position/rotation |
| `packet.Emote` | Emote animations |
| `packet.InventoryTransaction` | Block interactions, transactions |
| `packet.RequestChunkRadius` | Chunk loading radius |
| `packet.CommandRequest` | Commands execution |

---

## Authentication

```go
// TokenSource automatically handles Microsoft OAuth
// First run: opens browser for Xbox Live login
// Tokens cached for subsequent runs
TokenSource := auth.TokenSource
```

---

## Use Cases in This Project

### 1. NPC AI Bot
Bot yang spawn di dunia, listen events, dan respond ke players menggunakan AI processing.

### 2. Admin Tool
Remote bot yang bisa execute commands di server tanpa player account.

### 3. Event Monitor
Capture dan log semua game events untuk analytics.

### 4. MITM Proxy
Debugging tool untuk lihat raw packet flow.

---

## Important Notes

1. **Go 1.24+ Required** - Gophertunnel requires Go 1.24.0 or later
2. **Latest Minecraft Only** - Supports only the latest Minecraft version
3. **Raknet Protocol** - Uses Raknet for Bedrock UDP connections (port 19132 default)
4. **MIT License** - Fully open source, can be used commercially

---

## Links

- [GitHub Repository](https://github.com/Sandertv/gophertunnel)
- [Documentation](https://sandertv-gophertunnel.mintlify.app/)
- [Examples](https://mintlify.com/Sandertv/gophertunnel/examples/overview)

---

*Documented: 2026-04-08*
