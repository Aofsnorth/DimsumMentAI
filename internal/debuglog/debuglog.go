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
)

var (
	mu      sync.Mutex
	enabled bool
)

// SetEnabled turns session NDJSON file logging on when log_level is debug in config.
func SetEnabled(on bool) {
	mu.Lock()
	enabled = on
	mu.Unlock()
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

// Log appends one NDJSON debug line when debug mode is enabled in config.
func Log(hypothesisID, location, message string, data map[string]any) {
	mu.Lock()
	defer mu.Unlock()
	if !enabled {
		return
	}

	if data == nil {
		data = map[string]any{}
	}
	entry := map[string]any{
		"hypothesisId": hypothesisID,
		"location":     location,
		"message":      message,
		"data":         data,
		"timestamp":    time.Now().UnixMilli(),
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	path := logPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
	_ = f.Close()
}
