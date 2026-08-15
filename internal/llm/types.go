// Package llm provides provider-agnostic streaming access to LLM APIs.
package llm

import "context"

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

// Usage reports token accounting when the provider supplies it.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
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
	for _, x := range r.providers {
		if x.ID() == providerID || (providerID == "" && x.ID() == r.defaultID) {
			p = x
			break
		}
	}
	if p == nil && len(r.providers) > 0 {
		p = r.providers[0]
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
