package llm

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

// OpenAICompatible talks to any /chat/completions backend (OpenAI, DeepSeek,
// Qwen/DashScope, OpenRouter, Ollama, LM Studio, ...).
type OpenAICompatible struct {
	IDName      string
	DisplayName string
	BaseURL     string
	APIKey      string
	ModelList   []string
	HTTPClient  *http.Client
}

func (p *OpenAICompatible) ID() string   { return p.IDName }
func (p *OpenAICompatible) Name() string { return p.DisplayName }
func (p *OpenAICompatible) Available() bool {
	return p.BaseURL != "" && p.APIKey != ""
}
func (p *OpenAICompatible) Models() []string { return p.ModelList }
func (p *OpenAICompatible) DefaultModel() string {
	if len(p.ModelList) > 0 {
		return p.ModelList[0]
	}
	return ""
}

type oaiMsg struct {
	Role       string        `json:"role"`
	Content    any           `json:"content"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	Name       string        `json:"name,omitempty"`
}

type oaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (p *OpenAICompatible) Stream(ctx context.Context, req *Request) (<-chan StreamEvent, error) {
	body := map[string]any{
		"model":          req.Model,
		"messages":       toOAIMessages(req.Messages),
		"stream":         true,
		"temperature":    req.Temperature,
		"stream_options": map[string]any{"include_usage": true},
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Parameters,
				},
			})
		}
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := strings.TrimSuffix(p.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		resp.Body.Close()
		return nil, fmt.Errorf("%s: HTTP %d: %s", p.IDName, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	events := make(chan StreamEvent, 8)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		type accTool struct {
			id   string
			name string
			args strings.Builder
		}
		var acc []*accTool
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				ev := StreamEvent{FinishReason: "stop"}
				select {
				case events <- ev:
				case <-ctx.Done():
				}
				return
			}
			var chunk struct {
				Choices []struct {
					Delta struct {
						Content   string `json:"content"`
						ToolCalls []struct {
							Index    int    `json:"index"`
							ID       string `json:"id"`
							Function struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
				Usage *Usage `json:"usage"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			var ev StreamEvent
			var deltas []ToolCallDelta
			for _, tc := range chunk.Choices {
				if tc.FinishReason != "" {
					ev.FinishReason = tc.FinishReason
				}
				ev.Content += tc.Delta.Content
				for _, c := range tc.Delta.ToolCalls {
					for len(acc) <= c.Index {
						acc = append(acc, &accTool{})
					}
					a := acc[c.Index]
					if c.ID != "" {
						a.id = c.ID
					}
					if c.Function.Name != "" {
						a.name = c.Function.Name
					}
					a.args.WriteString(c.Function.Arguments)
				}
			}
			for i, a := range acc {
				deltas = append(deltas, ToolCallDelta{Index: i, ID: a.id, Name: a.name, Arguments: a.args.String()})
			}
			ev.ToolCalls = deltas
			if chunk.Usage != nil {
				ev.Usage = chunk.Usage
			}
			select {
			case events <- ev:
			case <-ctx.Done():
				return
			}
		}
		if err := sc.Err(); err != nil && ctx.Err() == nil {
			select {
			case events <- StreamEvent{Error: err}:
			case <-ctx.Done():
			}
		}
	}()
	return events, nil
}

func toOAIMessages(msgs []Message) []oaiMsg {
	out := make([]oaiMsg, 0, len(msgs))
	for _, m := range msgs {
		om := oaiMsg{Role: m.Role, ToolCallID: m.ToolCallID, Name: m.Name}
		switch m.Role {
		case RoleTool:
			om.Content = m.Content
		default:
			om.Content = m.Content
		}
		for _, tc := range m.ToolCalls {
			call := oaiToolCall{ID: tc.ID, Type: "function"}
			call.Function.Name = tc.Name
			call.Function.Arguments = tc.Arguments
			om.ToolCalls = append(om.ToolCalls, call)
		}
		out = append(out, om)
	}
	return out
}
