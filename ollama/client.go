package ollama

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
			Timeout: 5 * time.Minute,
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

// ListModels retrieves the available models in local Ollama instance
func (c *Client) ListModels() ([]string, error) {
	resp, err := c.HTTPClient.Get(c.BaseURL + "/api/tags")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama API error: status %d", resp.StatusCode)
	}

	var res ListModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	names := make([]string, 0, len(res.Models))
	for _, m := range res.Models {
		names = append(names, m.Name)
	}
	return names, nil
}

// ChatStreamFull handles full streaming for thinking, content, and collects tool calls
func (c *Client) ChatStreamFull(req ChatRequest, cb StreamCallbacks) (*Message, error) {
	req.Stream = true
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", c.BaseURL+"/api/chat", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama API returned status %d: %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var fullContent bytes.Buffer
	var fullThinking bytes.Buffer
	var collectedToolCalls []ToolCall

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var chunk ChatResponseChunk
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue
		}

		if chunk.Message.Thinking != "" {
			fullThinking.WriteString(chunk.Message.Thinking)
			if cb.OnThinking != nil {
				cb.OnThinking(chunk.Message.Thinking)
			}
		}

		if chunk.Message.Content != "" {
			fullContent.WriteString(chunk.Message.Content)
			if cb.OnContent != nil {
				cb.OnContent(chunk.Message.Content)
			}
		}

		if len(chunk.Message.ToolCalls) > 0 {
			collectedToolCalls = append(collectedToolCalls, chunk.Message.ToolCalls...)
		}

		if chunk.Done {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("stream scanning error: %w", err)
	}

	return &Message{
		Role:      "assistant",
		Content:   fullContent.String(),
		Thinking:  fullThinking.String(),
		ToolCalls: collectedToolCalls,
	}, nil
}
