package chat_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/chat"
)

// These cover the removal of the fixed sentences.
//
// "Siap, aku berhenti ikutin." was not a style problem. It was the same line for
// every player, every time, regardless of what the bot had actually done — and
// because it was a string literal, nothing in the reply path could notice the
// bot had stopped following for a different reason, or not at all. The state
// change itself stays in code; only the sentence about it comes from the model
// now, which is what makes the two separable and testable.

// stubModel answers every request with a fixed reply and records the prompts it
// was given.
type stubModel struct {
	server  *httptest.Server
	reply   string
	prompts chan string
}

func newStubModel(t *testing.T, reply string) *stubModel {
	t.Helper()

	m := &stubModel{reply: reply, prompts: make(chan string, 8)}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		if len(req.Messages) > 0 {
			select {
			case m.prompts <- req.Messages[len(req.Messages)-1].Content:
			default:
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": m.reply}},
			},
			"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	t.Cleanup(m.server.Close)
	return m
}

func (m *stubModel) newBot(t *testing.T) *bot.Bot {
	t.Helper()
	return &bot.Bot{
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		Name:                "Luna",
		AiClient:            ai.NewLLMClient("openai_compatible", "test-model", m.server.URL),
		RecentBotMessages:   make(map[string]time.Time),
		RecentStatusReports: make(map[string]time.Time),
	}
}

// The model decides the sentence. A hardcoded string could not pass this.
func TestStateReplyComesFromTheModel(t *testing.T) {
	t.Parallel()

	m := newStubModel(t, "Oke, aku diem di sini ya.")
	got := chat.StateReplyTextForTest(m.newBot(t), "Synterix", "berhenti ikutin aku", "stopped following")

	if got != "Oke, aku diem di sini ya." {
		t.Errorf("state reply = %q, want the model's own sentence %q", got, "Oke, aku diem di sini ya.")
	}
}

// The change has to reach the model, or the sentence is written about nothing.
// This is the claim the old literal made for free.
func TestStateReplyTellsTheModelWhatChanged(t *testing.T) {
	t.Parallel()

	m := newStubModel(t, "Siap.")
	chat.StateReplyTextForTest(m.newBot(t), "Synterix", "berhenti serang", "You stopped attacking")

	select {
	case prompt := <-m.prompts:
		if !strings.Contains(prompt, "stopped attacking") {
			t.Errorf("the prompt does not say what changed.\ngot: %s", prompt)
		}
		if !strings.Contains(prompt, "berhenti serang") {
			t.Errorf("the prompt does not carry the player's own words.\ngot: %s", prompt)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the model was never asked for a state-change reply")
	}
}

// Nothing is sent when the model declines. Silence is the correct outcome here:
// the bot has already stopped following, and a canned reassurance it did not
// mean would be worse than saying nothing.
func TestStateReplySendsNothingWhenTheModelDeclines(t *testing.T) {
	t.Parallel()

	m := newStubModel(t, "<silent/>")
	if got := chat.StateReplyTextForTest(m.newBot(t), "Synterix", "berhenti ikutin aku", "stopped following"); got != "" {
		t.Errorf("a declined state change produced %q, want no message", got)
	}
}

// An action tag in this reply must not become a task, and must not reach the
// player as text either. Nothing on this path executes anything.
func TestStateReplyNeverLeaksAnActionTag(t *testing.T) {
	t.Parallel()

	m := newStubModel(t, "Oke. <action>follow</action>")
	got := chat.StateReplyTextForTest(m.newBot(t), "Synterix", "berhenti ikutin aku", "stopped following")

	if strings.Contains(got, "<action>") {
		t.Errorf("the action tag reached the player: %q", got)
	}
	if got != "Oke." {
		t.Errorf("state reply = %q, want the tag stripped and the sentence kept", got)
	}
}

// A bot with no model still stops following — and says nothing about it, rather
// than falling back to the literal this replaced.
func TestStateReplyNeedsNoModelToChangeState(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		RecentBotMessages:   make(map[string]time.Time),
		RecentStatusReports: make(map[string]time.Time),
	}
	if got := chat.StateReplyTextForTest(b, "Synterix", "berhenti ikutin aku", "stopped following"); got != "" {
		t.Errorf("a bot with no AI client produced %q, want nothing", got)
	}
}
