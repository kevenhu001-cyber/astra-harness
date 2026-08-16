package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- Usage dual-naming (the prompt_tokens/completion_tokens bug) ---

func TestUsageUnmarshalOpenAIStyle(t *testing.T) {
	// OpenAI / DeepSeek / Qwen compatible APIs report prompt_tokens /
	// completion_tokens, not input_tokens / output_tokens.
	var u Usage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":10,"completion_tokens":5}`), &u); err != nil {
		t.Fatal(err)
	}
	if u.InputTokens != 10 || u.OutputTokens != 5 {
		t.Fatalf("openai-style usage = %+v, want input=10 output=5", u)
	}
}

func TestUsageUnmarshalAnthropicStyle(t *testing.T) {
	var u Usage
	if err := json.Unmarshal([]byte(`{"input_tokens":11,"output_tokens":4,"cache_read_input_tokens":3,"reasoning_tokens":2}`), &u); err != nil {
		t.Fatal(err)
	}
	if u.InputTokens != 11 || u.OutputTokens != 4 || u.CacheReadTokens != 3 || u.ReasoningTokens != 2 {
		t.Fatalf("anthropic-style usage = %+v", u)
	}
}

func TestUsageUnmarshalPrecedence(t *testing.T) {
	// When both conventions are present (some proxies), input_tokens wins.
	var u Usage
	if err := json.Unmarshal([]byte(`{"input_tokens":1,"prompt_tokens":99}`), &u); err != nil {
		t.Fatal(err)
	}
	if u.InputTokens != 1 {
		t.Fatalf("input_tokens should take precedence, got %d", u.InputTokens)
	}
}

// --- OpenAI-compatible streaming ---

// collectOpenAI consumes the stream and accumulates state across events,
// mirroring how the engine consumes it: the parser re-emits the accumulated
// tool-call state and usage on later events, and the terminal [DONE] event
// carries no payload.
func collectOpenAI(t *testing.T, srv *httptest.Server, req *Request) (content string, toolCalls []ToolCallDelta, usage *Usage, finish string, err error) {
	t.Helper()
	p := &OpenAICompatible{IDName: "test", BaseURL: srv.URL, APIKey: "k", HTTPClient: srv.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := p.Stream(ctx, req)
	if err != nil {
		return "", nil, nil, "", err
	}
	var sb strings.Builder
	for ev := range ch {
		if ev.Error != nil {
			return "", nil, nil, "", ev.Error
		}
		sb.WriteString(ev.Content)
		if len(ev.ToolCalls) > 0 {
			toolCalls = ev.ToolCalls
		}
		if ev.Usage != nil {
			usage = ev.Usage
		}
		if ev.FinishReason != "" {
			finish = ev.FinishReason
		}
	}
	return sb.String(), toolCalls, usage, finish, nil
}

func TestOpenAIStreamContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "m1" {
			t.Errorf("model = %v", body["model"])
		}
		if body["stream"] != true {
			t.Errorf("stream = %v", body["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello \"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"world\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	content, _, _, _, err := collectOpenAI(t, srv, &Request{Model: "m1", Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if content != "Hello world" {
		t.Fatalf("content = %q", content)
	}
}

func TestOpenAIStreamToolCallAccumulation(t *testing.T) {
	// Arguments arrive split across chunks and must be stitched together.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"search\",\"arguments\":\"\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"q\\\":\\\"\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"runServer\\\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	_, toolCalls, _, finish, err := collectOpenAI(t, srv, &Request{Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("tool calls = %+v", toolCalls)
	}
	tc := toolCalls[0]
	if tc.ID != "call_1" || tc.Name != "search" || tc.Arguments != `{"q":"runServer"}` {
		t.Fatalf("tool call = %+v", tc)
	}
	if finish != "tool_calls" {
		t.Fatalf("finish reason = %q", finish)
	}
}

func TestOpenAIStreamUsage(t *testing.T) {
	// stream_options.include_usage: final chunk carries usage with OpenAI naming.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	_, _, usage, _, err := collectOpenAI(t, srv, &Request{Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.InputTokens != 12 || usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v, want input=12 output=3", usage)
	}
}

func TestOpenAIStreamHTTPStatusErrors(t *testing.T) {
	cases := []struct {
		status     int
		retryable  bool
		statusCode int
	}{
		{429, true, 429},
		{503, true, 503},
		{400, false, 400},
		{401, false, 401},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			fmt.Fprint(w, "nope")
		}))
		p := &OpenAICompatible{IDName: "test", BaseURL: srv.URL, APIKey: "k", HTTPClient: srv.Client()}
		_, err := p.Stream(context.Background(), &Request{Model: "m1"})
		srv.Close()
		herr, ok := err.(*HTTPStatusError)
		if !ok {
			t.Fatalf("status %d: err = %v, want *HTTPStatusError", c.status, err)
		}
		if herr.StatusCode != c.statusCode || herr.IsRetryable() != c.retryable {
			t.Fatalf("status %d: got code=%d retryable=%v", c.status, herr.StatusCode, herr.IsRetryable())
		}
	}
}

func TestOpenAIStreamMalformedLineSkipped(t *testing.T) {
	// Non-JSON data lines must be skipped without killing the stream.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {not json}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"survived\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	content, _, _, _, err := collectOpenAI(t, srv, &Request{Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if content != "survived" {
		t.Fatalf("content = %q", content)
	}
}

// --- Anthropic streaming ---

func collectAnthropic(t *testing.T, srv *httptest.Server, req *Request) (content string, toolCalls []ToolCallDelta, usage *Usage, finish string, err error) {
	t.Helper()
	p := &Anthropic{APIKey: "k", BaseURL: srv.URL, HTTPClient: srv.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := p.Stream(ctx, req)
	if err != nil {
		return "", nil, nil, "", err
	}
	var sb strings.Builder
	for ev := range ch {
		if ev.Error != nil {
			return "", nil, nil, "", ev.Error
		}
		sb.WriteString(ev.Content)
		if len(ev.ToolCalls) > 0 {
			toolCalls = ev.ToolCalls
		}
		if ev.Usage != nil {
			usage = ev.Usage
		}
		if ev.FinishReason != "" {
			finish = ev.FinishReason
		}
	}
	return sb.String(), toolCalls, usage, finish, nil
}

func TestAnthropicStreamContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k" {
			t.Errorf("x-api-key = %q", r.Header.Get("x-api-key"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\n")
		fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":9,\"output_tokens\":0}}}\n\n")
		fmt.Fprint(w, "event: content_block_start\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi \"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"there\"}}\n\n")
		fmt.Fprint(w, "event: message_delta\n")
		fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n")
		fmt.Fprint(w, "event: message_stop\n")
		fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	content, _, usage, finish, err := collectAnthropic(t, srv, &Request{Model: "claude-x", MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	if content != "Hi there" {
		t.Fatalf("content = %q", content)
	}
	if finish != "end_turn" {
		t.Fatalf("finish reason = %q", finish)
	}
	if usage == nil || usage.InputTokens != 9 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestAnthropicStreamToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: content_block_start\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"get_weather\",\"input\":{}}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"city\\\":\\\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"London\\\"}\"}}\n\n")
		fmt.Fprint(w, "event: message_delta\n")
		fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n")
		fmt.Fprint(w, "event: message_stop\n")
		fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	_, toolCalls, _, finish, err := collectAnthropic(t, srv, &Request{Model: "claude-x", MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("tool calls = %+v", toolCalls)
	}
	tc := toolCalls[0]
	if tc.ID != "toolu_1" || tc.Name != "get_weather" || tc.Arguments != `{"city":"London"}` {
		t.Fatalf("tool call = %+v", tc)
	}
	if finish != "tool_use" {
		t.Fatalf("finish reason = %q", finish)
	}
}

func TestAnthropicStreamErrorEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: error\n")
		fmt.Fprint(w, "data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
	}))
	defer srv.Close()

	p := &Anthropic{APIKey: "k", BaseURL: srv.URL, HTTPClient: srv.Client()}
	ch, err := p.Stream(context.Background(), &Request{Model: "claude-x", MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	for ev := range ch {
		if ev.Error == nil {
			continue
		}
		if !strings.Contains(ev.Error.Error(), "overloaded_error") {
			t.Fatalf("error = %v", ev.Error)
		}
		return
	}
	t.Fatal("expected an error event, stream closed cleanly")
}
