// Package ai handles LLM client integration and chat parsing.
package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type ChatCompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens"`
}

type ChatCompletionResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

// NvidiaClient is a generic OpenAI-compatible chat completion client. The name
// is retained for backward compatibility; the same struct now also serves
// Minimax, OpenGateway, and any OpenAI-compatible HTTP endpoint via BaseURL.
type NvidiaClient struct {
	apiKey   string
	model    string
	baseURL  string
	provider string
	client   *http.Client
	History  *MessageHistory
	persona  string
	rules    string
	language string
}

const (
	endpointNvidia  = "https://integrate.api.nvidia.com/v1/chat/completions"
	endpointMinimax = "https://api.minimax.io/v1/chat/completions"
)

// defaultBaseURL returns the canonical endpoint for a known provider. Returns
// empty string when caller must supply BaseURL (opengateway, openai_compatible).
func defaultBaseURL(provider string) string {
	switch provider {
	case "nvidia":
		return endpointNvidia
	case "minimax":
		return endpointMinimax
	}
	return ""
}

// envVarForProvider returns the env var conventionally used to source the
// provider's API key.
func envVarForProvider(provider string) string {
	switch provider {
	case "nvidia":
		return "NVIDIA_API_KEY"
	case "minimax":
		return "MINIMAX_API_KEY"
	}
	return "OPENAI_API_KEY"
}

// NewNvidiaClient constructs a client preconfigured for the NVIDIA NIM endpoint.
// The API key is read from the NVIDIA_API_KEY environment variable.
// Kept for backward compatibility with existing call sites.
func NewNvidiaClient(model string) *NvidiaClient {
	return NewLLMClient("nvidia", model, "")
}

// NewLLMClient constructs an OpenAI-compatible chat-completion client for the
// given provider. The API key is sourced from the provider's conventional
// environment variable (NVIDIA_API_KEY, MINIMAX_API_KEY, or OPENAI_API_KEY).
// baseURL may be empty for known providers (nvidia, minimax); it is required
// for opengateway / openai_compatible.
func NewLLMClient(provider, model, baseURL string) *NvidiaClient {
	if provider == "" {
		provider = "nvidia"
	}
	apiKey := os.Getenv(envVarForProvider(provider))
	if baseURL == "" {
		baseURL = defaultBaseURL(provider)
	}
	if model == "" && provider == "nvidia" {
		model = "nvidia/llama-3.3-nemotron-super-49b-v1"
	}

	return &NvidiaClient{
		apiKey:   apiKey,
		model:    model,
		baseURL:  baseURL,
		provider: provider,
		client:   &http.Client{Timeout: 30 * time.Second},
		History:  NewMessageHistory(20),
		persona:  PromptCharacter,
		rules:    BedrockSystemRules,
		language: "Indonesian",
	}
}

// SetPersona overrides the default persona prompt
func (nc *NvidiaClient) SetPersona(persona string) {
	nc.persona = persona
}

// SetRules overrides the default technical constraint rules
func (nc *NvidiaClient) SetRules(rules string) {
	nc.rules = rules
}

func (nc *NvidiaClient) SetLanguage(language string) {
	if language == "" {
		language = "Indonesian"
	}
	nc.language = language
}

// BuildSystemPrompt constructs the system prompt dynamically with real-time environment variables
func (nc *NvidiaClient) BuildSystemPrompt(botName, botCoords, playerCoords, heldItem, inventoryText string) string {
	prompt := nc.persona + "\n" + nc.rules
	prompt += GetLanguageInstruction(nc.language)

	// Identity & addressing. Without this the model does not know its own
	// in-game username and will echo "@<itself>" back when greeted.
	if botName != "" {
		prompt += "\n\n[IDENTITY]" +
			"\n- Your in-game username is \"" + botName + "\". When someone writes \"" + botName + "\" or \"@" + botName + "\", they are talking TO you — that name refers to YOU, not another player." +
			"\n- Each incoming message is prefixed with the speaker's name in angle brackets, e.g. \"<PlayerName> their message\". The person you are replying to is that <PlayerName>." +
			"\n- When you address someone, use the SPEAKER's name (the <PlayerName>), never your own username \"" + botName + "\"." +
			"\n- NEVER tag, mention, or greet your own username \"" + botName + "\" in a reply. Doing so is always wrong."
	}

	// Coordinates
	if botCoords != "" {
		prompt += "\n\nBot Location: " + botCoords
	}
	if playerCoords != "" {
		prompt += "\nPlayer Location: " + playerCoords
	}

	// Held item
	if heldItem != "" {
		prompt += "\n\nCurrently holding: " + heldItem
	}

	// Inventory
	if inventoryText != "" {
		prompt += "\n\nFull inventory: " + inventoryText
	}

	// Anti-hallucination warning
	prompt += "\n\n[ANTI-HALLUCINATION] Reference ONLY coordinates/inventory data above. NEVER assume items. If unsure, say 'I don't know'."

	return prompt
}

// Ask queries the LLM API with the player's message and returns the response.
func (nc *NvidiaClient) Ask(user, systemPrompt, message string) (string, error) {
	messages := nc.prepareMessages(user, systemPrompt, message)

	reply, err := nc.completeWithRetry(messages, 0.4)
	if err != nil {
		return "", err
	}

	nc.storeHistory(user, message, reply)
	return reply, nil
}

// prepareMessages builds the message sequence for a chat request, including
// recent conversation history and the new user message.
func (nc *NvidiaClient) prepareMessages(user, systemPrompt, message string) []Message {
	var rawMessages []Message
	rawMessages = append(rawMessages, Message{Role: "system", Content: systemPrompt})

	hist := nc.History.GetHistory(user)
	if len(hist) > 10 {
		hist = hist[len(hist)-10:]
	}
	rawMessages = append(rawMessages, hist...)
	rawMessages = append(rawMessages, Message{Role: "user", Content: fmt.Sprintf("<%s> %s", user, message)})

	return FixMessages(rawMessages)
}

// storeHistory stores the chat turn in the conversation history.
func (nc *NvidiaClient) storeHistory(user, message, reply string) {
	parsed := Parse(reply)
	nc.History.AddMessage(user, "user", fmt.Sprintf("<%s> %s", user, message))
	nc.History.AddMessage(user, "assistant", parsed.CleanReply)
}

// completeWithRetry sends a chat completion request and retries on rate-limit
// (HTTP 429) and transient network errors with exponential backoff.
func (nc *NvidiaClient) completeWithRetry(messages []Message, temperature float64) (string, error) {
	req := ChatCompletionRequest{
		Model:       nc.model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   512,
	}
	maxRetries := 4
	for attempt := 0; attempt < maxRetries; attempt++ {
		completionResp, statusCode, err := nc.doCompletionRequest(req)
		if err != nil {
			if (statusCode == http.StatusTooManyRequests || statusCode == 0) && attempt < maxRetries-1 {
				time.Sleep(time.Duration(1<<attempt) * time.Second)
				continue
			}
			return "", err
		}

		if len(completionResp.Choices) == 0 || completionResp.Choices[0].Message.Content == "" {
			if attempt == maxRetries-1 {
				return "", fmt.Errorf("empty choices from %s API response", nc.provider)
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}

		return completionResp.Choices[0].Message.Content, nil
	}
	return "", fmt.Errorf("exhausted retries for %s API", nc.provider)
}

// doCompletionRequest performs a single chat completion HTTP request.
func (nc *NvidiaClient) doCompletionRequest(req ChatCompletionRequest) (ChatCompletionResponse, int, error) {
	var empty ChatCompletionResponse
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return empty, 0, fmt.Errorf("marshal request: %w", err)
	}

	url := nc.baseURL
	if url == "" {
		url = endpointNvidia
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return empty, 0, fmt.Errorf("create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+nc.apiKey)

	resp, err := nc.client.Do(httpReq)
	if err != nil {
		return empty, 0, fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return empty, resp.StatusCode, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return empty, resp.StatusCode, fmt.Errorf("HTTP %d from %s API: %s", resp.StatusCode, nc.provider, string(responseBody))
	}

	var completionResp ChatCompletionResponse
	if err := json.Unmarshal(responseBody, &completionResp); err != nil {
		return empty, resp.StatusCode, fmt.Errorf("unmarshal response: %w", err)
	}
	return completionResp, resp.StatusCode, nil
}

// AskPlanner queries the LLM without storing conversation history. Used by
// the planner's agentic loop for plan generation and step re-evaluation, so
// internal planner messages don't pollute the player's chat context.
func (nc *NvidiaClient) AskPlanner(systemPrompt, message string) (string, error) {
	var rawMessages []Message
	rawMessages = append(rawMessages, Message{Role: "system", Content: systemPrompt})
	rawMessages = append(rawMessages, Message{Role: "user", Content: message})

	req := ChatCompletionRequest{
		Model:       nc.model,
		Messages:    FixMessages(rawMessages),
		Temperature: 0.3, // slightly lower for structured plan output
		MaxTokens:   512,
	}

	completionResp, _, err := nc.doCompletionRequest(req)
	if err != nil {
		return "", err
	}
	if len(completionResp.Choices) == 0 || completionResp.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("empty choices from %s API response", nc.provider)
	}
	return completionResp.Choices[0].Message.Content, nil
}
