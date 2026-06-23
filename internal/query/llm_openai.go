package query

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// openAIChatRequest is the body for /chat/completions.
type openAIChatRequest struct {
	Model    string              `json:"model"`
	Messages []openAIChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openAIChatResponse covers both the non-streaming response and a streamed
// chunk (delta).
type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *LLMClient) openAIChatRequest(ctx context.Context, prompt string, stream bool) (*http.Response, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("openai api key not configured (set embeddings.openai_key)")
	}
	body, err := json.Marshal(openAIChatRequest{
		Model:    c.model,
		Messages: []openAIChatMessage{{Role: "user", Content: prompt}},
		Stream:   stream,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	return c.client.Do(req)
}

func (c *LLMClient) openAIGenerate(ctx context.Context, prompt string) (string, error) {
	resp, err := c.openAIChatRequest(ctx, prompt, false)
	if err != nil {
		return "", fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	var chatResp openAIChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if chatResp.Error != nil {
			return "", fmt.Errorf("openai error: %s", chatResp.Error.Message)
		}
		return "", fmt.Errorf("openai returned status %d: %s", resp.StatusCode, string(respBody))
	}
	if len(chatResp.Choices) == 0 {
		return "", nil
	}
	return chatResp.Choices[0].Message.Content, nil
}

func (c *LLMClient) openAIGenerateStream(ctx context.Context, prompt string, onChunk func(token string, done bool)) error {
	resp, err := c.openAIChatRequest(ctx, prompt, true)
	if err != nil {
		return fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("openai returned status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse the Server-Sent Events stream: lines of "data: {json}" terminated
	// by "data: [DONE]".
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			onChunk("", true)
			return nil
		}
		var chunk openAIChatResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			if token := chunk.Choices[0].Delta.Content; token != "" {
				onChunk(token, false)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading stream: %w", err)
	}
	onChunk("", true)
	return nil
}
