package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// HookEvent names, aligned with Codex hook_runtime.rs (a pragmatic subset).
type HookEvent string

const (
	HookPreToolUse  HookEvent = "PreToolUse"
	HookPostToolUse HookEvent = "PostToolUse"
	HookPreCompact  HookEvent = "PreCompact"
	HookPostCompact HookEvent = "PostCompact"
)

// defaultHookTimeout bounds hooks that do not declare their own timeout.
const defaultHookTimeout = 10 * time.Second

// runHooks executes every configured hook matching the event (and tool name,
// when a tool filter is present). For blocking events (PreToolUse,
// PreCompact) the first non-zero exit denies the action; its output becomes
// the reason. Post hooks never block; their output is surfaced as a system
// event so operators can observe them.
func (e *Engine) runHooks(event HookEvent, tool string, payload any) (denied bool, reason string) {
	hooks := e.Config.Hooks
	if len(hooks) == 0 {
		return false, ""
	}
	for _, h := range hooks {
		if h.Event != string(event) {
			continue
		}
		if tool != "" && len(h.Tools) > 0 && !containsStr(h.Tools, tool) {
			continue
		}
		output, err := e.runHookCommand(h, payload)
		trunc := truncateText(strings.TrimSpace(output), 200)
		switch event {
		case HookPreToolUse, HookPreCompact:
			if err != nil {
				if trunc == "" {
					trunc = err.Error()
				}
				return true, trunc
			}
		default:
			if trunc != "" {
				e.emit(EvSystem, fmt.Sprintf("hook %s: %s", event, trunc))
			}
		}
	}
	return false, ""
}

func (e *Engine) runHookCommand(h HookConfig, payload any) (string, error) {
	timeout := time.Duration(h.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultHookTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	cmd := shellCommand(ctx, h.Command)
	cmd.Dir = e.Root
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out) + "\n[hook timed out after " + timeout.String() + "]", err
	}
	return string(out), err
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
