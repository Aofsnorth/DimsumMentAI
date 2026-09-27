// Package jev is a client for TypeSafe AI's "Jev" System One model.
//
// Jev is not a text generator and cannot be swapped in for the LLM. You hand it
// a blob of program state plus a set of typed questions, and it answers all of
// them in one parallel pass with calibrated probabilities — a yes/no
// probability (a "noul"), a choice from a named set, or a score on a rubric. It
// emits no prose at all.
//
// That is exactly the shape of the AGI reflex layer. "Should I run?" and "what
// should I do next?" are decisions, not sentences, and today the bot answers
// them with hardcoded Go thresholds — which cannot weigh anything the author
// failed to anticipate. Jev can.
//
// The division of labour that falls out of this:
//
//	Jev    — System One. Fast, cheap, structured. Should I flee? Is this moment
//	         worth speaking? Which activity next? No text is produced.
//	LLM    — System Two. Slow, expensive, generative. The actual sentence the
//	         bot says, and any free-form action tag.
//
// TypeSafe names the split after Kahneman's two systems, and the analogy holds:
// the reflex layer was renting System Two to answer questions that never needed
// prose in the first place.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultEndpoint is TypeSafe's native host. Change it only if you are routing
// through a gateway (Vercel AI Gateway, LiteLLM's /typesafe pass-through,
// OpenRouter) — the request body is the same shape everywhere, and only the
// host, the key and the model name change.
const DefaultEndpoint = "https://api.typesafe.ai"

// DefaultModel asks the service to resolve to the current Jev release. The
// resolved version comes back in the response, which is what makes pinning a
// number like "1.13.0" a deliberate choice rather than a default.
const DefaultModel = "jev-latest"

// evaluatePath is where the request goes. Appended to a configured base URL
// when it is not already there. This is the native TypeSafe path; gateways
// (Vercel AI Gateway, OpenRouter) mount the same body shape under their own
// prefixes, so a configured base_url that already ends in a path is left alone
// by New.
const evaluatePath = "/v1/systemone"

// defaultTimeout is the fallback when no timeout is configured.
const defaultTimeout = 5 * time.Second

// Environment variables. The model name and the key are read from the
// environment rather than the config file: one is a secret that must never be
// committed, and the other changes per gateway (Vercel AI Gateway serves this
// model as "typesafe-ai/jev", OpenRouter as "typesafe/jev-1.13", the native API
// as "jev-latest"). Switching gateway is therefore a change to .env rather than
// to a file that is shared or checked in.
const (
	// EnvAPIKey holds the bearer key. Absent means Jev stays off.
	EnvAPIKey = "TYPESAFE_API_KEY"
	// EnvModel overrides the model alias. Absent falls back to DefaultModel.
	EnvModel = "JEV_MODEL"
)

// FromEnv builds a client from the environment. Returns a client with
// Available=false when no key is set, which is a supported configuration: the
// bot runs on its hardcoded thresholds and Jev is simply not in the loop.
func FromEnv(endpoint string) *Client {
	return New(endpoint, os.Getenv(EnvModel), os.Getenv(EnvAPIKey))
}

// QuestionType names one of Jev's three primitives.
type QuestionType string

const (
	// TypeNoul is a yes/no probability. TypeSafe's name for the boolean type.
	// The answer is a probability, not a decision — the caller thresholds it,
	// which is deliberate: the threshold stays in your hands.
	TypeNoul QuestionType = "noul"
	// TypeChoice picks one option from a set, returning the top pick plus the
	// full distribution and a confidence figure.
	TypeChoice QuestionType = "choice"
	// TypeScore places something on a rubric.
	TypeScore QuestionType = "score"
)

// NoulQuestion asks a yes/no question about the state.
type NoulQuestion struct {
	Type         QuestionType `json:"type"`
	Instructions string       `json:"instructions"`
}

// ChoiceQuestion asks which of the named options best fits the state.
type ChoiceQuestion struct {
	Type         QuestionType      `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

// Request is one evaluate call. Every question is answered in the same
// parallel pass, so several related questions cost no more round trips than one.
type Request struct {
	State     string                     `json:"state"`
	Model     string                     `json:"model,omitempty"`
	Questions map[string]json.RawMessage `json:"questions"`
}

// Answer is the shape of one entry under "answers". Jev echoes the primitive
// type, so the same struct covers all three; only the matching field is set.
type Answer struct {
	Type   QuestionType  `json:"type"`
	Noul   *float64      `json:"noul,omitempty"`
	Choice *ChoiceAnswer `json:"choice,omitempty"`
	Score  *ScoreAnswer  `json:"score,omitempty"`
}

// ChoiceAnswer carries the winning option and the full probability
// distribution. The wire field is "probabilities" — the first client sent
// "distribution", which decoded fine into an always-empty map and silently hid
// every per-option probability.
type ChoiceAnswer struct {
	Choice        string             `json:"choice"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

// ScoreAnswer is a position on the question's rubric.
type ScoreAnswer struct {
	Score         float64            `json:"score"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

// Usage is what the call cost. Only input tokens are billed; output is free.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Response is the evaluate reply.
type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

// Client talks to the Jev evaluate endpoint.
type Client struct {
	endpoint   string
	model      string
	apiKey     string
	httpClient *http.Client
	// Available is false when no key was configured. Callers check it rather
	// than making a request that would fail: the AGI layer treats Jev as an
	// upgrade, not a requirement, and has to keep working without it.
	Available bool
}

// New builds a client. An empty apiKey yields a client with Available=false
// rather than an error, so "no key" is a configuration state the bot can run
// in rather than a crash at startup.
func New(endpoint, model, apiKey string) *Client {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	// Accept either a host or a full endpoint. Gateways are configured as a base
	// URL while the native API is usually pasted with its path already attached,
	// and making the user strip one or the other is a footgun that shows up as a
	// 404 with no obvious cause.
	endpoint = strings.TrimRight(endpoint, "/")
	if !strings.HasSuffix(endpoint, evaluatePath) {
		endpoint += evaluatePath
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = DefaultModel
	}
	return &Client{
		endpoint:   endpoint,
		model:      model,
		apiKey:     strings.TrimSpace(apiKey),
		httpClient: &http.Client{Timeout: defaultTimeout},
		Available:  strings.TrimSpace(apiKey) != "",
	}
}

// Endpoint reports the resolved base URL, for startup logging.
func (c *Client) Endpoint() string {
	if c == nil {
		return ""
	}
	return c.endpoint
}

// ModelName reports the resolved model alias, for startup logging.
func (c *Client) ModelName() string {
	if c == nil {
		return ""
	}
	return c.model
}

// SetTimeout bounds one evaluate call. Jev answers in tens to hundreds of
// milliseconds, so this is a guard against a hung endpoint rather than a tuning
// knob for speed.
func (c *Client) SetTimeout(d time.Duration) {
	if c == nil || d <= 0 {
		return
	}
	c.httpClient.Timeout = d
}

// ErrUnavailable is returned when Jev was not configured. It is a distinct
// error so callers can fall back quietly instead of logging a failure for a
// state the user chose.
var ErrUnavailable = errors.New("jev: no API key configured")

// Evaluate sends state and a set of questions in one call.
func (c *Client) Evaluate(ctx context.Context, state string, questions map[string]json.RawMessage) (*Response, error) {
	if c == nil || !c.Available {
		return nil, ErrUnavailable
	}
	if len(questions) == 0 {
		return nil, errors.New("jev: no questions")
	}

	body, err := json.Marshal(Request{State: state, Model: c.model, Questions: questions})
	if err != nil {
		return nil, fmt.Errorf("jev: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("jev: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jev: evaluate: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// The body often carries the request id, which support needs. Keep it
		// short so it fits in a log line.
		snippet := make([]byte, 256)
		n, _ := resp.Body.Read(snippet)
		return nil, fmt.Errorf("jev: evaluate returned %s: %s", resp.Status, strings.TrimSpace(string(snippet[:n])))
	}

	var out Response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("jev: decode response: %w", err)
	}
	return &out, nil
}

// Noul reads a yes/no answer. The second return is false when the question is
// missing or came back as a different primitive, so a caller never silently
// treats a missing answer as "no" or "yes".
func (r *Response) Noul(question string) (probability float64, ok bool) {
	if r == nil {
		return 0, false
	}
	answer, found := r.Answers[question]
	if !found || answer.Noul == nil {
		return 0, false
	}
	return *answer.Noul, true
}

// Choice reads a choice answer.
func (r *Response) Choice(question string) (choice string, confidence float64, ok bool) {
	if r == nil {
		return "", 0, false
	}
	answer, found := r.Answers[question]
	if !found || answer.Choice == nil {
		return "", 0, false
	}
	if answer.Choice.Confidence != nil {
		confidence = *answer.Choice.Confidence
	}
	return answer.Choice.Choice, confidence, true
}
