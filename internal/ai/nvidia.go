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

type NvidiaClient struct {
	apiKey   string
	model    string
	client   *http.Client
	History  *MessageHistory
	persona  string
	rules    string
	language string
}

func NewNvidiaClient(apiKey, model string) *NvidiaClient {
	if apiKey == "" {
		apiKey = os.Getenv("NVIDIA_API_KEY")
	}
	if model == "" {
		model = "nvidia/llama-3.3-nemotron-super-49b-v1"
	}

	return &NvidiaClient{
		apiKey:   apiKey,
		model:    model,
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

// Ask queries the NVIDIA LLM API with the player's message and returns the response
func (nc *NvidiaClient) Ask(user, systemPrompt, message string) (string, error) {
	// 1. Prepare raw message sequence
	var rawMessages []Message
	rawMessages = append(rawMessages, Message{Role: "system", Content: systemPrompt})

	// Add recent history (up to last 10 messages)
	hist := nc.History.GetHistory(user)
	if len(hist) > 10 {
		hist = hist[len(hist)-10:]
	}
	rawMessages = append(rawMessages, hist...)

	// Add new user message
	rawMessages = append(rawMessages, Message{Role: "user", Content: fmt.Sprintf("<%s> %s", user, message)})

	// 2. Format sequence for Nvidia's strict alternating-role validation
	messages := FixMessages(rawMessages)

	// 3. Make HTTP request with exponential backoff on HTTP 429
	var bodyBytes []byte
	var err error

	reqBody := ChatCompletionRequest{
		Model:       nc.model,
		Messages:    messages,
		Temperature: 0.7,
		MaxTokens:   512,
	}

	bodyBytes, err = json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := "https://integrate.api.nvidia.com/v1/chat/completions"
	var completionResp ChatCompletionResponse
	var lastEmpty bool
	maxRetries := 4

	for attempt := 0; attempt < maxRetries; attempt++ {
		req, reqErr := http.NewRequest("POST", url, bytes.NewBuffer(bodyBytes))
		if reqErr != nil {
			return "", fmt.Errorf("create HTTP request: %w", reqErr)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+nc.apiKey)

		resp, respErr := nc.client.Do(req)
		if respErr != nil {
			if attempt == maxRetries-1 {
				return "", fmt.Errorf("HTTP request: %w", respErr)
			}
			delay := time.Duration(1<<attempt) * time.Second
			time.Sleep(delay)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests { // HTTP 429
			resp.Body.Close()
			if attempt == maxRetries-1 {
				return "", fmt.Errorf("HTTP 429 rate limit exceeded after %d retries", maxRetries)
			}
			delay := time.Duration(1<<attempt) * time.Second
			time.Sleep(delay)
			continue
		}

		responseBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("read response body: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("HTTP %d from NVIDIA API: %s", resp.StatusCode, string(responseBody))
		}

		completionResp = ChatCompletionResponse{}
		if err := json.Unmarshal(responseBody, &completionResp); err != nil {
			return "", fmt.Errorf("unmarshal response: %w", err)
		}

		if len(completionResp.Choices) == 0 || completionResp.Choices[0].Message.Content == "" {
			lastEmpty = true
			if attempt == maxRetries-1 {
				break
			}
			// Short backoff before retrying on empty completion.
			time.Sleep(500 * time.Millisecond)
			continue
		}

		lastEmpty = false
		break
	}

	if lastEmpty {
		return "", fmt.Errorf("empty choices from NVIDIA API response")
	}

	reply := completionResp.Choices[0].Message.Content

	// 5. Store conversation step in history (clean reply text only)
	parsed := Parse(reply)
	nc.History.AddMessage(user, "user", fmt.Sprintf("<%s> %s", user, message))
	nc.History.AddMessage(user, "assistant", parsed.CleanReply)

	return reply, nil
}
