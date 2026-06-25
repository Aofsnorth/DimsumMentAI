# Harness Engineering — DimsumMentAI

> **Harness Engineering** adalah disiplin membangun scaffolding di sekitar
> sistem untuk meregulasinya menuju kondisi yang diinginkan. Konsep ini
> berasal dari dunia AI agent engineering (Agent = Model + Harness), namun
> prinsipnya berlaku universal untuk codebase software.
>
> Referensi: [Martin Fowler — Harness engineering for coding agent users](https://martinfowler.com/articles/harness-engineering.html)

---

## Konsep Inti

Harness terdiri dari dua jenis kontrol yang bekerja bersama:

### Guides (Feedforward Controls)
Mengantisipasi perilaku yang tidak diinginkan dan mencegahnya **sebelum**
terjadi. Guides meningkatkan probabilitas hasil yang benar pada percobaan
pertama.

**Contoh:** linter, formatting rules, conventions, AGENTS.md rules.

### Sensors (Feedback Controls)
Mengobservasi sistem **setelah** perubahan dan melaporkan deviasi. Sensors
memungkinkan self-correction sebelum issue mencapai reviewer manusia.

**Contoh:** unit tests, architecture checks, coverage analysis, race detector.

### Computational vs Inferential

| Tipe | Eksekusi | Kecepatan | Determinisme | Contoh |
|------|----------|-----------|--------------|--------|
| **Computational** | CPU | ms–detik | Deterministik | linter, tests, vet, arch check |
| **Inferential** | GPU/NPU | detik–menit | Probabilistik | AI code review, LLM-as-judge |

### Tiga Kategori Regulasi

1. **Maintainability** — kualitas kode internal, kompleksitas, style.
2. **Architecture Fitness** — batas modul, arah dependency, layering.
3. **Behaviour** — kebenaran fungsional melalui tests.

---

## Arsitektur Harness di DimsumMentAI

```
┌─────────────────────────────────────────────────────────┐
│                    HARNESS RUNNER                        │
│         (internal/harness/runner.go)                     │
│   Orchestrates all sensors & guides concurrently         │
└────────────┬────────────────────┬───────────────────────┘
             │                    │
     ┌───────▼──────┐    ┌───────▼───────┐
     │   GUIDES     │    │   SENSORS     │
     │ (Feedforward)│    │  (Feedback)   │
     └──────────────┘    └───────────────┘
             │                    │
     ┌───────┴──────┐    ┌────────┴────────┐
     │ .golangci.yml│    │  Unit Tests     │
     │ gofmt        │    │  Arch Fitness   │
     │ goimports    │    │  Race Detector  │
     │ AGENTS.md    │    │  Coverage       │
     │ Skills       │    │  go vet         │
     └──────────────┘    └─────────────────┘
```

### Core Package: `internal/harness/`

Package ini mendefinisikan abstraksi SOLID untuk harness:

| Interface | Tanggung Jawab | SOLID Principle |
|-----------|---------------|-----------------|
| `Sensor` | Feedback control — observe & report | SRP, ISP |
| `Guide` | Feedforward control — prevent issues | SRP, ISP |
| `Reporter` | Format & emit results | SRP, ISP |
| `Runner` | Orchestrate all checks concurrently | OCP, DIP |

**SOLID compliance:**
- **S** — Setiap interface punya satu tanggung jawab
- **O** — Sensor/Guide baru bisa ditambah tanpa modifikasi Runner
- **L** — Semua implementasi substitutable melalui interface
- **I** — Sensor, Guide, Reporter adalah interface terpisah yang kecil
- **D** — Runner bergantung pada abstraksi (Sensor, Reporter), bukan konkrit

### Architecture Fitness: `internal/harness/architecture/`

Sensor komputasional yang mem-parse import statements dan memvalidasi
aturan dependency antar package:

```
cmd/              → boleh import apapun di internal/
internal/harness/ → TIDAK boleh import bot/ai/handler (meta-layer)
internal/ai/      → TIDAK boleh import internal/bot (leaf service)
internal/config/  → TIDAK boleh import bot/ai (leaf)
internal/event/   → TIDAK boleh import bot/ai (leaf)
internal/bot/     → boleh import ai, config, event, handler
```

Setiap violation menghasilkan `Finding` dengan `Suggest` field yang berisi
instruksi perbaikan — ini adalah "positive prompt injection" untuk
self-correction oleh coding agent.

---

## Cara Menjalankan Harness

### Quick Reference

```bash
make help          # List semua target
make harness       # Fast checks: fmt-check, vet, lint, build, test, arch
make harness-full  # Full checks: + race detector + coverage
make test          # Unit tests saja
make lint          # golangci-lint saja
make arch          # Architecture fitness saja
make cover         # Coverage report
```

### Pre-commit Hook

```bash
make install-hooks   # Install .git/hooks/pre-commit
```

Hook otomatis menjalankan: gofmt check, go vet, build, dan tests untuk
package yang berubah — sebelum commit dibuat.

### CI Pipeline

GitHub Actions workflow (`.github/workflows/harness.yml`) menjalankan:

1. **Guides job:** gofmt check, go vet, golangci-lint
2. **Sensors job:** build, unit tests, architecture fitness, race detector, coverage
3. **Summary gate:** gagal jika salah satu job gagal

---

## Menambahkan Sensor Baru

Harness dirancang untuk extensible (Open/Closed). Untuk menambah sensor baru:

```go
package mypackage

import "bedrock-ai/internal/harness"

type MySensor struct{}

func (s *MySensor) Name() string                       { return "my.sensor" }
func (s *MySensor) Category() harness.Category         { return harness.CategoryBehaviour }
func (s *MySensor) Mode() harness.ExecutionMode        { return harness.ModeComputational }
func (s *MySensor) Run() ([]harness.Finding, error) {
    // Lakukan pengecekan...
    return []harness.Finding{
        {
            Check:    s.Name(),
            Severity: harness.SeverityWarning,
            Message:  "found something worth noting",
            Suggest:  "consider doing X instead of Y",
        },
    }, nil
}
```

Daftarkan ke Runner:

```go
runner := harness.NewRunner(reporter)
runner.RegisterSensor(&mypackage.MySensor{})
result, _ := runner.Run()
```

---

## Test Coverage Map

| Package | Test File | What's Covered |
|---------|-----------|----------------|
| `internal/ai` | `parser_test.go` | Action tag extraction, think-block stripping, whitespace collapse |
| `internal/ai` | `throttler_test.go` | Duplicate detection, rate limiting, rollback, case-insensitivity |
| `internal/ai` | `history_test.go` | Message storage, capping, copy semantics, FixMessages sanitization |
| `internal/bot/action` | `labels_test.go` | Supported action labels, aliases, completeness |
| `internal/bot/action` | `helpers_test.go` | normalizeItemName, isWoodLike, normalizeCropType, parseCount, durationTicks |
| `internal/bot/pathfinder` | `heuristic_test.go` | Euclidean distance, symmetry, negative coords |
| `internal/bot/pathfinder` | `node_test.go` | Node equality, link type constants |
| `internal/bot/pathfinder` | `astar_test.go` | A* pathfinding, fallback, reconstructPath, target reachability |
| `internal/config` | `loader_test.go` | YAML loading, defaults, validation, error cases |
| `internal/event` | `bus_test.go` | Pub/sub, multiple subscribers, event type isolation, concurrency |
| `internal/harness` | `harness_test.go` | Runner orchestration, finding aggregation, reporter |
| `internal/harness/architecture` | `architecture_test.go` | Dependency rule enforcement, file filtering, custom rules |

---

## Steering Loop

Harness engineering bukan one-time setup — itu adalah **steering loop**.
Setiap kali issue terjadi berulang, perbaiki guide atau sensor untuk
mencegahnya di masa depan:

1. **Issue terjadi** → tambahkan test (sensor) yang mendeteksinya
2. **Issue berulang** → tambahkan linter rule atau AGENTS.md rule (guide) yang mencegahnya
3. **Pattern baru** → dokumentasikan di AGENTS.md atau skill

```
  Issue ──→ Sensor detects ──→ Fix ──→ Guide prevents recurrence
                                              │
                                              ▼
                                    Steering Loop (human iterates)
```

---

## Referensi

- [Martin Fowler — Harness engineering for coding agent users](https://martinfowler.com/articles/harness-engineering.html)
- [LangChain — The Anatomy of an Agent Harness](https://www.langchain.com/blog/the-anatomy-of-an-agent-harness)
- [OpenAI — Harness engineering: leveraging Codex](https://openai.com/index/harness-engineering/)
- [Anthropic — Effective harnesses for long-running agents](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents)
- [GitHub — awesome-harness-engineering](https://github.com/ai-boost/awesome-harness-engineering)
