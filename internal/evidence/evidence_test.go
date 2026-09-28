package evidence

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Evidence only works if it is complete, ordered, and honest about its own gaps.
// A log that is missing lines, puts them in the wrong order, or does not say it
// is missing anything is worse than no log at all, because it invites
// conclusions the evidence cannot support.

// readRecords reads back everything written to a file.
func readRecords(t *testing.T, path string) []Record {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open evidence file: %v", err)
	}
	defer f.Close()

	var out []Record
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("evidence line is not valid JSON: %v\n%s", err, line)
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan evidence: %v", err)
	}
	return out
}

// TestRecordsAreWrittenAsOneJSONObjectPerLine is the format itself. Anything
// else and every consumer needs a parser.
func TestRecordsAreWrittenAsOneJSONObjectPerLine(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "events.jsonl")
	l := Open(path)

	l.Record(KindPlanReady, "plan-1 ready", map[string]any{"objective": "get wood", "steps": 3})
	l.Record(KindStepDone, "gathered", map[string]any{"kind": "gather"})
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	recs := readRecords(t, path)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	if recs[0].Kind != KindPlanReady {
		t.Errorf("kind = %q, want %q", recs[0].Kind, KindPlanReady)
	}
	if recs[0].Detail != "plan-1 ready" {
		t.Errorf("detail = %q", recs[0].Detail)
	}
	if recs[0].Fields["objective"] != "get wood" {
		t.Errorf("fields = %v, want the objective carried through", recs[0].Fields)
	}
	if recs[0].Time.IsZero() {
		t.Error("a record with no timestamp cannot be placed in a session")
	}
}

// TestSequenceGivesAnOrderThatWallClocksCannot pins the reason Seq exists. Two
// records can share a millisecond, and "what happened first" has to have an
// answer that does not depend on how precise the clock is.
func TestSequenceGivesAnOrderThatWallClocksCannot(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "events.jsonl")
	l := Open(path)

	const n = 200
	for i := 0; i < n; i++ {
		l.Record(KindStepDone, "step", map[string]any{"i": i})
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	recs := readRecords(t, path)
	if len(recs) != n {
		t.Fatalf("got %d records, want %d", len(recs), n)
	}
	for i, rec := range recs {
		if want := uint64(i + 1); rec.Seq != want {
			t.Fatalf("record %d has seq %d, want %d", i, rec.Seq, want)
		}
		if int(rec.Fields["i"].(float64)) != i {
			t.Fatalf("record %d carries i=%v, want %d", i, rec.Fields["i"], i)
		}
	}
}

// TestTheLoggerNeverBlocksTheCaller is the promise that matters most. A bot that
// stalls waiting for a log write is a bot that stood still at the wrong moment,
// which is a worse failure than a missing line.
func TestTheLoggerNeverBlocksTheCaller(t *testing.T) {
	t.Parallel()

	// A logger with a full buffer and no writer to drain it is the worst case a
	// stalled disk can produce, and Record still has to return.
	l := &Logger{
		records: make(chan Record, 1),
		closed:  make(chan struct{}),
		enabled: true,
	}
	for i := 0; i < 4; i++ {
		l.Record(KindError, "flooding a logger nobody is reading", nil)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100000; i++ {
			l.Record(KindError, "still going", nil)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Record blocked on a logger with a full buffer")
	}
}

// TestAnUnopenablePathLeavesTheLoggerInert is the failure direction. An
// unreadable log directory is a nuisance, not a reason to refuse to play.
func TestAnUnopenablePathLeavesTheLoggerInert(t *testing.T) {
	t.Parallel()

	// A regular file where the logger wants a directory: MkdirAll cannot make a
	// directory out of it, so Open has to give up and say so.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	l := Open(filepath.Join(blocker, "events.jsonl"))
	defer l.Close()

	if l.Enabled() {
		t.Error("a logger that could not open its file reports itself as enabled")
	}
	// Recording into an inert logger must still be safe, because call sites
	// record unconditionally and have no business knowing.
	l.Record(KindError, "into the void", nil)
	if !strings.Contains(l.Summary(), "unavailable") {
		t.Errorf("Summary = %q, want it to report the logger as unavailable", l.Summary())
	}
}

// TestAFullBufferDropsAndCountsRatherThanWaiting is the other half of the same
// promise. The hole has to be visible, or "the bot did not do this" and "we
// did not see it happen" become indistinguishable.
func TestAFullBufferDropsAndCountsRatherThanWaiting(t *testing.T) {
	t.Parallel()

	// Hold the writer hostage by giving it a file it cannot write to quickly is
	// not possible here, so the buffer is filled directly: the same code path a
	// stalled disk would take.
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l := &Logger{
		records: make(chan Record, 2),
		closed:  make(chan struct{}),
		enabled: true,
	}
	_ = path

	for i := 0; i < 50; i++ {
		l.Record(KindStepDone, "flooding", nil)
	}
	if l.Dropped() == 0 {
		t.Error("50 records into a 2-slot buffer dropped nothing; the loss is not being counted")
	}
	if l.Dropped() != 48 {
		t.Errorf("dropped %d, want 48", l.Dropped())
	}
	if !strings.Contains(l.Summary(), "48 dropped") {
		t.Errorf("Summary = %q, want it to report the gap", l.Summary())
	}
}

// TestCloseFlushesWhatIsAlreadyQueued is the shutdown case. The last few events
// are usually the ones about what just went wrong.
func TestCloseFlushesWhatIsAlreadyQueued(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "events.jsonl")
	l := Open(path)

	const n = 50
	for i := 0; i < n; i++ {
		l.Record(KindReflex, "surfacing", map[string]any{"i": i})
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if recs := readRecords(t, path); len(recs) != n {
		t.Errorf("got %d records after Close, want all %d", len(recs), n)
	}
}

// TestANilLoggerIsInert lets a call site record unconditionally. A subsystem
// should not have to know whether evidence is switched on.
func TestANilLoggerIsInert(t *testing.T) {
	t.Parallel()

	var l *Logger
	// None of these may panic.
	l.Record(KindError, "no logger", nil)
	if l.Enabled() {
		t.Error("a nil logger reports itself as enabled")
	}
	if l.Dropped() != 0 {
		t.Error("a nil logger counted drops")
	}
	if err := l.Close(); err != nil {
		t.Errorf("Close on nil: %v", err)
	}
	if !strings.Contains(l.Summary(), "off") {
		t.Errorf("Summary = %q, want it to say logging is off", l.Summary())
	}
}

// TestConcurrentRecordingProducesWholeLines is the corruption guard. Every
// subsystem records from its own goroutine, and a half-written line in the middle
// of the file is a line every later reader has to decide what to do with.
//
// The record count is deliberately well under the buffer: this test is about
// whether concurrent writers corrupt lines, not about what happens when the
// buffer overflows, which TestAFullBufferDropsAndCountsRatherThanWaiting covers
// on its own. Drops here would be correct behaviour, not a failure.
func TestConcurrentRecordingProducesWholeLines(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "events.jsonl")
	l := Open(path)

	const workers, each = 8, 20
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				l.Record(KindStepDone, "concurrent", map[string]any{"worker": w, "i": i})
			}
		}(worker)
	}
	wg.Wait()
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// readRecords fails the test on any line that is not valid JSON, which is
	// exactly the corruption this is checking for.
	recs := readRecords(t, path)
	if len(recs) != workers*each {
		t.Errorf("got %d records, want %d", len(recs), workers*each)
	}

	// Sequence numbers must be unique even though eight goroutines were racing.
	seen := make(map[uint64]bool, len(recs))
	for _, rec := range recs {
		if seen[rec.Seq] {
			t.Fatalf("sequence %d was written twice", rec.Seq)
		}
		seen[rec.Seq] = true
	}
}

// TestFieldsThatWillNotMarshalStillProduceALine is the bad-caller guard. Losing
// the record is better than losing the file, but the kind is the one thing worth
// keeping, because it is what tells you what you just lost.
func TestFieldsThatWillNotMarshalStillProduceALine(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "events.jsonl")
	l := Open(path)

	// A channel cannot be marshalled to JSON.
	l.Record(KindError, "unmarshalable fields", map[string]any{"bad": make(chan int)})
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	recs := readRecords(t, path)
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if recs[0].Kind != KindError {
		t.Errorf("kind = %q, want the kind preserved", recs[0].Kind)
	}
}

// TestReopeningAppendsRatherThanTruncating. Two sessions in the same directory
// are two sessions, and the first one's evidence is not the second's to delete.
func TestReopeningAppendsRatherThanTruncating(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "events.jsonl")

	first := Open(path)
	first.Record(KindPlanReady, "session one", nil)
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second := Open(path)
	second.Record(KindPlanReady, "session two", nil)
	if err := second.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	recs := readRecords(t, path)
	if len(recs) != 2 {
		t.Fatalf("got %d records across two sessions, want 2", len(recs))
	}
	if recs[0].Detail != "session one" || recs[1].Detail != "session two" {
		t.Errorf("details = %q, %q", recs[0].Detail, recs[1].Detail)
	}
}
