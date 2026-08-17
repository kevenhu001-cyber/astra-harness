package engine

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed system_prompt.md
var astraBasePrompt string

// buildSystemPrompt assembles the system prompt sent to the model:
//
//  1. The static Astra persona and working rules (system_prompt.md).
//  2. Mode-specific sandbox/approval instructions.
//  3. Project instructions collected from AGENTS.md files.
//  4. The compiled knowledge state (goals, claims, evidence, unknowns).
//  5. The full tool catalog.
func (e *Engine) buildSystemPrompt() string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(astraBasePrompt))
	b.WriteString("\n\n")
	b.WriteString(e.permissionInstructions())
	b.WriteString("\n\n")
	if docs := e.LoadProjectInstructions(); docs != "" {
		b.WriteString("=== PROJECT INSTRUCTIONS (AGENTS.md) ===\n")
		b.WriteString(docs)
		b.WriteString("\n\n")
	}
	b.WriteString("=== COMPILED KNOWLEDGE STATE (query result, not chat history) ===\n")
	b.WriteString(e.CompilerOutput())
	b.WriteString("\n=== TOOLS ===\n")
	for _, t := range e.AllToolDefs() {
		fmt.Fprintf(&b, "- %s: %s\n", t.Name, t.Description)
	}
	return b.String()
}

// permissionInstructions renders the active sandbox/approval guidance for
// each run. The text tells the model what the current permission mode means
// in practice.
func (e *Engine) permissionInstructions() string {
	switch {
	case e.Perm.IsPlanMode():
		return `## Sandbox and approvals

Current mode: plan.

You are read-only. Do not modify files, run commands with side effects, or perform any write/execute/deploy/delete action. Investigate the codebase with read-only tools and present a concrete plan: goal, constraints, steps, and expected verification.`
	case e.Perm.GetMode() == ModeAllow:
		return `## Sandbox and approvals

Current mode: allow.

Approval is not requested for tool calls; you may run commands, edit files, and use MCP tools. Keep actions scoped to the user's request, prefer reversible operations, and avoid destructive commands (e.g. rm, git reset --hard) unless the user explicitly asked for them.`
	case e.Perm.GetMode() == ModeDeny:
		return `## Sandbox and approvals

Current mode: deny (read-only).

Write, execute, and destructive tools will be rejected. Do not attempt them. Analyze the problem with read-only tools and deliver a concrete plan the user can approve.`
	default:
		return `## Sandbox and approvals

Current mode: ask.

Tool calls that need approval pause and show the user a permission prompt. Do not claim an action was completed until its tool result confirms it. Prefer read-only investigation before proposing writes or high-risk commands; when a prompt appears, the user may allow once, allow for the session, or deny.`
	}
}
