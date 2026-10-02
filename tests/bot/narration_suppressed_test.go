package bot_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/event"
)

// Suppressing a routine success has to happen before the model is consulted, not
// after — otherwise the bot still pays for a reply it throws away, and the
// silence becomes an accident of timing rather than a decision.
//
// This proves the ordering rather than the policy. ShouldNarrateStatus is a
// pure function and a test of it cannot tell whether the call site honours it;
// pointing the client at a local server and counting requests can.

// countingModel is a stand-in for the LLM endpoint that records how many times
// it was asked.
type countingModel struct {
	server  *httptest.Server
	calls   atomic.Int64
	replies chan string
}

func newCountingModel(t *testing.T) *countingModel {
	t.Helper()

	m := &countingModel{replies: make(chan string, 8)}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.calls.Add(1)
		body := "Dapet kayunya, stok aman."
		select {
		case m.replies <- body:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": body}},
			},
			"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	t.Cleanup(m.server.Close)
	return m
}

func (m *countingModel) client(t *testing.T) *ai.NvidiaClient {
	t.Helper()
	return ai.NewLLMClient("openai_compatible", "test-model", m.server.URL)
}

func newReportingBot(client *ai.NvidiaClient) *bot.Bot {
	return &bot.Bot{
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		AiClient: client,
		// SendSafeChat records what the bot said so it can ignore its own echo
		// coming back from the server. Without these the maps are nil and the
		// send path panics on the first chat line.
		RecentBotMessages:   make(map[string]time.Time),
		RecentStatusReports: make(map[string]time.Time),
	}
}

// TestARoutineSuccessNeverReachesTheModel is the ordering claim. The bot went
// quiet, and the only way to know it was quiet because it decided to be — rather
// than quiet because the reply was dropped later — is to show the model was
// never asked.
func TestARoutineSuccessNeverReachesTheModel(t *testing.T) {
	t.Parallel()

	model := newCountingModel(t)
	b := newReportingBot(model.client(t))

	b.ReportActionStatus("Arthenyxx", event.ActionStatus{
		Action: "chop", Item: "log", Count: 10, Success: true,
	})

	// The report is dispatched on its own goroutine, so give it long enough to
	// have arrived had it been dispatched at all.
	time.Sleep(300 * time.Millisecond)

	if got := model.calls.Load(); got != 0 {
		t.Errorf("the model was asked %d times for a routine gather success; "+
			"the reply was going to be thrown away", got)
	}
}

// TestAFailureStillReachesTheModel is the other half: silence must not become a
// mute button. A failure is the one thing the player is waiting to hear about.
func TestAFailureStillReachesTheModel(t *testing.T) {
	t.Parallel()

	model := newCountingModel(t)
	b := newReportingBot(model.client(t))

	b.ReportActionStatus("Arthenyxx", event.ActionStatus{
		Action: "chop", Item: "log", Success: false, Error: "tidak ada pohon",
	})

	deadline := time.After(3 * time.Second)
	for model.calls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("a gather failure produced no reply: the player is never told what went wrong")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// TestACraftStillReachesTheModel keeps the suppression off the results a player
// actually asked for.
func TestACraftStillReachesTheModel(t *testing.T) {
	t.Parallel()

	model := newCountingModel(t)
	b := newReportingBot(model.client(t))

	b.ReportActionStatus("Arthenyxx", event.ActionStatus{
		Action: "craft", Item: "crafting_table", Count: 1, Success: true,
	})

	deadline := time.After(3 * time.Second)
	for model.calls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("a crafted item produced no reply: that is the result the player was waiting for")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
