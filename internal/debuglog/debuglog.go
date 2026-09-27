// Package debuglog writes optional session diagnostics as NDJSON.
//
// It used to open, write and close the file on every call while holding a
// global mutex, and it was called from inside the packet read loop. On a
// laggy server the read-gap logger fires on every single packet, so each one
// paid a file open/close under a lock that every other goroutine also needed.
// That is a feedback loop — the laggier the server, the more disk I/O, the
// laggier the client — and the client gets dropped for being unresponsive.
//
// The file is now opened once and kept open, so a log line is a single append.
package debuglog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	logDir  = "logs"
	logFile = "debug-090ce4.log"

	// minWriteInterval bounds how often a line is actually written. Callers in
	// hot paths (the packet read loop) can call Log freely; the cost is capped
	// here rather than at every call site.
	minWriteInterval = 250 * time.Millisecond
)

var (
	mu      sync.Mutex
	enabled bool
	file    *os.File
	lastLog time.Time
)

// SetEnabled turns session NDJSON file logging on when log_level is debug in
// config. Turning it off closes the file so the handle is not held for a
// session that no longer writes to it.
func SetEnabled(on bool) {
	mu.Lock()
	defer mu.Unlock()
	enabled = on
	// Reset the write window so enabling debug always lets the next call
	// through. Without this, a session that enables logging just after another
	// one wrote a line has its first 250ms of diagnostics silently dropped.
	lastLog = time.Time{}
	if !on && file != nil {
		_ = file.Close()
		file = nil
	}
}

// Enabled reports whether debug file logging is active.
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return enabled
}

func logPath() string {
	return filepath.Join(logDir, logFile)
}

// ensureFile opens the log once and reuses the handle. Callers must hold mu.
func ensureFile() (*os.File, error) {
	if file != nil {
		return file, nil
	}
	path := logPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	file = f
	return f, nil
}

// Log appends one NDJSON debug line when debug mode is enabled in config.
//
// Writes inside minWriteInterval of the previous one are dropped, which is what
// stops a hot caller from turning the logger into the bottleneck it used to be.
func Log(hypothesisID, location, message string, data map[string]any) {
	now := time.Now()

	mu.Lock()
	defer mu.Unlock()
	if !enabled {
		return
	}
	if now.Sub(lastLog) < minWriteInterval {
		return
	}
	lastLog = now

	if data == nil {
		data = map[string]any{}
	}
	entry := map[string]any{
		"hypothesisId": hypothesisID,
		"location":     location,
		"message":      message,
		"data":         data,
		"timestamp":    now.UnixMilli(),
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	f, err := ensureFile()
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
}
