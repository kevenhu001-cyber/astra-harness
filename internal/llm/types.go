// Package llm provides provider-agnostic streaming access to LLM APIs.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

// Roles.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// ToolCall is a request from the model to invoke a tool.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Message is a chat message in provider-neutral form.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolDef describes a callable tool.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Request is a provider-neutral chat completion request.
type Request struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []ToolDef
	MaxTokens   int
	Temperature float64
}

// ToolCallDelta is a streaming fragment of a tool call.
type ToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

// Usage reports token accounting when the provider supplies it. CacheReads
// and ReasoningTokens are optional fields populated by providers that
// support prompt caching or reasoning-effort accounting respectively.
type Usage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
}

// UnmarshalJSON tolerates both token-naming conventions seen in the wild:
// Anthropic-style input_tokens/output_tokens/cache_read_input_tokens and
// OpenAI-compatible prompt_tokens/completion_tokens. Without this, usage
// tracking silently reports zero for OpenAI / DeepSeek / Qwen backends.
func (u *Usage) UnmarshalJSON(data []byte) error {
	var raw struct {
		InputTokens            int `json:"input_tokens"`
		OutputTokens           int `json:"output_tokens"`
		CacheReadTokens        int `json:"cache_read_tokens"`
		CacheReadInputTokens   int `json:"cache_read_input_tokens"`
		ReasoningTokens        int `json:"reasoning_tokens"`
		PromptTokens           int `json:"prompt_tokens"`
		CompletionTokens       int `json:"completion_tokens"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	u.InputTokens = firstNonZero(raw.InputTokens, raw.PromptTokens)
	u.OutputTokens = firstNonZero(raw.OutputTokens, raw.CompletionTokens)
	u.CacheReadTokens = firstNonZero(raw.CacheReadTokens, raw.CacheReadInputTokens)
	u.ReasoningTokens = raw.ReasoningTokens
	return nil
}

func firstNonZero(vals ...int) int {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}

// StreamEvent is one event from the streaming provider.
type StreamEvent struct {
	Content      string
	ToolCalls    []ToolCallDelta
	FinishReason string
	Usage        *Usage
	Error        error
}

// Provider is a single LLM backend.
type Provider interface {
	ID() string
	Name() string
	Available() bool
	Models() []string
	DefaultModel() string
	Stream(ctx context.Context, req *Request) (<-chan StreamEvent, error)
}

// Router picks a provider and model for a task.
type Router struct {
	providers    []Provider
	defaultID    string
	defaultModel string
}

func NewRouter(providers []Provider, defaultID, defaultModel string) *Router {
	return &Router{providers: providers, defaultID: defaultID, defaultModel: defaultModel}
}

// Default returns the configured provider and model.
func (r *Router) Default() (Provider, string, error) {
	return r.Pick(r.defaultID, r.defaultModel)
}

func (r *Router) Providers() []Provider {
	return r.providers
}

// Pick resolves a provider ID and optional model name.
func (r *Router) Pick(providerID, model string) (Provider, string, error) {
	var p Provider
	if providerID != "" {
		for _, x := range r.providers {
			if x.ID() == providerID {
				p = x
				break
			}
		}
		if p == nil {
			return nil, "", fmt.Errorf("unknown provider %q", providerID)
		}
	} else {
		// Prefer the configured provider, then any available provider.
		for _, x := range r.providers {
			if (r.defaultID == "" || x.ID() == r.defaultID) && x.Available() {
				p = x
				break
			}
		}
		if p == nil {
			for _, x := range r.providers {
				if x.Available() {
					p = x
					break
				}
			}
		}
		if p == nil && len(r.providers) > 0 {
			p = r.providers[0]
		}
	}
	if p == nil {
		return nil, "", errNoProviders
	}
	if model == "" {
		model = r.defaultModel
	}
	if model == "" {
		model = p.DefaultModel()
	}
	return p, model, nil
}

type providerError string

func (e providerError) Error() string { return string(e) }

const errNoProviders = providerError("no LLM providers configured")

// HTTPStatusError marks a failed HTTP response with its status code. The
// engine uses it to decide whether a failure is transient (429 / 5xx) and
// therefore retryable with backoff.
type HTTPStatusError struct {
	Provider   string
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.Provider, e.StatusCode, e.Body)
}

// IsRetryable reports whether the error is a transient HTTP status (rate
// limit or server-side error).
func (e *HTTPStatusError) IsRetryable() bool {
	return e.StatusCode == 429 || e.StatusCode >= 500
}

// NewHTTPStatusError builds an HTTPStatusError for a provider.
func NewHTTPStatusError(provider string, code int, body string) error {
	return &HTTPStatusError{Provider: provider, StatusCode: code, Body: body}
}
