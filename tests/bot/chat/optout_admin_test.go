package chat_test

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"bedrock-ai/internal/bot/chat"
)

// TestOptOutAdminCommandsAreKnown is the regression guard for "!nofollow" and
// "!disengage".
//
// The chat pipeline sends every line beginning with "!" to the admin command
// table before any intent matcher runs, so these two forms — which the intent
// matchers explicitly support — never reached them. They landed on the
// "Unknown admin command" branch and did nothing: the bot kept following the
// player who had just asked it to stop.
func TestOptOutAdminCommandsAreKnown(t *testing.T) {
	t.Parallel()

	for _, cmd := range []string{"!nofollow", "!disengage"} {
		var buf bytes.Buffer
		var mu sync.Mutex
		w := &lockedWriter{mu: &mu, buf: &buf}
		b := newAdminBot()
		b.Logger = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))

		chat.HandleAdminCommand(b, cmd, "Synterix")

		mu.Lock()
		out := buf.String()
		mu.Unlock()
		if strings.Contains(out, "Unknown admin command") {
			t.Errorf("%q was rejected as an unknown admin command:\n%s", cmd, out)
		}
	}
}

type lockedWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}
