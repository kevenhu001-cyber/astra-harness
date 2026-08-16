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

// Anthropic implements the Messages streaming API.
type Anthropic struct {
	APIKey     string
	BaseURL    string
	ModelList  []string
	HTTPClient *http.Client
}

func (p *Anthropic) ID() string   { return "anthropic" }
func (p *Anthropic) Name() string { return "Anthropic" }
func (p *Anthropic) Available() bool {
	return p.APIKey != ""
}
func (p *Anthropic) Models() []string { return p.ModelList }
func (p *Anthropic) DefaultModel() string {
	if len(p.ModelList) > 0 {
		return p.ModelList[0]
	}
	return "claude-sonnet-4-20250514"
}

func (p *Anthropic) Stream(ctx context.Context, req *Request) (<-chan StreamEvent, error) {
	messages := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case RoleUser, RoleSystem:
			if m.Content != "" {
				messages = append(messages, map[string]any{"role": "user", "content": m.Content})
			}
		case RoleAssistant:
			if len(m.ToolCalls) == 0 {
				if m.Content != "" {
					messages = append(messages, map[string]any{"role": "assistant", "content": m.Content})
				}
				continue
			}
			blocks := []map[string]any{}
			if m.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input any
				_ = json.Unmarshal([]byte(tc.Arguments), &input)
				blocks = append(blocks, map[string]any{
					"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": input,
				})
			}
			messages = append(messages, map[string]any{"role": "assistant", "content": blocks})
		case RoleTool:
			blocks := []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     truncateString(m.Content, 30000),
			}}
			messages = append(messages, map[string]any{"role": "user", "content": blocks})
		}
	}
	body := map[string]any{
		"model":       req.Model,
		"max_tokens":  req.MaxTokens,
		"temperature": req.Temperature,
		"stream":      true,
		"messages":    messages,
	}
	if req.System != "" {
		body["system"] = req.System
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"name": t.Name, "description": t.Description, "input_schema": t.Parameters,
			})
		}
		body["tools"] = tools
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	base := p.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	url := strings.TrimSuffix(base, "/") + "/v1/messages"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
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
		return nil, NewHTTPStatusError("anthropic", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	events := make(chan StreamEvent, 8)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		type toolAcc struct {
			id   string
			name string
			args strings.Builder
		}
		var acc []*toolAcc
		var finish string
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "event:") && !strings.HasPrefix(line, "data:") {
				continue
			}
			if strings.HasPrefix(line, "event:") {
				continue
			}
			var payload struct {
				Type  string `json:"type"`
				Error *struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
				Index        int `json:"index"`
				ContentBlock *struct {
					Type  string         `json:"type"`
					ID    string         `json:"id"`
					Name  string         `json:"name"`
					Input map[string]any `json:"input"`
				} `json:"content_block"`
				Delta *struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
					StopReason  string `json:"stop_reason"`
				} `json:"delta"`
				Message *struct {
					Usage *Usage `json:"usage"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data:")), &payload); err != nil {
				continue
			}
			if payload.Error != nil {
				select {
				case events <- StreamEvent{Error: fmt.Errorf("anthropic: %s: %s", payload.Error.Type, payload.Error.Message)}:
				case <-ctx.Done():
				}
				return
			}
			switch payload.Type {
			case "content_block_start":
				if payload.ContentBlock != nil && payload.ContentBlock.Type == "tool_use" {
					for len(acc) <= payload.Index {
						acc = append(acc, &toolAcc{})
					}
					acc[payload.Index].id = payload.ContentBlock.ID
					acc[payload.Index].name = payload.ContentBlock.Name
					if len(payload.ContentBlock.Input) > 0 {
						b, _ := json.Marshal(payload.ContentBlock.Input)
						acc[payload.Index].args.Write(b)
					}
				}
			case "content_block_delta":
				if payload.Delta == nil {
					continue
				}
				switch payload.Delta.Type {
				case "text_delta":
					emit(events, ctx, StreamEvent{Content: payload.Delta.Text})
				case "input_json_delta":
					for len(acc) <= payload.Index {
						acc = append(acc, &toolAcc{})
					}
					acc[payload.Index].args.WriteString(payload.Delta.PartialJSON)
				}
			case "message_delta":
				if payload.Delta != nil && payload.Delta.StopReason != "" {
					finish = payload.Delta.StopReason
				}
			case "message_start":
				if payload.Message != nil && payload.Message.Usage != nil {
					emit(events, ctx, StreamEvent{Usage: payload.Message.Usage})
				}
			case "message_stop":
				var deltas []ToolCallDelta
				for i, a := range acc {
					deltas = append(deltas, ToolCallDelta{Index: i, ID: a.id, Name: a.name, Arguments: a.args.String()})
				}
				emit(events, ctx, StreamEvent{ToolCalls: deltas, FinishReason: finish})
			}
		}
		if err := sc.Err(); err != nil && ctx.Err() == nil {
			emit(events, ctx, StreamEvent{Error: err})
		}
	}()
	return events, nil
}

func emit(ch chan<- StreamEvent, ctx context.Context, ev StreamEvent) {
	select {
	case ch <- ev:
	case <-ctx.Done():
	}
}

func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n...[truncated]"
}
