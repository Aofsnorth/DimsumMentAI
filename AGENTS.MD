# AGENTS.MD — gophertunnel

> Panduan referensi untuk AI agent yang bekerja di dalam atau dengan codebase **gophertunnel**.

---

## Apa itu gophertunnel?

**gophertunnel** (`github.com/sandertv/gophertunnel`) adalah library Go serbaguna untuk membangun perangkat lunak yang berhubungan dengan **Minecraft Bedrock Edition**. Library ini sering disebut sebagai "swiss army knife" untuk ekosistem Minecraft Bedrock di Go.

- **Maintainer:** Sandertv (Sander Dankbaar)
- **Bahasa:** Go (minimal Go 1.24 sejak v1.47.5)
- **Protokol saat ini:** Protocol `924`, Minecraft `1.26.0`
- **Import path:** `github.com/sandertv/gophertunnel`
- **Transport utama:** RakNet (`github.com/sandertv/go-raknet`)

---

## Struktur Package

```
gophertunnel/
├── minecraft/                  # Package utama: koneksi, listener, dialer
│   ├── auth/                   # Autentikasi Xbox Live / Microsoft OAuth2
│   ├── nbt/                    # Encode/decode Named Binary Tag (NBT)
│   ├── protocol/               # Struktur data protokol Bedrock
│   │   ├── packet/             # Definisi semua paket jaringan
│   │   └── login/              # Data login: IdentityData, ClientData
│   └── resource/               # Resource pack handling
└── main.go                     # Contoh implementasi MITM proxy
```

---

## Package-by-Package

### `minecraft` (core)

Package utama. Menyediakan abstraksi tingkat tinggi atas koneksi Minecraft Bedrock.

**Tipe kunci:**

| Tipe | Deskripsi |
|------|-----------|
| `Conn` | Representasi koneksi Minecraft (Bedrock Edition). Thread-safe kecuali `ReadPacket` yang tidak boleh dipanggil dari beberapa goroutine secara bersamaan. |
| `Dialer` | Konfigurasi untuk membuka koneksi ke server Minecraft. |
| `ListenConfig` | Konfigurasi untuk membuat listener yang menerima koneksi masuk. |
| `Listener` | Listener aktif yang menerima koneksi client. |
| `GameData` | Wrapper data dari paket `StartGame` (posisi spawn, game mode, dll.). |

**Fungsi & Method penting:**

```go
// Dial ke server (client mode)
conn, err := minecraft.Dial("raknet", "server.address:19132")

// Atau dengan konfigurasi custom (misal: dengan auth)
dialer := minecraft.Dialer{TokenSource: src}
conn, err := dialer.Dial("raknet", address)

// Spawn di dunia (blocking)
err = conn.DoSpawn()

// Baca paket
pk, err := conn.ReadPacket()

// Tulis paket
err = conn.WritePacket(&packet.Text{Message: "Hello"})

// Listener (server mode)
cfg := minecraft.ListenConfig{
    StatusProvider: minecraft.NewStatusProvider("Server Name", "Gophertunnel"),
}
listener, err := cfg.Listen("raknet", ":19132")
c, err := listener.Accept()
conn := c.(*minecraft.Conn)

// Disconnect client
listener.Disconnect(conn, "Pesan disconnect")
```

**`ListenConfig` fields penting:**

```go
type ListenConfig struct {
    ErrorLog                *slog.Logger
    AuthenticationDisabled  bool          // nonaktifkan verifikasi Xbox Live
    MaximumPlayers          int           // 0 = unlimited
    AllowUnknownPackets     bool
    AllowInvalidPackets     bool
    Compression             packet.Compression
    CompressionThreshold    int           // default 256 bytes
    FlushRate               time.Duration // default time.Second/20
    PacketFunc              func(header packet.Header, payload []byte, src, dst net.Addr)
    FetchResourcePacks      func(identityData login.IdentityData, clientData login.ClientData, current []*resource.Pack) []*resource.Pack
    TexturePacksRequired    bool
}
```

---

### `minecraft/auth`

Menangani autentikasi Microsoft/Xbox Live untuk login ke server online.

```go
import "github.com/sandertv/gophertunnel/minecraft/auth"

// Minta token Live baru (akan membuka browser)
token, err := auth.RequestLiveToken()

// Buat token source yang otomatis refresh
src := auth.RefreshTokenSource(token)

// Gunakan di Dialer
dialer := minecraft.Dialer{TokenSource: src}
```

---

### `minecraft/nbt`

Encode/decode format Named Binary Tag (NBT) yang digunakan Minecraft.

Mendukung tiga format:
- `nbt.NetworkLittleEndian` — varint little endian, digunakan untuk transmisi jaringan Bedrock Edition
- `nbt.LittleEndian` — fixed size little endian, digunakan untuk world save Bedrock Edition
- `nbt.BigEndian` — fixed size big endian, digunakan oleh Java Edition

**Pemetaan tipe Go → NBT:**

| Go | NBT Tag |
|----|---------|
| `byte` / `uint8` | TAG_Byte |
| `bool` | TAG_Byte |
| `int16` | TAG_Short |
| `int32` | TAG_Int |
| `int64` | TAG_Long |
| `float32` | TAG_Float |
| `float64` | TAG_Double |
| `string` | TAG_String |
| `[]byte` | TAG_ByteArray |
| `struct{...}` | TAG_Compound |
| `[]<type>` | TAG_List |
| `map[string]<type>` | TAG_Compound |

```go
import "github.com/sandertv/gophertunnel/minecraft/nbt"

// Marshal (default: NetworkLittleEndian)
data, err := nbt.Marshal(myStruct)

// Marshal dengan encoding spesifik
data, err = nbt.MarshalEncoding(myStruct, nbt.LittleEndian)

// Unmarshal
var result MyStruct
err = nbt.Unmarshal(data, &result)

// Dengan encoder/decoder (untuk io.Reader/Writer)
encoder := nbt.NewEncoder(writer)
err = encoder.Encode(myStruct)

decoder := nbt.NewDecoder(reader)
err = decoder.Decode(&result)

// Debug: tampilkan NBT sebagai string human-readable
str, err := nbt.Dump(data, nbt.NetworkLittleEndian)
```

**Struct tags:**
```go
type MyData struct {
    Health   float32 `nbt:"minecraft:health"`
    Name     string  `nbt:"name,omitempty"`
    Ignored  int     `nbt:"-"`
}
```

---

### `minecraft/protocol`

Definisi struktur data level protokol. Digunakan internal oleh package `packet`.

**Konstanta penting:**
```go
const (
    CurrentProtocol = 924
    CurrentVersion  = "1.26.0"
)
```

**Beberapa tipe penting:**
- `AttributeValue` — nama + nilai + min/max untuk atribut entitas (e.g. `minecraft:health`)
- `SubChunkPos` / `SubChunkEntry` — posisi dan data sub-chunk
- Ability flags: `AbilityBuild`, `AbilityFly`, `AbilityMayFly`, dll.

---

### `minecraft/protocol/packet`

Berisi definisi **semua paket jaringan** Minecraft Bedrock Edition. Setiap paket mengimplementasikan interface dengan method `ID() uint32`.

**Contoh paket umum:**

| Paket | Arah | Deskripsi |
|-------|------|-----------|
| `Login` | Client → Server | Paket login awal |
| `RequestNetworkSettings` | Client → Server | Minta setting jaringan |
| `StartGame` | Server → Client | Data world saat spawn |
| `Text` | Keduanya | Pesan chat |
| `PlayerAuthInput` | Client → Server | Input gerakan/aksi player |
| `SubChunk` | Server → Client | Data terrain sub-chunk |
| `Unknown` | — | Paket tidak dikenal (jika `AllowUnknownPackets = true`) |

```go
import "github.com/sandertv/gophertunnel/minecraft/protocol/packet"

// Baca paket dan type-switch
pk, err := conn.ReadPacket()
switch p := pk.(type) {
case *packet.Text:
    fmt.Println("Chat:", p.Message)
case *packet.PlayerAuthInput:
    fmt.Println("Posisi:", p.Position)
}

// Tulis paket
err = conn.WritePacket(&packet.Text{
    TextType: packet.TextTypeChat,
    Message:  "Hello, world!",
})
```

---

### `minecraft/protocol/login`

Data yang digunakan saat login.

```go
type IdentityData struct {
    XUID        string
    Identity    string // UUID player
    DisplayName string
    TitleID     string
}

type ClientData struct {
    // Skin, locale, device info, dll.
}
```

---

## Pola Umum

### 1. Membuat Minecraft Client

```go
package main

import (
    "fmt"
    "github.com/sandertv/gophertunnel/minecraft"
    "github.com/sandertv/gophertunnel/minecraft/auth"
    "github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func main() {
    token, _ := auth.RequestLiveToken()
    src := auth.RefreshTokenSource(token)

    conn, err := minecraft.Dialer{TokenSource: src}.Dial("raknet", "server.example.com:19132")
    if err != nil {
        panic(err)
    }
    defer conn.Close()

    if err := conn.DoSpawn(); err != nil {
        panic(err)
    }

    for {
        pk, err := conn.ReadPacket()
        if err != nil {
            break
        }
        switch p := pk.(type) {
        case *packet.Text:
            fmt.Println("[Chat]", p.Message)
        }
    }
}
```

### 2. Membuat Minecraft Server

```go
package main

import (
    "fmt"
    "github.com/sandertv/gophertunnel/minecraft"
    "github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func main() {
    cfg := minecraft.ListenConfig{
        StatusProvider: minecraft.NewStatusProvider("My Server", "Gophertunnel"),
    }
    listener, err := cfg.Listen("raknet", ":19132")
    if err != nil {
        panic(err)
    }
    defer listener.Close()

    for {
        c, err := listener.Accept()
        if err != nil {
            panic(err)
        }
        go handleConn(c.(*minecraft.Conn), listener)
    }
}

func handleConn(conn *minecraft.Conn, listener *minecraft.Listener) {
    defer conn.Close()
    if err := conn.StartGame(conn.GameData()); err != nil {
        return
    }
    for {
        pk, err := conn.ReadPacket()
        if err != nil {
            return
        }
        _ = pk
    }
}
```

### 3. Membuat MITM Proxy

```go
package main

import (
    "errors"
    "sync"
    "github.com/sandertv/gophertunnel/minecraft"
    "github.com/sandertv/gophertunnel/minecraft/auth"
)

func handleConn(conn *minecraft.Conn, listener *minecraft.Listener, remoteAddr string, src oauth2.TokenSource) {
    serverConn, err := minecraft.Dialer{
        TokenSource: src,
        ClientData:  conn.ClientData(),
    }.Dial("raknet", remoteAddr)
    if err != nil {
        return
    }

    var g sync.WaitGroup
    g.Add(2)
    go func() {
        defer g.Done()
        conn.StartGame(serverConn.GameData())
    }()
    go func() {
        defer g.Done()
        serverConn.DoSpawn()
    }()
    g.Wait()

    // Forward client → server
    go func() {
        for {
            pk, err := conn.ReadPacket()
            if err != nil {
                return
            }
            if err := serverConn.WritePacket(pk); err != nil {
                var disc minecraft.DisconnectError
                if errors.As(err, &disc) {
                    listener.Disconnect(conn, disc.Error())
                }
                return
            }
        }
    }()

    // Forward server → client
    for {
        pk, err := serverConn.ReadPacket()
        if err != nil {
            return
        }
        conn.WritePacket(pk)
    }
}
```

---

## Aturan Penting untuk Agent

### Konkurensi
- `conn.ReadPacket()` **tidak thread-safe** — jangan panggil dari beberapa goroutine sekaligus.
- `conn.WritePacket()` dan method lain **thread-safe**.
- Selalu gunakan `sync.WaitGroup` untuk sinkronisasi `StartGame` + `DoSpawn` di proxy pattern.

### Versi & Protokol
- gophertunnel hanya mendukung **satu versi Minecraft sekaligus** (biasanya versi terbaru).
- Cek `protocol.CurrentProtocol` dan `protocol.CurrentVersion` untuk versi aktif.
- Jangan hardcode nomor protokol; selalu gunakan konstanta dari package `protocol`.

### Autentikasi
- Untuk koneksi ke server online (Realms, server resmi), `TokenSource` wajib diisi.
- Token disimpan dan di-refresh otomatis via `auth.RefreshTokenSource`.
- Untuk server lokal/offline, bisa lewati auth dengan `AuthenticationDisabled: true` di `ListenConfig`.

### Paket
- Type-switch adalah cara utama untuk menangani paket — selalu handle `default` case.
- Untuk paket tidak dikenal, aktifkan `AllowUnknownPackets: true` di `ListenConfig` dan cek `*packet.Unknown`.
- Jangan assume struktur paket stabil antar versi Minecraft — update gophertunnel saat update Minecraft.

### Performa
- `FlushRate` default adalah 20Hz (50ms). Turunkan untuk latensi rendah; naikkan untuk efisiensi.
- `CompressionThreshold` default 256 bytes — data di bawah threshold tidak dikompresi.
- Gunakan `ClientCacheEnabled` untuk mengurangi transmisi chunk data bila client mendukung.

### Error Handling
- Selalu cek `minecraft.DisconnectError` saat forwarding paket di proxy.
- Gunakan `defer conn.Close()` dan `defer listener.Disconnect(conn, "reason")` untuk cleanup.

---

## Dependencies Eksternal

| Package | Kegunaan |
|---------|----------|
| `github.com/sandertv/go-raknet` | Implementasi transport RakNet |
| `golang.org/x/oauth2` | OAuth2 untuk autentikasi Microsoft |
| `golang.org/x/text/language` | Parsing locale/bahasa |
| `github.com/pelletier/go-toml` | Parsing konfigurasi TOML (di contoh proxy) |

---

## Tips untuk AI Agent

1. **Saat menambah paket baru:** Selalu cek `minecraft/protocol/packet/` untuk paket yang sudah ada sebelum membuat custom packet.
2. **Saat debug:** Gunakan `PacketFunc` di `ListenConfig` untuk intercept dan log semua paket tanpa mengubah alur utama.
3. **Saat update versi Minecraft:** Update gophertunnel ke versi yang mendukung Minecraft tersebut, lalu regenerasi kode yang bergantung pada nomor protokol.
4. **NBT:** Selalu gunakan `nbt.NetworkLittleEndian` untuk data yang dikirim melalui jaringan Bedrock; gunakan `nbt.LittleEndian` untuk baca/tulis world save.
5. **Resource pack:** Gunakan `listener.AddResourcePack()` sebelum client terhubung. Method ini tidak mempengaruhi koneksi yang sudah aktif.

---

## Harness Engineering

Project ini dilengkapi dengan **full engineering harness** — sistem scaffolding
yang meregulasi codebase menuju kondisi yang diinginkan. Lihat
`docs/HARNESS_ENGINEERING.md` untuk dokumentasi lengkap.

### Aturan Harness untuk AI Agent

**Sebelum menulis kode:**
1. Jalankan `make harness` untuk memastikan baseline hijau sebelum mulai.
2. Baca aturan dependency di `internal/harness/architecture/architecture.go`
   (`DefaultRules`) — jangan membuat package leaf (ai, config, event) import
   package yang lebih tinggi (bot, handler).
3. Ikuti konfigurasi linter di `.golangci.yml` — itu adalah computational
   guide yang mencegah issue sebelum terjadi.

**Saat menulis kode:**
4. Setiap function pure-logic baru **wajib** punya unit test. Tulis test
   bersamaan dengan kode (TDD preferred).
5. Gunakan `t.Parallel()` di semua test untuk konkurensi safety.
6. Test file dinamakan `<file>_test.go` dan ditempatkan di package yang sama
   (white-box testing untuk akses unexported symbols).
7. Jangan import `internal/bot` dari `internal/ai`, `internal/config`, atau
   `internal/event` — ini akan ditangkap oleh architecture fitness sensor.

**Setelah menulis kode:**
8. Jalankan `make harness` sebelum commit. Semua check harus pass.
9. Jalankan `make harness-full` sebelum PR (termasuk race detector & coverage).
10. Jika menemukan bug baru, tambahkan test yang mereproduksi bug tersebut
    **sebelum** memperbaikinya (steering loop: issue → sensor → fix → guide).

**Saat menambah modul/package baru:**
11. Tambahkan aturan dependency di `DefaultRules()` jika package baru punya
    batasan import.
12. Tambahkan test architecture fitness di
    `internal/harness/architecture/architecture_test.go` untuk memvalidasi
    aturan baru.
13. Daftarkan package di test coverage map di `docs/HARNESS_ENGINEERING.md`.

**SOLID principles di harness:**
- `Sensor` dan `Guide` adalah interface terpisah (Interface Segregation).
- `Runner` menerima implementasi baru tanpa modifikasi (Open/Closed).
- `Runner` bergantung pada abstraksi `Sensor`/`Reporter`, bukan konkrit
  (Dependency Inversion).

### Quick Commands

```bash
make help          # List semua target
make harness       # Fast: fmt-check, vet, lint, build, test, arch
make harness-full  # Full: + race detector + coverage
make test          # Unit tests saja
make lint          # golangci-lint saja
make arch          # Architecture fitness saja
make cover         # Coverage report
make install-hooks # Install pre-commit hook
```