// Package ai handles LLM client integration and chat parsing.
package ai

import (
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
	Stream      bool      `json:"stream"`
}

type ChatCompletionResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	// Data mirrors Choices for gateways that wrap the OpenAI payload in an
	// envelope, e.g. {"data":{"choices":[...]},"success":true}. Both are
	// optional and at most one is populated.
	Data *struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	} `json:"data"`
}

// NvidiaClient is a generic multi-protocol chat completion client. Despite the
// historical name it speaks three wire formats — OpenAI-compatible (default,
// also used by NVIDIA NIM), Anthropic Messages, and Google Gemini — selected
// from the configured provider via protocolForProvider.
type NvidiaClient struct {
	apiKey   string
	model    string
	baseURL  string
	provider string
	protocol Protocol
	client   *http.Client
	History  *MessageHistory
	persona  string
	// personaTemplate is the unresolved persona, kept so the name can be
	// re-substituted when the server assigns a different one mid-session.
	personaTemplate string
	rules           string
	language        string

	contextWindowOverride int // 0 = auto-detect from model registry
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
	case "anthropic_compatible":
		return "ANTHROPIC_API_KEY"
	case "google_compatible":
		return "GOOGLE_API_KEY"
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
		apiKey:          apiKey,
		model:           model,
		baseURL:         baseURL,
		provider:        provider,
		protocol:        protocolForProvider(provider),
		client:          &http.Client{Timeout: 120 * time.Second},
		History:         NewMessageHistory(20),
		personaTemplate: PromptCharacter,
		persona:         BuildPersona(PromptCharacter, ""),
		rules:           BedrockSystemRules,
		language:        "Indonesian",
	}
}

// SetPersona overrides the default persona prompt
// SetPersona overrides the default persona prompt and binds it to a name.
//
// botName is required because the persona templates carry a name placeholder:
// without it a persona would still be talking about whichever name was compiled
// into the constant. An empty name is allowed, and the persona then simply has
// no name to use.
func (nc *NvidiaClient) SetPersona(persona, botName string) {
	if persona == "" {
		return
	}
	nc.personaTemplate = persona
	nc.persona = BuildPersona(persona, botName)
}

// SetBotName re-binds the persona to a new username. The run loop calls this on
// every session because the bot can be switched to another server, where the
// server may hand it a different identity than the one in the config.
func (nc *NvidiaClient) SetBotName(botName string) {
	nc.persona = BuildPersona(nc.personaTemplate, botName)
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
		prompt += "\n\nFull inventory: " + inventoryText +
			"\n[INVENTORY RULE] The line above is your LIVE inventory \u2014 it has ALREADY been checked for you. " +
			"When anyone asks what you have, what's in your inventory, or to check your inventory, answer DIRECTLY from this data in the SAME reply. " +
			"NEVER reply with 'let me check', 'oke aku cek dulu', 'I'll check', or any stalling phrase \u2014 you already have the data, so just state the items now."
	}

	// Anti-hallucination warning
	prompt += "\n\n[ANTI-HALLUCINATION] Reference ONLY coordinates/inventory data above. NEVER assume items. If unsure, say 'I don't know'."

	// Honesty rules last, so they are the most recent instruction the model
	// reads before it answers. Placed after the grounding blocks above on
	// purpose: it qualifies them rather than competing with them, and a model
	// that has just been shown live inventory is exactly the model about to be
	// asked "did you get it?".
	prompt += HonestyRules

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

	// Auto-compact so the request never exceeds 25% of the model's context
	// window; oldest history is dropped first to keep the bot from stalling.
	return nc.compactToBudget(FixMessages(rawMessages), nc.ContextBudget())
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
		reply, statusCode, err := nc.doCompletionRequest(req)
		if err != nil {
			if (statusCode == http.StatusTooManyRequests || statusCode == 0) && attempt < maxRetries-1 {
				time.Sleep(time.Duration(1<<attempt) * time.Second)
				continue
			}
			return "", err
		}

		if reply == "" {
			if attempt == maxRetries-1 {
				return "", fmt.Errorf("empty reply from %s API response", nc.provider)
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}

		return reply, nil
	}
	return "", fmt.Errorf("exhausted retries for %s API", nc.provider)
}

// doCompletionRequest performs a single chat completion HTTP request using the
// client's wire protocol and returns the extracted assistant text.
func (nc *NvidiaClient) doCompletionRequest(req ChatCompletionRequest) (string, int, error) {
	httpReq, err := nc.buildHTTPRequest(req.Messages, req.Temperature, req.MaxTokens)
	if err != nil {
		return "", 0, err
	}

	resp, err := nc.client.Do(httpReq)
	if err != nil {
		return "", 0, fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", resp.StatusCode, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, fmt.Errorf("HTTP %d from %s API: %s", resp.StatusCode, nc.provider, string(responseBody))
	}

	reply, err := nc.parseReply(responseBody)
	if err != nil {
		return "", resp.StatusCode, err
	}
	return reply, resp.StatusCode, nil
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
		Messages:    nc.compactToBudget(FixMessages(rawMessages), nc.ContextBudget()),
		Temperature: 0.3, // slightly lower for structured plan output
		MaxTokens:   512,
	}

	reply, _, err := nc.doCompletionRequest(req)
	if err != nil {
		return "", err
	}
	if reply == "" {
		return "", fmt.Errorf("empty reply from %s API response", nc.provider)
	}
	return reply, nil
}
