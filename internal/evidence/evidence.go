// Evidence: a durable, structured record of what the bot actually did.
//
// Everything else this bot does can be argued about from the log, and right now
// the log is a console line that goes to stdout and is gone when the process
// ends. That is enough to debug a live session and not nearly enough to answer
// the questions that actually come up later:
//
//   - did the bot really go where it said it was going?
//   - which plan was it on when it failed, and what had it already completed?
//   - how many times did this step fail before the planner was asked again?
//   - did the weapon swap happen, or did it only decide to?
//
// Those are all questions about sequence, and sequence is exactly what a
// console log throws away. This package exists to keep it.
//
// Three rules, in priority order:
//
//   - Never block the bot. A call site hands the record to a channel and moves
//     on. If the disk is slow, records are dropped and counted — a bot that
//     stalls waiting for a log write is a bot that died in a cave because a
//     hard drive was busy.
//   - Never rate-limit. The existing debug logger drops anything within 250ms
//     of the last line, which is right for "I am debugging this one thing" and
//     exactly wrong for evidence: the dropped line is the one you wanted.
//   - Never lie about what is missing. A gap in the evidence is reported as a
//     gap, with a count, so "the bot did not do this" and "we did not see it
//     happen" stay distinguishable.

package evidence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Kind is the sort of thing a record describes. It is a plain string rather
// than an enum so a new subsystem does not have to come back here to be
// recorded — the cost of a typo is a field nobody queries, not a line that
// cannot be written.
type Kind string

// The kinds worth naming, because these are the ones that get asked about
// later. Anything else is still accepted and still recorded.
const (
	KindPlanReady    Kind = "plan.ready"
	KindStepStart    Kind = "step.start"
	KindStepDone     Kind = "step.done"
	KindStepFailed   Kind = "step.failed"
	KindStepRetry    Kind = "step.retry"
	KindPlanComplete Kind = "plan.complete"
	KindReflex       Kind = "reflex"
	KindWeaponChoice Kind = "combat.weapon"
	KindShieldPlan   Kind = "combat.shield"
	KindDimension    Kind = "dimension.change"
	KindRecovery     Kind = "inventory.recover"
	KindEpisodeStart Kind = "episode.start"
	KindEpisodeEnd   Kind = "episode.end"
	KindEpisodeWrap  Kind = "episode.wrap_up"
	KindIdle         Kind = "idle"
	KindWatchdog     Kind = "watchdog"
	KindDiscovery    Kind = "discovery"
	KindError        Kind = "error"
)

// Record is one line of evidence.
//
// Fields is free-form on purpose. The alternative is a struct that has to be
// edited every time some new subsystem wants to record something, and a struct
// that has to be edited every time is a struct that does not get extended.
type Record struct {
	// Time is when the thing happened, not when the line was written. Those
	// differ by however long the channel was full, and the difference is the
	// whole point of having a timestamp at all.
	Time time.Time `json:"ts"`
	// Seq is a monotonically increasing counter, so two records that share a
	// timestamp still have a defined order. Wall clocks are not that good.
	Seq uint64 `json:"seq"`
	// Kind is what happened.
	Kind Kind `json:"kind"`
	// Detail is the human-readable line, kept because a log nobody can read is
	// a log nobody will read.
	Detail string `json:"detail,omitempty"`
	// Fields carries whatever the subsystem wanted to say.
	Fields map[string]any `json:"fields,omitempty"`
}

// defaultBuffer is how many records may be waiting to be written before new
// ones are dropped. It is sized for a burst — a plan replanning, a fight, a
// dimension change all produce a dozen lines in a moment — and not for a
// backlog, which is a disk problem rather than a logging problem.
const defaultBuffer = 256

// maxFileBytes is where events.jsonl is rolled over.
//
// Rolling rather than truncating is deliberate: the session that went wrong is
// usually not the one you are watching when you notice. Rotation by time alone
// loses that; rotation by size keeps recent history at a predictable cost.
const maxFileBytes = 16 << 20 // 16 MiB

// Logger writes records to a file in the background.
//
// The zero value is not usable; call Open. A nil *Logger is valid and inert, so
// a call site never has to check whether evidence is switched on.
type Logger struct {
	records chan Record

	// dropped counts records lost to a full buffer. It is surfaced rather than
	// swallowed: a log with holes in it that does not say so is worse than no
	// log, because it invites conclusions the evidence cannot support.
	dropped atomic.Uint64
	written atomic.Uint64

	mu      sync.Mutex
	file    *os.File
	bytes   int64
	enabled bool

	closed chan struct{}
	once   sync.Once
	wg     sync.WaitGroup
}

// Open starts a logger writing to path, creating the directory if needed.
//
// It returns an inert logger when it cannot open the file. Failing to start a
// log must never stop the bot from joining a world — an unreadable log
// directory is a nuisance, not a reason to refuse to play.
func Open(path string) *Logger {
	l := &Logger{
		records: make(chan Record, defaultBuffer),
		closed:  make(chan struct{}),
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		l.enabled = false
		return l
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		l.enabled = false
		return l
	}
	if info, statErr := f.Stat(); statErr == nil {
		l.bytes = info.Size()
	}
	l.file = f
	l.enabled = true

	l.wg.Add(1)
	go l.run()
	return l
}

// Enabled reports whether records are actually being written.
//
// A caller that wants to avoid the cost of building an expensive Fields map can
// ask first, and a caller reading a log can tell the difference between "no
// events" and "logging was off".
func (l *Logger) Enabled() bool {
	return l != nil && l.enabled
}

// Record hands an event to the writer.
//
// It never blocks. If the buffer is full the record is dropped and counted,
// because the alternative — making the game loop wait on a file write — trades
// a small hole in the evidence for a bot that stands still at the wrong moment.
func (l *Logger) Record(kind Kind, detail string, fields map[string]any) {
	if l == nil || !l.enabled {
		return
	}
	rec := Record{
		Time:   time.Now().UTC(),
		Kind:   kind,
		Detail: detail,
		Fields: fields,
	}
	// A single non-blocking send. There is deliberately no retry and no wait:
	// a caller that cannot have its record written right now would have to be
	// able to give up, and the count is how the gap becomes visible.
	select {
	case l.records <- rec:
	default:
		l.dropped.Add(1)
	}
}

// Dropped reports how many records were lost to a full buffer.
func (l *Logger) Dropped() uint64 {
	if l == nil {
		return 0
	}
	return l.dropped.Load()
}

// Close flushes and stops the writer.
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	var err error
	l.once.Do(func() {
		close(l.closed)
		l.wg.Wait()
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.file != nil {
			err = l.file.Close()
			l.file = nil
			l.enabled = false
		}
	})
	return err
}

// run drains the buffer until the logger is closed.
func (l *Logger) run() {
	defer l.wg.Done()
	for {
		select {
		case <-l.closed:
			// Drain whatever is already queued before leaving, so a clean
			// shutdown does not lose the last few events — which are usually
			// the ones about what just went wrong.
			for {
				select {
				case rec := <-l.records:
					l.write(rec)
					continue
				default:
				}
				return
			}
		case rec := <-l.records:
			l.write(rec)
		}
	}
}

// write appends one record as a single line of JSON.
//
// Lines are written under one lock, whole. A half-written line in the middle of
// the file is worse than a missing one, because every later reader has to
// decide what to do with it.
func (l *Logger) write(rec Record) {
	// The sequence is assigned at write time, not at call time, so it reflects
	// the order the records actually reached the file rather than the order
	// several goroutines happened to race into the channel.
	rec.Seq = l.written.Add(1)

	data, err := json.Marshal(rec)
	if err != nil {
		// A field that will not marshal is a bug in the caller, and losing the
		// record is better than losing the file. The kind is still worth
		// keeping, so it is written on its own.
		fallback, _ := json.Marshal(struct {
			Time   time.Time `json:"ts"`
			Seq    uint64    `json:"seq"`
			Kind   Kind      `json:"kind"`
			Detail string    `json:"detail"`
			Error  string    `json:"error"`
		}{rec.Time, rec.Seq, rec.Kind, rec.Detail, "fields would not marshal: " + err.Error()})
		data = fallback
	}
	data = append(data, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	l.rollIfNeeded(int64(len(data)))
	n, err := l.file.Write(data)
	l.bytes += int64(n)
	if err != nil {
		l.dropped.Add(1)
	}
}

// rollIfNeeded rotates the file once it grows past the cap. The caller holds
// the lock.
func (l *Logger) rollIfNeeded(incoming int64) {
	if l.bytes+incoming <= maxFileBytes {
		return
	}
	name := l.file.Name()
	if err := l.file.Close(); err != nil {
		return
	}
	rolled := name + ".1"
	_ = os.Remove(rolled)
	// Rename rather than truncate: the session that went wrong is usually not
	// the one you are watching when you finally notice.
	if err := os.Rename(name, rolled); err != nil {
		return
	}
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		l.file = nil
		l.enabled = false
		return
	}
	l.file = f
	l.bytes = 0
}

// Summary renders the counters for a log line at shutdown.
//
// It exists so a session that dropped records says so. A run that ended with
// evidence missing and did not mention it is a run whose conclusions cannot be
// trusted, and that is worth one line at the end.
func (l *Logger) Summary() string {
	if l == nil {
		return "evidence: off"
	}
	if !l.enabled {
		return "evidence: unavailable"
	}
	return fmt.Sprintf("evidence: %d written, %d dropped", l.written.Load(), l.dropped.Load())
}
