package jev_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"bedrock-ai/internal/jev"
)

// TestEvaluateSendsTheDocumentedRequestShape pins the wire format. Jev is not
// OpenAI-compatible, so the usual /chat/completions habit is wrong here: the
// body is {state, model, questions} against /evaluate, and a bearer key in the
// Authorization header.
func TestEvaluateSendsTheDocumentedRequestShape(t *testing.T) {
	t.Parallel()

	var gotPath, gotAuth, gotContentType string
	var gotBody jev.Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{}}`))
	}))
	defer srv.Close()

	c := jev.New(srv.URL, "jev-latest", "sk-test")
	if _, err := c.Evaluate(context.Background(), "hp 4/20", jev.BuildReflexQuestions()); err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	if gotPath != "/evaluate" {
		t.Errorf("posted to %q, want /evaluate", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer sk-test")
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody.State != "hp 4/20" {
		t.Errorf("state = %q, want the caller's text", gotBody.State)
	}
	if gotBody.Model != "jev-latest" {
		t.Errorf("model = %q, want jev-latest", gotBody.Model)
	}
	for _, name := range []string{jev.QDanger, jev.QHungry, jev.QWorthSay} {
		if _, ok := gotBody.Questions[name]; !ok {
			t.Errorf("question %q missing from the request", name)
		}
	}
}

// TestNoulReturnsTheProbabilityNotTheDecision is the property the whole
// integration rests on. Jev answers "how likely", and where to cut that is a
// policy decision that has to stay in the caller's hands.
func TestNoulReturnsTheProbabilityNotTheDecision(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"model":"jev-1.13.0",
			"answers":{
				"danger":{"type":"noul","noul":0.83},
				"worth_speaking":{"type":"noul","noul":0.12}
			},
			"usage":{"input_tokens":392,"output_tokens":65}
		}`))
	}))
	defer srv.Close()

	c := jev.New(srv.URL, "", "sk-test")
	resp, err := c.Evaluate(context.Background(), "state", jev.BuildReflexQuestions())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	p, ok := resp.Noul(jev.QDanger)
	if !ok || p != 0.83 {
		t.Errorf("Noul(danger) = %v, %v; want 0.83, true", p, ok)
	}
	if p, ok := resp.Noul(jev.QWorthSay); !ok || p != 0.12 {
		t.Errorf("Noul(worth_speaking) = %v, %v; want 0.12, true", p, ok)
	}
	// A question the model did not answer must report absent, not zero. Treating
	// a missing answer as "no" would silently switch the brain off on a partial
	// or failed response.
	if _, ok := resp.Noul("not_asked"); ok {
		t.Error("Noul() reported an answer for a question that was never asked")
	}
}

// TestChoiceReadsTheTopPick is the one structured primitive AGI uses to pick an
// activity.
func TestChoiceReadsTheTopPick(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"model":"jev-1.13.0",
			"answers":{
				"activity":{
					"type":"choice",
					"choice":{"choice":"wander","confidence":0.71,"distribution":{"wander":0.71,"rest":0.2,"gather":0.09}}
				}
			}
		}`))
	}))
	defer srv.Close()

	c := jev.New(srv.URL, "", "sk-test")
	// No curriculum: BuildActivityQuestion falls back to its default options, which
	// is the same set the no-argument form used to produce.
	resp, err := c.Evaluate(context.Background(), "state", jev.BuildActivityQuestion(nil))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	choice, confidence, ok := resp.Choice(jev.QActivity)
	if !ok {
		t.Fatal("Choice() reported no answer")
	}
	if choice != jev.ActivityWander {
		t.Errorf("Choice() = %q, want %q", choice, jev.ActivityWander)
	}
	if confidence != 0.71 {
		t.Errorf("confidence = %v, want 0.71", confidence)
	}
}

// TestMissingKeyIsAConfigurationStateNotACrash keeps a user without a key on a
// working bot. Jev is an upgrade, not a requirement.
func TestMissingKeyIsAConfigurationStateNotACrash(t *testing.T) {
	t.Parallel()

	c := jev.New("", "", "")
	if c.Available {
		t.Error("a client with no API key reports itself available")
	}
	if _, err := c.Evaluate(context.Background(), "state", jev.BuildReflexQuestions()); err != jev.ErrUnavailable {
		t.Errorf("Evaluate() with no key = %v, want ErrUnavailable", err)
	}
}

// TestServerErrorIsSurfaced covers the failure a user will actually hit first:
// a rejected key. The status has to reach the log, or the bot looks broken for
// no visible reason.
func TestServerErrorIsSurfaced(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid key"}`))
	}))
	defer srv.Close()

	c := jev.New(srv.URL, "", "sk-bad")
	_, err := c.Evaluate(context.Background(), "state", jev.BuildReflexQuestions())
	if err == nil {
		t.Fatal("Evaluate() succeeded against a 401")
	}
	if got := err.Error(); !contains(got, "401") {
		t.Errorf("error = %q, want the HTTP status included", got)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
