package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &Client{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Minute, // Generous 10 minute timeout for long local LLM reasoning & subagent runs
		},
	}
}

type Options struct {
	NumCtx      int     `json:"num_ctx,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
}

type FunctionParamProperty struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
}

type FunctionParamSchema struct {
	Type       string                          `json:"type"`
	Properties map[string]FunctionParamProperty `json:"properties"`
	Required   []string                        `json:"required,omitempty"`
}

type FunctionDef struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Parameters  FunctionParamSchema `json:"parameters"`
}

type Tool struct {
	Type     string      `json:"type"` // always "function"
	Function FunctionDef `json:"function"`
}

type ToolCallFunction struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

type ToolCall struct {
	ID       string           `json:"id,omitempty"`
	Function ToolCallFunction `json:"function"`
}

type Message struct {
	Role      string     `json:"role"` // "system", "user", "assistant", "tool"
	Content   string     `json:"content"`
	Thinking  string     `json:"thinking,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
	Options  *Options  `json:"options,omitempty"`
	Stream   bool      `json:"stream"`
}

type ChatResponseChunk struct {
	Model     string   `json:"model"`
	CreatedAt string   `json:"created_at"`
	Message   Message  `json:"message"`
	Done      bool     `json:"done"`
}

type ModelItem struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

type ListModelsResponse struct {
	Models []ModelItem `json:"models"`
}

type StreamCallbacks struct {
	OnThinking func(token string)
	OnContent  func(token string)
}

func (c *Client) ListModels() ([]string, error) {
	resp, err := c.HTTPClient.Get(c.BaseURL + "/api/tags")
	if err != nil {
		return nil, fmt.Errorf("failed to reach Ollama API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama API returned status: %d", resp.StatusCode)
	}

	var res ListModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("failed to decode models response: %w", err)
	}

	names := make([]string, 0, len(res.Models))
	for _, m := range res.Models {
		names = append(names, m.Name)
	}

	return names, nil
}

func (c *Client) ChatStreamFull(req ChatRequest, cb StreamCallbacks) (*Message, error) {
	return c.ChatStreamFullWithContext(context.Background(), req, cb)
}

func (c *Client) ChatStreamFullWithContext(ctx context.Context, req ChatRequest, cb StreamCallbacks) (*Message, error) {
	req.Stream = true

	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/api/chat", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		if ctx.Err() == context.Canceled || strings.Contains(err.Error(), "context canceled") {
			return nil, context.Canceled
		}
		if strings.Contains(err.Error(), "context deadline exceeded") {
			return nil, fmt.Errorf("LLM stream request timed out (context deadline exceeded)")
		}
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Ollama error (status %d): %s", resp.StatusCode, string(b))
	}

	reader := bufio.NewReader(resp.Body)

	var thinkingSB strings.Builder
	var contentSB strings.Builder
	var lastToolCalls []ToolCall

	for {
		select {
		case <-ctx.Done():
			return nil, context.Canceled
		default:
		}

		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			if ctx.Err() == context.Canceled {
				return nil, context.Canceled
			}
			return nil, fmt.Errorf("error reading stream chunk: %w", err)
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var chunk ChatResponseChunk
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue
		}

		if chunk.Message.Thinking != "" {
			thinkingSB.WriteString(chunk.Message.Thinking)
			if cb.OnThinking != nil {
				cb.OnThinking(chunk.Message.Thinking)
			}
		}

		if chunk.Message.Content != "" {
			contentSB.WriteString(chunk.Message.Content)
			if cb.OnContent != nil {
				cb.OnContent(chunk.Message.Content)
			}
		}

		if len(chunk.Message.ToolCalls) > 0 {
			lastToolCalls = chunk.Message.ToolCalls
		}

		if chunk.Done {
			break
		}
	}

	fullMsg := &Message{
		Role:      "assistant",
		Content:   contentSB.String(),
		Thinking:  thinkingSB.String(),
		ToolCalls: lastToolCalls,
	}

	return fullMsg, nil
}
