package jev_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bedrock-ai/internal/jev"
)

// The Jev response shape is the one thing in this integration that is decided by
// somebody else's server, and it is not identical across deployments. The
// documented form nests the payload — "choice": {"choice": "wander", ...} — and
// the gateway this bot actually runs against answers with the flat form,
// "choice": "wander", with confidence and the distribution as siblings.
//
// The difference is invisible until it is fatal. A client that only understands
// the nested form decodes every reply into an error, and because the AGI layer
// degrades quietly on a Jev failure, the symptom is not a crash: it is a bot
// that joins, idles, wanders, and never once thinks. The error names the field,
// but nothing anywhere records what the server actually sent, so the log says
// "cannot unmarshal string" and leaves the reader to guess.

// serve answers every Evaluate with a fixed body and returns the client for it.
func serve(t *testing.T, body string) *jev.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return jev.New(srv.URL, "", "sk-test")
}

// TestChoiceAcceptsTheFlatWireShape is the live regression: the real endpoint
// sends the winning option as a bare string, and a client that rejects it never
// gets to make a single decision.
func TestChoiceAcceptsTheFlatWireShape(t *testing.T) {
	t.Parallel()

	c := serve(t, `{
		"model":"jev-latest",
		"answers":{
			"activity":{"type":"choice","choice":"wander","confidence":0.71,"probabilities":{"wander":0.71,"rest":0.2}}
		}
	}`)

	resp, err := c.Evaluate(context.Background(), "state", jev.BuildActivityQuestion(nil))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	choice, confidence, ok := resp.Choice(jev.QActivity)
	if !ok {
		t.Fatal("Choice() reported no answer for a flat choice")
	}
	if choice != jev.ActivityWander {
		t.Errorf("Choice() = %q, want %q", choice, jev.ActivityWander)
	}
	if confidence != 0.71 {
		t.Errorf("confidence = %v, want the sibling value 0.71", confidence)
	}
}

// TestChoiceAcceptsTheNestedWireShape is the other half: a tolerant decoder is
// only worth having if it does not cost the documented form.
func TestChoiceAcceptsTheNestedWireShape(t *testing.T) {
	t.Parallel()

	c := serve(t, `{
		"answers":{"activity":{"type":"choice","choice":{"choice":"mine","confidence":0.6}}}
	}`)

	resp, err := c.Evaluate(context.Background(), "state", jev.BuildActivityQuestion(nil))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	choice, confidence, ok := resp.Choice(jev.QActivity)
	if !ok || choice != "mine" || confidence != 0.6 {
		t.Errorf("Choice() = %q, %v, %v; want mine, 0.6, true", choice, confidence, ok)
	}
}

// TestChoiceAcceptsANestedObjectWithFlatSiblings covers the mixed shape, which is
// what a gateway that reshapes only the payload field produces.
func TestChoiceAcceptsANestedObjectWithFlatSiblings(t *testing.T) {
	t.Parallel()

	c := serve(t, `{
		"answers":{"activity":{"type":"choice","choice":"mine","probabilities":{"mine":0.9},"distribution":{"mine":0.9}}}
	}`)

	resp, err := c.Evaluate(context.Background(), "state", jev.BuildActivityQuestion(nil))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	choice, _, ok := resp.Choice(jev.QActivity)
	if !ok || choice != "mine" {
		t.Errorf("Choice() = %q, %v; want mine, true", choice, ok)
	}
	if got := resp.Probabilities(jev.QActivity); got["mine"] != 0.9 {
		t.Errorf("Probabilities() = %v, want the distribution to survive the flat form", got)
	}
}

// TestNoulAndScoreSurviveTheFlatForm covers the other two primitives. Only one
// of them has ever been seen failing, but they travel together, and a decoder
// that handles one shape of one primitive is a decoder that fails the same way
// on the next field the server reshapes.
func TestNoulAndScoreSurviveTheFlatForm(t *testing.T) {
	t.Parallel()

	c := serve(t, `{
		"answers":{
			"danger":{"type":"noul","noul":0.83},
			"mood":{"type":"score","score":0.4}
		}
	}`)

	resp, err := c.Evaluate(context.Background(), "state", jev.BuildReflexQuestions())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if p, ok := resp.Noul(jev.QDanger); !ok || p != 0.83 {
		t.Errorf("Noul(danger) = %v, %v; want 0.83, true", p, ok)
	}
	if s, ok := resp.Score("mood"); !ok || s != 0.4 {
		t.Errorf("Score(mood) = %v, %v; want 0.4, true", s, ok)
	}
}

// TestOneBadAnswerDoesNotDiscardTheWholeReply is the difference between a bot
// that occasionally misreads a field and a bot that stops thinking. Every
// question rides in one reply, so a single unparseable entry must not take the
// other answers down with it — and a question with no usable answer is already a
// supported state, because the accessors report "not answered" rather than
// guessing.
func TestOneBadAnswerDoesNotDiscardTheWholeReply(t *testing.T) {
	t.Parallel()

	c := serve(t, `{
		"answers":{
			"danger":{"type":"noul","noul":0.83},
			"activity":{"type":"choice","choice":{"unexpected":"object"}}
		}
	}`)

	resp, err := c.Evaluate(context.Background(), "state", jev.BuildReflexQuestions())
	if err != nil {
		t.Fatalf("Evaluate() error = %v; one malformed answer must not fail the tick", err)
	}
	if p, ok := resp.Noul(jev.QDanger); !ok || p != 0.83 {
		t.Errorf("Noul(danger) = %v, %v; want the good answer to survive", p, ok)
	}
	if _, _, ok := resp.Choice(jev.QActivity); ok {
		t.Error("Choice() invented an answer from a malformed one")
	}
}

// TestADecodeFailureShowsWhatTheServerSent closes the loop that made this
// expensive: the old error named the field it choked on and nothing else, so
// the only way to learn the actual shape was to attach a debugger to a live
// bot. A short excerpt of the body belongs in the error.
func TestADecodeFailureShowsWhatTheServerSent(t *testing.T) {
	t.Parallel()

	c := serve(t, `{"answers":"this is not a map at all"}`)

	_, err := c.Evaluate(context.Background(), "state", jev.BuildReflexQuestions())
	if err == nil {
		t.Fatal("Evaluate() accepted a reply whose answers are not a map")
	}
	if !strings.Contains(err.Error(), "not a map at all") {
		t.Errorf("error = %q, want it to quote what the server actually sent", err)
	}
}
