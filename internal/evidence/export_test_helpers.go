// Test-support constructors.
//
// The logger's buffer, closed channel and enabled flag are deliberately
// unexported: they are the writer's internals, and a black-box test in
// tests/evidence cannot build a Logger with a full buffer and no reader without
// a way in. These constructors are that way in — the smallest surface that lets
// an external test drive the drop path without also exposing the fields.

package evidence

import (
	"os"
	"path/filepath"
)

// NewBlockedForTest returns an enabled logger with a buffer of the given size
// and no writer draining it, so a record past the buffer is dropped rather than
// written. A size below 1 is raised to 1.
func NewBlockedForTest(buffer int) *Logger {
	if buffer < 1 {
		buffer = 1
	}
	l := &Logger{
		records: make(chan Record, buffer),
		closed:  make(chan struct{}),
	}
	// Stored rather than assigned to the field: enabled is an atomic.Bool, so
	// there is no bool literal to write into it any more.
	l.enabled.Store(true)
	return l
}

// NewRotationDoomedForTest returns an enabled logger whose next rotation is
// guaranteed to fail at the rename step, with the current file already closed
// by the time it gets there.
//
// It reproduces a state nothing a caller can produce on purpose: rotation closes
// the file, then renames it, and on Windows the rename fails whenever anything
// else — an indexer, an antivirus scanner, a backup tool — has the file open
// again in between. The bug was not that the rename fails. It was what the code
// did next: returned, leaving the logger holding a closed handle with its size
// counter still over the cap, so every record for the rest of the session took
// the same path and was lost while Summary still reported the logger healthy.
//
// The destination is made un-renameable-over by creating a non-empty directory
// where the rolled file goes: Remove fails because the directory is not empty,
// and Rename fails because it will not replace a directory with a file.
func NewRotationDoomedForTest(buffer int) (*Logger, error) {
	if buffer < 1 {
		buffer = 1
	}
	dir, err := os.MkdirTemp("", "evidence-rotation-")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		return nil, err
	}

	// A non-empty directory where events.jsonl.1 belongs.
	blocker := path + ".1"
	if err := os.Mkdir(blocker, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(blocker, "occupied"), []byte("x"), 0o644); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}

	l := &Logger{
		records: make(chan Record, buffer),
		closed:  make(chan struct{}),
		file:    f,
		bytes:   maxFileBytes, // already at the cap, so the next write rotates
	}
	l.enabled.Store(true)
	// The writer goroutine, as Open starts it. Without it the records only ever
	// fill the channel and the rotation this fixture exists to provoke never
	// happens.
	l.wg.Add(1)
	go l.run()
	return l, nil
}

// CleanupForTest releases the temporary directory a doomed-rotation logger
// created. The logger itself is left alone; a test that wants to assert on it
// does so first.
func CleanupForTest(l *Logger) {
	if l == nil || l.file == nil {
		return
	}
	if name := l.file.Name(); name != "" {
		_ = os.RemoveAll(filepath.Dir(name))
	}
}
