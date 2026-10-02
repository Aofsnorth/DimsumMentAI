package evidence

// Test-support constructors.
//
// The logger's buffer, closed channel and enabled flag are deliberately
// unexported: they are the writer's internals, and a black-box test in
// tests/evidence cannot build a Logger with a full buffer and no reader without
// a way in. These constructors are that way in — the smallest surface that lets
// an external test drive the drop path without also exposing the fields.

// NewBlockedForTest returns an enabled logger with a buffer of the given size
// and no writer draining it, so a record past the buffer is dropped rather than
// written. A size below 1 is raised to 1.
func NewBlockedForTest(buffer int) *Logger {
	if buffer < 1 {
		buffer = 1
	}
	return &Logger{
		records: make(chan Record, buffer),
		closed:  make(chan struct{}),
		enabled: true,
	}
}
