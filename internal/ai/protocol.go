package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Protocol identifies an LLM wire-format family. Every configured provider is
// normalized onto one of these three so any OpenAI-, Anthropic-, or
// Google-compatible endpoint works through a single code path.
type Protocol string

const (
	ProtocolOpenAI    Protocol = "openai"
	ProtocolAnthropic Protocol = "anthropic"
	ProtocolGoogle    Protocol = "google"
)

// anthropicAPIVersion is the required anthropic-version header value.
const anthropicAPIVersion = "2023-06-01"

// protocolForProvider maps a configured provider onto its wire protocol.
// nvidia and the legacy OpenAI-compatible aliases all speak the OpenAI format.
func protocolForProvider(provider string) Protocol {
	switch provider {
	case "anthropic_compatible":
		return ProtocolAnthropic
	case "google_compatible":
		return ProtocolGoogle
	default: // nvidia, openai_compatible, minimax, opengateway, openai
		return ProtocolOpenAI
	}
}

// --- Anthropic wire format ---

type anthropicRequest struct {
	Model       string             `json:"model"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// --- Google (Gemini) wire format ---

type googleRequest struct {
	SystemInstruction *googleContent  `json:"system_instruction,omitempty"`
	Contents          []googleContent `json:"contents"`
	GenerationConfig  googleGenConfig `json:"generationConfig"`
}

type googleContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []googlePart `json:"parts"`
}

type googlePart struct {
	Text string `json:"text"`
}

type googleGenConfig struct {
	Temperature     float64 `json:"temperature"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
}

type googleResponse struct {
	Candidates []struct {
		Content struct {
			Parts []googlePart `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

// buildHTTPRequest constructs the protocol-specific HTTP request for a chat
// completion. messages follow the OpenAI role convention (system/user/assistant).
func (nc *NvidiaClient) buildHTTPRequest(messages []Message, temperature float64, maxTokens int) (*http.Request, error) {
	switch nc.protocol {
	case ProtocolAnthropic:
		return nc.buildAnthropicRequest(messages, temperature, maxTokens)
	case ProtocolGoogle:
		return nc.buildGoogleRequest(messages, temperature, maxTokens)
	default:
		return nc.buildOpenAIRequest(messages, temperature, maxTokens)
	}
}

// parseReply extracts the assistant text from a protocol-specific response body.
// It returns an empty string (no error) when the provider returned no content.
func (nc *NvidiaClient) parseReply(body []byte) (string, error) {
	switch nc.protocol {
	case ProtocolAnthropic:
		return parseAnthropicReply(body)
	case ProtocolGoogle:
		return parseGoogleReply(body)
	default:
		return parseOpenAIReply(body)
	}
}

func (nc *NvidiaClient) buildOpenAIRequest(messages []Message, temperature float64, maxTokens int) (*http.Request, error) {
	reqBody := ChatCompletionRequest{
		Model:       nc.model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   maxTokens,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := nc.baseURL
	if endpoint == "" {
		endpoint = endpointNvidia
	} else {
		endpoint = normalizeOpenAIBaseURL(endpoint)
	}

	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if nc.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+nc.apiKey)
	}
	return httpReq, nil
}

func (nc *NvidiaClient) buildAnthropicRequest(messages []Message, temperature float64, maxTokens int) (*http.Request, error) {
	if nc.baseURL == "" {
		return nil, fmt.Errorf("ai.base_url is required for anthropic_compatible provider")
	}

	var system []string
	var msgs []anthropicMessage
	for _, m := range messages {
		switch m.Role {
		case "system":
			system = append(system, m.Content)
		case "user", "assistant":
			msgs = append(msgs, anthropicMessage{Role: m.Role, Content: m.Content})
		}
	}

	reqBody := anthropicRequest{
		Model:       nc.model,
		System:      strings.Join(system, "\n"),
		Messages:    msgs,
		MaxTokens:   maxTokens,
		Temperature: temperature,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", nc.baseURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", nc.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)
	return httpReq, nil
}

func (nc *NvidiaClient) buildGoogleRequest(messages []Message, temperature float64, maxTokens int) (*http.Request, error) {
	if nc.baseURL == "" {
		return nil, fmt.Errorf("ai.base_url is required for google_compatible provider")
	}

	var sysParts []string
	var contents []googleContent
	for _, m := range messages {
		switch m.Role {
		case "system":
			sysParts = append(sysParts, m.Content)
		case "user":
			contents = append(contents, googleContent{Role: "user", Parts: []googlePart{{Text: m.Content}}})
		case "assistant":
			contents = append(contents, googleContent{Role: "model", Parts: []googlePart{{Text: m.Content}}})
		}
	}

	reqBody := googleRequest{
		Contents:         contents,
		GenerationConfig: googleGenConfig{Temperature: temperature, MaxOutputTokens: maxTokens},
	}
	if len(sysParts) > 0 {
		reqBody.SystemInstruction = &googleContent{Parts: []googlePart{{Text: strings.Join(sysParts, "\n")}}}
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := strings.TrimRight(nc.baseURL, "/") + "/models/" + url.PathEscape(nc.model) +
		":generateContent?key=" + url.QueryEscape(nc.apiKey)
	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	return httpReq, nil
}

// normalizeOpenAIBaseURL ensures the base URL points to the chat completions
// endpoint. Users may configure either the full path
// (https://api.openai.com/v1/chat/completions) or just the base
// (https://api.openai.com/v1) — both work.
func normalizeOpenAIBaseURL(raw string) string {
	u := strings.TrimRight(raw, "/")
	if strings.HasSuffix(u, "/chat/completions") {
		return u
	}
	return u + "/chat/completions"
}

func parseOpenAIReply(body []byte) (string, error) {
	// Use a streaming decoder so trailing content after the first JSON value
	// (e.g. a stray "data: [DONE]" line from a half-streaming server) doesn't
	// cause a spurious "invalid character 'd' after top-level value" error.
	dec := json.NewDecoder(bytes.NewReader(body))
	var resp ChatCompletionResponse
	if err := dec.Decode(&resp); err != nil {
		// Some OpenAI-compatible servers ignore "stream": false and return a
		// Server-Sent Events stream ("data: {...}\n\ndata: [DONE]"). Fall back
		// to reassembling the streamed deltas before giving up.
		if text, ok := parseOpenAISSE(body); ok {
			return text, nil
		}
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", nil
	}
	return resp.Choices[0].Message.Content, nil
}

// openAIStreamChunk is a single SSE delta frame from a streaming chat response.
type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// parseOpenAISSE reassembles the assistant text from a Server-Sent Events
// stream body. Returns ok=false when the body isn't recognizable as SSE.
func parseOpenAISSE(body []byte) (string, bool) {
	if !strings.Contains(string(body), "data:") {
		return "", false
	}
	var sb strings.Builder
	sawChunk := false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content != "" {
				sb.WriteString(c.Delta.Content)
				sawChunk = true
			} else if c.Message.Content != "" {
				sb.WriteString(c.Message.Content)
				sawChunk = true
			}
		}
	}
	return sb.String(), sawChunk
}

func parseAnthropicReply(body []byte) (string, error) {
	var resp anthropicResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	var sb strings.Builder
	for _, c := range resp.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String(), nil
}

func parseGoogleReply(body []byte) (string, error) {
	var resp googleResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if len(resp.Candidates) == 0 {
		return "", nil
	}
	var sb strings.Builder
	for _, p := range resp.Candidates[0].Content.Parts {
		sb.WriteString(p.Text)
	}
	return sb.String(), nil
}
