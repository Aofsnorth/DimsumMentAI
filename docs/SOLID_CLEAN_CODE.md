# SOLID, Clean Code, dan Prinsip Pemrograman Profesional

> Panduan praktis untuk menjaga kualitas kode **DimsumMentAI** agar mudah diuji, di-review, dan dikembangkan secara berkelanjutan.

---

## 1. SOLID Principles

### S — Single Responsibility Principle

Setiap modul, struct, atau fungsi hanya boleh punya **satu alasan untuk berubah**.

**Contoh di project:**

- `internal/bot/fishing/fisher.go` hanya menangani memancing.
- `internal/bot/farming/farmer.go` hanya menangani farming.
- `internal/harness/` memisahkan `Sensor`, `Guide`, `Reporter`, dan `Runner` sebagai interface tersendiri.

**Anti-pattern:**

```go
// Jangan: satu fungsi menangani login, parsing world, dan pathfinding.
func DoEverything() { ... }
```

**Pattern:**

```go
func (f *Fisher) Fish(ctx context.Context) error { ... }
func (f *Farmer) PlantSeeds(ctx context.Context) error { ... }
```

---

### O — Open/Closed Principle

Open for extension, closed for modification. Tambahkan perilaku baru dengan membuat struct/interface baru, bukan mengubah kode yang sudah stabil.

**Contoh di project:**

- `Runner.RegisterSensor` dan `Runner.RegisterGuide` menerima interface baru tanpa mengubah `Runner`.
- `PacketLoopFunc`, `SendPlayerSkinFunc`, dll. di `internal/bot/bot.go` memungkinkan perilaku disuntik dari luar tanpa modifikasi `Bot`.

---

### L — Liskov Substitution Principle

Implementasi interface harus bisa diganti satu sama lain tanpa merusak program.

**Contoh:**

```go
var r harness.Reporter = &harness.ConsoleReporter{} // bisa juga JSONReporter, NullReporter
result, _ := r.Report(findings)
```

---

### I — Interface Segregation Principle

Interface harus kecil dan spesifik. Jangan memaksa implementasi memiliki method yang tidak dibutuhkan.

**Contoh di project:**

```go
// internal/harness/harness.go
type Sensor interface {
    Name() string
    Category() Category
    Mode() ExecutionMode
    Run() ([]Finding, error)
}

type Guide interface {
    Name() string
    Category() Category
    Run() ([]Finding, error)
}
```

`Sensor` dan `Guide` dipisahkan karena tanggung jawabnya berbeda.

---

### D — Dependency Inversion Principle

Bergantung pada abstraksi, bukan konkrit.

**Contoh di project:**

- `Runner` bergantung pada interface `Sensor` dan `Reporter`, bukan implementasi konkret.
- `Farmer`, `Fisher`, `Explorer` menerima interface `Bot` yang kecil, bukan `*bot.Bot` langsung.

```go
// internal/bot/farming/farmer.go
type Bot interface {
    GetCoords() mgl32.Vec3
    ...
}

type Farmer struct{ bot Bot }
```

---

## 2. Clean Code Principles

### Nama yang Bermakna

Nama fungsi/variabel harus menjelaskan maksudnya.

**Jangan:**

```go
n := 3
rv := tc.resolveNextTarget()
```

**Lakukan:**

```go
const maxRetries = 3
target := tc.resolveNextTarget()
```

### Fungsi Kecil dan Fokus

- Panjang fungsi ideal di bawah 40 baris.
- Cyclomatic complexity di bawah 15 (threshold harness).
- Jika fungsi melebihi, ekstrak helper.

**Contoh:**

```go
func (tc *TickContext) updateLookDirection() {
    if tc.isLookingAtTarget() {
        tc.applyTrackedLook()
        return
    }
    tc.pickNaturalLookTarget()
}
```

### Hindari Duplikasi (DRY)

Jika pola sama muncul >2 kali, ekstrak ke helper.

**Contoh:**

- `internal/bot/gathering/inventory_count.go` menyatukan logika perhitungan inventory untuk miner, looter, dan chopper.
- `internal/bot/rand/rand.go` menyatukan `math/rand` untuk seluruh codebase.

### Error Handling yang Jelas

- Error tidak boleh di-silent.
- `golangci-lint` `errcheck` memastikan return error dicek.
- Jika memang boleh ignore, gunakan `_ = ...` secara eksplisit.

```go
if err := doSomething(); err != nil {
    return fmt.Errorf("do something: %w", err)
}
```

### Komentar yang Bermanfaat

- Komentar menjelaskan **mengapa**, bukan **apa**.
- Hindari komentar yang redundan.

**Jangan:**

```go
// increment counter
counter++
```

**Lakukan:**

```go
// Retry with backoff to avoid hammering the server.
counter++
```

---

## 3. Prinsip Pemrograman Profesional

### KISS — Keep It Simple, Stupid

Solusi paling sederhana yang bekerja adalah yang terbaik. Hindari over-engineering.

### YAGNI — You Aren't Gonna Need It

Jangan menulis fitur yang belum dibutuhkan. Jangan membuat abstraksi sebelum ada 3 contoh konkret.

### Defensive Programming

- Validate input, terutama dari jaringan (Minecraft protocol).
- Clamp integer conversions dengan `safecast` (lihat `internal/safecast` setelah refactor G115).
- Handle `nil` context dengan `context.TODO()` alih-alih `nil`.

### Security by Default

- Jangan hardcode secret/token.
- `.env` sudah di-gitignore; gunakan `godotenv`.
- `gosec` `G404` (weak random) diperbaiki dengan `internal/bot/rand`.
- `gosec` `G115` (integer overflow) sedang diperbaiki via `internal/safecast`.
- `gosec` `G306`/`G114` sudah diperbaiki.

### Test-Driven Development (TDD)

- Tulis test sebelum implementasi untuk fungsi pure-logic baru.
- `internal/bot/pathfinder/astar_test.go` dan `internal/harness/*_test.go` adalah contoh.
- Setiap test harus jelas, fokus, dan parallel-safe (`t.Parallel()`).

### Code Review sebagai Harness

Sebelum commit, jalankan:

```bash
make harness
```

Ini menjalankan guide (fmt, vet, lint) dan sensor (test, arch, race, coverage) untuk menangkap masalah sebelum review manusia.

---

## 4. Checklist Harian

- [ ] `make harness` pass
- [ ] `golangci-lint run` 0 issue
- [ ] Fungsi baru memiliki unit test
- [ ] Error return value dicek
- [ ] Tidak ada duplikasi logika yang signifikan
- [ ] Package import mematuhi aturan `internal/harness/architecture`
- [ ] Dokumentasi package/header ada untuk file non-test

---

## Referensi

- Robert C. Martin — *Clean Code* dan *Clean Architecture*
- Martin Fowler — *Refactoring*
- Go Code Review Comments — [go.dev/wiki/CodeReviewComments](https://go.dev/wiki/CodeReviewComments)
- Go Blog — [CodeReviewComments](https://go.dev/blog/index) and [Go Proverbs](https://go-proverbs.github.io/)
