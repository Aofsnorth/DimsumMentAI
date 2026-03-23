package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
)

// NVIDIAClient is an LLM client for NVIDIA NIM endpoints.
type NVIDIAClient struct {
	apiKey  string
	baseURL string
	model   string
	client  *openai.Client
}

// NewNVIDIAClient creates a new NVIDIA NIM client.
func NewNVIDIAClient(apiKey, baseURL, model string) *NVIDIAClient {
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = baseURL
	client := openai.NewClientWithConfig(config)

	return &NVIDIAClient{
		apiKey:  apiKey,
		baseURL: baseURL,
		model:   model,
		client:  client,
	}
}

// Chat sends a chat completion request to NVIDIA NIM.
func (c *NVIDIAClient) Chat(ctx context.Context, messages []Message, tools []Tool, model string) (*Message, []ToolCall, error) {
	reqModel := c.model
	if model != "" {
		reqModel = model
	}

	openaiMessages := convertMessagesToOpenAI(messages)
	openaiTools := convertToolsToOpenAI(tools)

	req := openai.ChatCompletionRequest{
		Model:       reqModel,
		Messages:    openaiMessages,
		Tools:       openaiTools,
		ToolChoice:  "auto",
		Temperature: 0.7,
	}

	resp, err := c.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return nil, nil, fmt.Errorf("NVIDIA API error: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, nil, errors.New("no response choices returned")
	}

	choice := resp.Choices[0]
	responseMessage := choice.Message

	resultMessage := &Message{
		Role:    responseMessage.Role,
		Content: responseMessage.Content,
	}

	var toolCalls []ToolCall
	if len(responseMessage.ToolCalls) > 0 {
		toolCalls = make([]ToolCall, 0, len(responseMessage.ToolCalls))
		for _, tc := range responseMessage.ToolCalls {
			args := make(map[string]any)
			if err := json.Unmarshal([]byte(tc.FunctionCall.Arguments), &args); err != nil {
				args["_raw"] = tc.FunctionCall.Arguments
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   tc.ID,
				Name: tc.FunctionCall.Name,
				Args: args,
			})
		}
		resultMessage.ToolCalls = toolCalls
	}

	return resultMessage, toolCalls, nil
}

// ChatWithRetry sends a chat request with retry on transient errors.
func (c *NVIDIAClient) ChatWithRetry(ctx context.Context, messages []Message, tools []Tool, model string) (*Message, []ToolCall, error) {
	var lastErr error
	backoff := 500 * time.Millisecond

	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(backoff):
				backoff *= 2
			}
		}

		msg, calls, err := c.Chat(ctx, messages, tools, model)
		if err == nil {
			return msg, calls, nil
		}

		lastErr = err

		if !isRetryableError(err) {
			return nil, nil, err
		}
	}

	return nil, nil, fmt.Errorf("all retries exhausted: %w", lastErr)
}

// isRetryableError determines if an error should trigger a retry.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()

	if strings.Contains(errStr, "status code: 500") ||
		strings.Contains(errStr, "status code: 502") ||
		strings.Contains(errStr, "status code: 503") {
		return true
	}

	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "network") ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, io.ErrServerClosed) ||
		errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var oaiErr *openai.APIError
	if errors.As(err, &oaiErr) {
		if oaiErr.StatusCode >= 500 && oaiErr.StatusCode < 600 {
			return true
		}
	}

	var netErr *net.URLError
	if errors.As(err, &netErr) {
		return true
	}

	return false
}

// convertMessagesToOpenAI converts our Message type to openai.ChatCompletionMessage.
func convertMessagesToOpenAI(messages []Message) []openai.ChatCompletionMessage {
	result := make([]openai.ChatCompletionMessage, 0, len(messages))

	for _, msg := range messages {
		openaiMsg := openai.ChatCompletionMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}

		if msg.Name != "" {
			openaiMsg.Name = msg.Name
		}

		if msg.ToolCallID != "" {
			openaiMsg.ToolCallID = msg.ToolCallID
		}

		if len(msg.ToolCalls) > 0 {
			openaiMsg.ToolCalls = make([]openai.ToolCall, len(msg.ToolCalls))
			for i, tc := range msg.ToolCalls {
				argsJSON, _ := json.Marshal(tc.Args)
				openaiMsg.ToolCalls[i] = openai.ToolCall{
					ID: tc.ID,
					Function: openai.FunctionCall{
						Name:      tc.Name,
						Arguments: string(argsJSON),
					},
				}
			}
		}

		result = append(result, openaiMsg)
	}

	return result
}

// convertToolsToOpenAI converts our Tool type to openai.Tool.
func convertToolsToOpenAI(tools []Tool) []openai.Tool {
	if tools == nil {
		return nil
	}

	result := make([]openai.Tool, len(tools))
	for i, tool := range tools {
		result[i] = openai.Tool{
			Type: "function",
			Function: openai.FunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		}
	}

	return result
}

// EstimateTokens returns a rough estimate of the number of tokens in the text.
// Uses approximately 4 characters per token for English text.
func EstimateTokens(text string) int {
	return len(text) / 4
}
