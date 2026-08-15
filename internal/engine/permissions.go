package engine

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kevenhu001-cyber/astra-harness/internal/core"
)

// Permission kinds.
const (
	PermRead       = "READ"
	PermWrite      = "WRITE"
	PermExecute    = "EXECUTE"
	PermNetwork    = "NETWORK"
	PermCredential = "CREDENTIAL"
	PermDeploy     = "DEPLOY"
	PermDelete     = "DELETE"
)

// Permission modes.
const (
	ModeAsk   = "ask"
	ModeAllow = "allow"
	ModeDeny  = "deny"
)

// PermissionRequest is sent to the UI/operator.
type PermissionRequest struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Target      string `json:"target"`
	Description string `json:"description"`
	Command     string `json:"command,omitempty"`
	Risk        string `json:"risk,omitempty"`
}

// PermissionDecision is the operator's answer.
type PermissionDecision struct {
	Allowed bool
	Always  bool
}

// PromptFunc asks the operator for a decision.
type PromptFunc func(req PermissionRequest) (PermissionDecision, error)

// PermissionManager enforces the safety model.
type PermissionManager struct {
	mu          sync.Mutex
	Root        string
	Mode        string
	PlanMode    bool
	Prompt      PromptFunc
	allowAlways map[string]bool
	denyAlways  map[string]bool
}

func NewPermissionManager(root, mode string, prompt PromptFunc) *PermissionManager {
	if mode == "" {
		mode = ModeAsk
	}
	return &PermissionManager{
		Root: root, Mode: mode, Prompt: prompt,
		allowAlways: map[string]bool{}, denyAlways: map[string]bool{},
	}
}

// Check decides whether a capability may proceed.
func (p *PermissionManager) Check(kind, target, description, command string) (bool, error) {
	p.mu.Lock()
	plan := p.PlanMode
	mode := p.Mode
	allowAlways := p.allowAlways[target]
	denyAlways := p.denyAlways[target]
	p.mu.Unlock()

	if plan && kind != PermRead {
		return false, fmt.Errorf("plan mode blocks %s; switch to normal mode to continue", strings.ToLower(kind))
	}
	if denyAlways {
		return false, nil
	}
	if allowAlways {
		return true, nil
	}
	switch mode {
	case ModeAllow:
		return true, nil
	case ModeDeny:
		return false, nil
	}
	if kind == PermRead {
		return true, nil
	}
	if p.Prompt == nil {
		return false, fmt.Errorf("permission prompt unavailable for %s", target)
	}
	req := PermissionRequest{
		ID: newReqID(), Kind: kind, Target: target,
		Description: description, Command: command, Risk: riskLabel(kind, command),
	}
	dec, err := p.Prompt(req)
	if err != nil {
		return false, err
	}
	if dec.Always {
		p.mu.Lock()
		if dec.Allowed {
			p.allowAlways[target] = true
		} else {
			p.denyAlways[target] = true
		}
		p.mu.Unlock()
	}
	return dec.Allowed, nil
}

// SetMode changes the permission mode at runtime.
func (p *PermissionManager) SetMode(mode string) {
	p.mu.Lock()
	p.Mode = mode
	p.mu.Unlock()
}

func (p *PermissionManager) GetMode() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Mode
}

func (p *PermissionManager) SetPlanMode(v bool) {
	p.mu.Lock()
	p.PlanMode = v
	p.mu.Unlock()
}

func (p *PermissionManager) IsPlanMode() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.PlanMode
}

// ResetAlways clears session-wide allow/deny decisions.
func (p *PermissionManager) ResetAlways() {
	p.mu.Lock()
	p.allowAlways = map[string]bool{}
	p.denyAlways = map[string]bool{}
	p.mu.Unlock()
}

func riskLabel(kind, command string) string {
	switch kind {
	case PermWrite:
		return "modifies files"
	case PermExecute:
		if command != "" {
			return "runs: " + truncateForRisk(command, 80)
		}
		return "executes a command"
	case PermDelete:
		return "permanently deletes data"
	case PermCredential:
		return "accesses credentials"
	case PermDeploy:
		return "deploys to production"
	case PermNetwork:
		return "accesses the network"
	default:
		return strings.ToLower(kind)
	}
}

func truncateForRisk(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func newReqID() string {
	return core.NewID("req")
}

// SafePath ensures a path stays inside the project root.
func (p *PermissionManager) SafePath(path string) (string, error) {
	if path == "" {
		return p.Root, nil
	}
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(p.Root, path)
	}
	rel, err := filepath.Rel(p.Root, full)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes project root: %s", path)
	}
	return filepath.Clean(full), nil
}
