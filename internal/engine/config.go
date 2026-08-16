// Package engine implements the Astra agent runtime: config, permissions,
// tools, verification and the uncertainty-driven loop.
package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kevenhu001-cyber/astra-harness/internal/llm"
)

// ProviderConfig describes one LLM backend.
type ProviderConfig struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"` // openai-compatible | anthropic
	Name      string   `json:"name,omitempty"`
	BaseURL   string   `json:"base_url,omitempty"`
	APIKeyEnv string   `json:"api_key_env,omitempty"`
	APIKey    string   `json:"api_key,omitempty"`
	Models    []string `json:"models"`
}

// McpServerConfig describes one MCP server (mirrors Codex's
// [mcp_servers.<name>] config): stdio (command + args + env) or HTTP
// (url + headers), with optional per-tool allow/deny under Tools.
type McpServerConfig struct {
	ID      string            `json:"id"`
	Type    string            `json:"type,omitempty"` // "stdio" (default) | "http"
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Tools maps tool names to per-tool policy (e.g. Disabled).
	Tools map[string]McpToolConfig `json:"tools,omitempty"`
}

// McpToolConfig is the per-tool policy for an MCP server (Codex
// [mcp_servers.<name>.tools.<tool>]).
type McpToolConfig struct {
	Disabled bool `json:"disabled,omitempty"`
}

// HookConfig is a command-based lifecycle hook (Codex hook_runtime.rs
// subset): run before/after tool calls and around compaction. The command is
// executed via sh -c with a JSON payload on stdin. For blocking events
// (PreToolUse / PreCompact) a non-zero exit denies the action.
type HookConfig struct {
	// Event is one of: PreToolUse, PostToolUse, PreCompact, PostCompact.
	Event string `json:"event"`
	// Tools restricts the hook to specific tool names; empty matches all.
	Tools []string `json:"tools,omitempty"`
	// Command is the shell command to run.
	Command string `json:"command"`
	// TimeoutSeconds bounds the hook; default 10.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// Config is the merged Astra configuration.
type Config struct {
	Providers        []ProviderConfig `json:"providers"`
	DefaultProvider  string           `json:"default_provider,omitempty"`
	DefaultModel     string           `json:"default_model,omitempty"`
	PermissionMode   string           `json:"permission_mode,omitempty"` // ask | allow | deny
	MaxIterations    int              `json:"max_iterations,omitempty"`
	MaxContextTokens int              `json:"max_context_tokens,omitempty"`
	AutoVerify       *bool            `json:"auto_verify,omitempty"`
	TimeoutSeconds   int              `json:"timeout_seconds,omitempty"`
	SmallModel       string           `json:"small_model,omitempty"`
	ReasoningEffort  string           `json:"reasoning_effort,omitempty"` // low | medium | high | xhigh
	RecentModels     []string         `json:"recent_models,omitempty"`
	// MaxProjectDocBytes caps the total AGENTS.md content injected into the
	// system prompt (Codex: project_doc_max_bytes, default 32 KiB).
	MaxProjectDocBytes int `json:"max_project_doc_bytes,omitempty"`
	// McpServers lists stdio MCP servers whose tools are exposed to the model
	// under the mcp__<server-id>__<tool> namespace.
	McpServers []McpServerConfig `json:"mcp_servers,omitempty"`
	// Hooks lists lifecycle hooks (PreToolUse / PostToolUse / PreCompact /
	// PostCompact).
	Hooks []HookConfig `json:"hooks,omitempty"`
	// AuthServer is the Astra account/auth server base URL used by
	// `astra login` (device flow). Overridable with ASTRA_AUTH_SERVER.
	AuthServer string `json:"auth_server,omitempty"`
	// StatusLine is the ordered list of Codex-compatible status line items
	// shown in the TUI footer (e.g. model, git-branch, context-used).
	StatusLine []string `json:"status_line,omitempty"`
	// StatusLineUseColors enables semantic colors in the status line.
	StatusLineUseColors *bool `json:"status_line_use_colors,omitempty"`
	// Keymap maps core TUI action names to their key chord (e.g.
	// "interrupt_or_quit": "ctrl+c"). Empty values fall back to defaults.
	Keymap map[string]string `json:"keymap,omitempty"`
}

// defaultProjectDocBytes mirrors Codex's AGENTS_MD_MAX_BYTES (32 KiB).
const defaultProjectDocBytes = 32 * 1024

// DefaultKeymap mirrors Codex's default global key bindings for the actions
// Astra exposes. Values are Bubble Tea key strings.
var DefaultKeymap = map[string]string{
	"interrupt_or_quit": "ctrl+c",
	"quit":              "ctrl+d",
	"backtrack":         "esc",
	"history_search":    "ctrl+r",
	"newline":           "ctrl+j",
	"external_editor":   "ctrl+g",
	"transcript":        "ctrl+t",
	"model_picker":      "ctrl+m",
}

// DefaultStatusLine mirrors Codex's default footer items.
var DefaultStatusLine = []string{"model-with-reasoning", "current-dir"}

func defaultConfig() *Config {
	autoVerify := true
	statusLineColors := true
	return &Config{
		Providers: []ProviderConfig{
			{ID: "openai", Type: "openai-compatible", Name: "OpenAI",
				BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY",
				Models: []string{"gpt-4o", "gpt-4o-mini"}},
			{ID: "anthropic", Type: "anthropic", Name: "Anthropic",
				BaseURL: "https://api.anthropic.com", APIKeyEnv: "ANTHROPIC_AUTH_TOKEN",
				Models: []string{"claude-sonnet-4-20250514", "claude-3-5-sonnet-20241022"}},
			{ID: "deepseek", Type: "openai-compatible", Name: "DeepSeek",
				BaseURL: "https://api.deepseek.com/v1", APIKeyEnv: "DEEPSEEK_API_KEY",
				Models: []string{"deepseek-chat", "deepseek-reasoner"}},
			{ID: "qwen", Type: "openai-compatible", Name: "Qwen (DashScope)",
				BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", APIKeyEnv: "DASHSCOPE_API_KEY",
				Models: []string{"qwen-plus", "qwen-max", "qwen-coder-plus"}},
			{ID: "openrouter", Type: "openai-compatible", Name: "OpenRouter",
				BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OPENROUTER_API_KEY",
				Models: []string{"openai/gpt-4o", "anthropic/claude-sonnet-4"}},
			{ID: "ollama", Type: "openai-compatible", Name: "Ollama (local)",
				BaseURL: "http://localhost:11434/v1", APIKeyEnv: "OLLAMA_API_KEY",
				Models: []string{"llama3.1", "qwen2.5-coder"}},
		},
		PermissionMode:      "ask",
		MaxIterations:       20,
		MaxContextTokens:    160000,
		AutoVerify:          &autoVerify,
		TimeoutSeconds:      120,
		SmallModel:          "",
		MaxProjectDocBytes:  defaultProjectDocBytes,
		StatusLine:          append([]string(nil), DefaultStatusLine...),
		StatusLineUseColors: &statusLineColors,
		Keymap:              cloneKeymap(DefaultKeymap),
	}
}

func cloneKeymap(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// LoadConfig merges global and project configs, then applies env overrides.
func LoadConfig(root string) (*Config, error) {
	cfg := defaultConfig()
	if err := mergeFile(cfg, globalConfigPath()); err != nil {
		return nil, err
	}
	if err := mergeFile(cfg, filepath.Join(root, ".astra", "config.json")); err != nil {
		return nil, err
	}
	if v := os.Getenv("ASTRA_PROVIDER"); v != "" {
		cfg.DefaultProvider = v
	}
	if v := os.Getenv("ASTRA_MODEL"); v != "" {
		cfg.DefaultModel = v
	}
	if v := os.Getenv("ASTRA_PERMISSION_MODE"); v != "" {
		cfg.PermissionMode = v
	}
	if v := os.Getenv("ASTRA_AUTH_SERVER"); v != "" {
		cfg.AuthServer = v
	}
	if cfg.PermissionMode == "" {
		cfg.PermissionMode = "ask"
	}
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 20
	}
	if cfg.MaxContextTokens <= 0 {
		cfg.MaxContextTokens = 160000
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 120
	}
	return cfg, nil
}

func globalConfigPath() string {
	if d := os.Getenv("ASTRA_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "config.json")
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "astra", "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "astra", "config.json")
}

func mergeFile(cfg *Config, path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config %s: %w", path, err)
	}
	var merged Config
	if err := json.Unmarshal(data, &merged); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	if merged.Providers != nil {
		cfg.Providers = merged.Providers
	}
	if merged.DefaultProvider != "" {
		cfg.DefaultProvider = merged.DefaultProvider
	}
	if merged.DefaultModel != "" {
		cfg.DefaultModel = merged.DefaultModel
	}
	if merged.PermissionMode != "" {
		cfg.PermissionMode = merged.PermissionMode
	}
	if merged.MaxIterations > 0 {
		cfg.MaxIterations = merged.MaxIterations
	}
	if merged.MaxContextTokens > 0 {
		cfg.MaxContextTokens = merged.MaxContextTokens
	}
	if merged.AutoVerify != nil {
		cfg.AutoVerify = merged.AutoVerify
	}
	if merged.TimeoutSeconds > 0 {
		cfg.TimeoutSeconds = merged.TimeoutSeconds
	}
	if merged.SmallModel != "" {
		cfg.SmallModel = merged.SmallModel
	}
	if merged.ReasoningEffort != "" {
		cfg.ReasoningEffort = merged.ReasoningEffort
	}
	if merged.RecentModels != nil {
		cfg.RecentModels = merged.RecentModels
	}
	if merged.MaxProjectDocBytes > 0 {
		cfg.MaxProjectDocBytes = merged.MaxProjectDocBytes
	}
	if merged.McpServers != nil {
		cfg.McpServers = merged.McpServers
	}
	if merged.Hooks != nil {
		cfg.Hooks = merged.Hooks
	}
	if merged.StatusLine != nil {
		cfg.StatusLine = merged.StatusLine
	}
	if merged.StatusLineUseColors != nil {
		cfg.StatusLineUseColors = merged.StatusLineUseColors
	}
	if merged.Keymap != nil {
		cfg.Keymap = merged.Keymap
	}
	return nil
}

// BuildProviders constructs llm.Provider instances from config.
func BuildProviders(cfg *Config) []llm.Provider {
	var out []llm.Provider
	for _, p := range cfg.Providers {
		key := resolveKey(p)
		switch strings.ToLower(p.Type) {
		case "anthropic":
			out = append(out, &llm.Anthropic{
				APIKey: key, BaseURL: p.BaseURL, ModelList: p.Models,
			})
		default:
			out = append(out, &llm.OpenAICompatible{
				IDName: p.ID, DisplayName: p.Name, BaseURL: p.BaseURL,
				APIKey: key, ModelList: p.Models,
			})
		}
	}
	return out
}

func resolveKey(p ProviderConfig) string {
	if p.APIKey != "" {
		return p.APIKey
	}
	if p.APIKeyEnv != "" {
		if v := os.Getenv(p.APIKeyEnv); v != "" {
			return v
		}
	}
	upper := strings.ToUpper(strings.NewReplacer("-", "_").Replace(p.ID))
	if v := os.Getenv("ASTRA_" + upper + "_API_KEY"); v != "" {
		return v
	}
	if v := os.Getenv("ASTRA_API_KEY"); v != "" {
		return v
	}
	if p.Type == "anthropic" {
		if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
			return v
		}
	}
	return ""
}

// SaveConfig writes a project config.
func SaveConfig(root string, cfg *Config) error {
	dir := filepath.Join(root, ".astra")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	for _, p := range cfg.Providers {
		if p.APIKey != "" {
			mode = 0o600
			break
		}
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, mode)
}

// EnsureConfig writes a starter config if none exists.
func EnsureConfig(root string) (*Config, error) {
	cfg, err := LoadConfig(root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(root, ".astra", "config.json")); os.IsNotExist(err) {
		_ = SaveConfig(root, cfg)
	}
	return cfg, nil
}
