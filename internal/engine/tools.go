package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kevenhu001-cyber/astra-harness/internal/core"
	"github.com/kevenhu001-cyber/astra-harness/internal/llm"
	"github.com/kevenhu001-cyber/astra-harness/internal/mcp"
)

// ToolResult is the normalized outcome of a tool call.
type ToolResult struct {
	Success  bool              `json:"success"`
	Output   string            `json:"output"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

const toolOutputLimit = 30000

// mcpToolPrefix namespaces MCP tools so collisions with built-ins are
// impossible (Codex uses a comparable prefix scheme for MCP tool exposure).
const mcpToolPrefix = "mcp__"

// AllToolDefs returns the built-in tools plus every connected MCP server's
// tools, namespaced as mcp__<server-id>__<tool>.
func (e *Engine) AllToolDefs() []llm.ToolDef {
	defs := ToolDefs()
	e.mu.Lock()
	clients := append([]mcp.ToolClient(nil), e.mcpClients...)
	e.mu.Unlock()
	for _, c := range clients {
		for _, t := range c.ToolDefs() {
			if e.mcpToolDisabled(c.ID(), t.Name) {
				continue
			}
			defs = append(defs, llm.ToolDef{
				Name:        mcpToolPrefix + c.ID() + "__" + t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			})
		}
	}
	return defs
}

// ToolDefs returns the tool schema exposed to the model.
func ToolDefs() []llm.ToolDef {
	return []llm.ToolDef{
		{
			Name: "search", Description: "Search the codebase with ripgrep. Returns ranked file:line matches.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":       map[string]any{"type": "string", "description": "search text or regex"},
					"max_results": map[string]any{"type": "integer", "description": "max results (default 20)"},
				},
				"required": []string{"query"},
			},
		},
		{
			Name: "read", Description: "Read a file (optionally a line range). Use for any file you need to inspect.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":       map[string]any{"type": "string"},
					"start_line": map[string]any{"type": "integer"},
					"end_line":   map[string]any{"type": "integer"},
				},
				"required": []string{"path"},
			},
		},
		{
			Name: "list_dir", Description: "List files in a directory (non-recursive, ignores heavy dirs).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":        map[string]any{"type": "string", "description": "directory, default root"},
					"max_entries": map[string]any{"type": "integer"},
				},
			},
		},
		{
			Name: "edit_file", Description: "Apply a precise replacement in an existing file. old_string must match exactly, ideally with enough surrounding context to be unique.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":        map[string]any{"type": "string"},
					"old_string":  map[string]any{"type": "string"},
					"new_string":  map[string]any{"type": "string"},
					"replace_all": map[string]any{"type": "boolean", "description": "replace every occurrence (default false)"},
				},
				"required": []string{"path", "old_string", "new_string"},
			},
		},
		{
			Name: "write_file", Description: "Create or fully overwrite a file with content.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string"},
					"content": map[string]any{"type": "string"},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			Name: "run_command", Description: "Run a shell command (build, test, program, script). Output is captured.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command":         map[string]any{"type": "string"},
					"timeout_seconds": map[string]any{"type": "integer"},
					"description":     map[string]any{"type": "string", "description": "what this command does"},
				},
				"required": []string{"command"},
			},
		},
		{
			Name: "run_tests", Description: "Run the project test suite (auto-detected runner).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string", "description": "optional override"},
				},
			},
		},
		{
			Name: "run_build", Description: "Run the project build/typecheck (auto-detected).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string", "description": "optional override"},
				},
			},
		},
		{
			Name: "git_status", Description: "Show git working tree status.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name: "git_diff", Description: "Show uncommitted diff for a path or the whole tree.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"path": map[string]any{"type": "string"}},
			},
		},
		{
			Name: "git_log", Description: "Show recent commit history.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"n": map[string]any{"type": "integer"}},
			},
		},
		{
			Name: "apply_patch", Description: "Apply an Astra patch to one or more files. Format: *** Begin Patch / *** Update File: <path> / @@ context / -old line / +new line / *** Add File: <path> (lines prefixed with +) / *** Delete File: <path> / *** Move to: <newpath> / *** End of File / *** End Patch. Use -/+ lines with enough surrounding context for a unique match; multiple files in one patch.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"patch": map[string]any{"type": "string", "description": "the full patch text between *** Begin Patch and *** End Patch"},
				},
				"required": []string{"patch"},
			},
		},
		{
			Name: "ask_user", Description: "Ask the human operator a question when information or a decision is needed.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"question": map[string]any{"type": "string"}},
				"required":   []string{"question"},
			},
		},
		{
			Name: "verify", Description: "Run verification (tests/build) and record evidence against the goal.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"scope": map[string]any{"type": "string"}},
			},
		},
	}
}

// ExecuteTool dispatches a tool call, gated by PreToolUse hooks and observed
// by PostToolUse hooks.
func (e *Engine) ExecuteTool(ctx context.Context, name, argsJSON string) ToolResult {
	var args map[string]any
	if strings.TrimSpace(argsJSON) != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return ToolResult{Success: false, Output: fmt.Sprintf("invalid arguments JSON: %v", err)}
		}
	}
	if denied, reason := e.runHooks(HookPreToolUse, name, map[string]any{"tool": name, "arguments": args}); denied {
		return ToolResult{Success: false, Output: "hook denied: " + reason}
	}
	res := e.dispatchTool(ctx, name, args)
	e.runHooks(HookPostToolUse, name, map[string]any{"tool": name, "success": res.Success, "output": res.Output})
	return res
}

// dispatchTool routes a parsed tool call to its implementation.
func (e *Engine) dispatchTool(ctx context.Context, name string, args map[string]any) ToolResult {
	if strings.HasPrefix(name, mcpToolPrefix) {
		return e.executeMcpTool(ctx, name, args)
	}
	switch name {
	case "search":
		return e.toolSearch(ctx, args)
	case "read":
		return e.toolRead(args)
	case "list_dir":
		return e.toolListDir(args)
	case "edit_file":
		return e.toolEdit(args)
	case "write_file":
		return e.toolWrite(args)
	case "run_command":
		return e.toolRun(ctx, args)
	case "run_tests":
		return e.toolTests(ctx, args)
	case "run_build":
		return e.toolBuild(ctx, args)
	case "git_status":
		return ToolResult{Success: true, Output: e.Git.Status()}
	case "git_diff":
		return e.toolGitDiff(args)
	case "git_log":
		n := argInt(args, "n", 10)
		return ToolResult{Success: true, Output: e.Git.Log(n)}
	case "apply_patch":
		return e.toolApplyPatch(args)
	case "ask_user":
		return e.toolAskUser(args)
	case "verify":
		return e.toolVerify(ctx, args)
	default:
		return ToolResult{Success: false, Output: "unknown tool: " + name}
	}
}

// toolApplyPatch validates permissions for every file the patch touches (WRITE
// for add/update, DELETE for delete), then applies the hunks in order.
func (e *Engine) toolApplyPatch(args map[string]any) ToolResult {
	patch := argString(args, "patch", "")
	if strings.TrimSpace(patch) == "" {
		return ToolResult{Success: false, Output: "patch is required"}
	}
	hunks, err := parsePatch(patch)
	if err != nil {
		return ToolResult{Success: false, Output: "invalid patch: " + err.Error()}
	}
	seen := map[string]bool{}
	for _, h := range hunks {
		if seen[h.path] {
			continue
		}
		seen[h.path] = true
		if _, err := e.Perm.SafePath(h.path); err != nil {
			return ToolResult{Success: false, Output: err.Error()}
		}
		kind := PermWrite
		if h.kind == "delete" {
			kind = PermDelete
		}
		if allowed, err := e.Perm.CheckWithPreview(kind, h.path, "apply_patch", "", patch); err != nil || !allowed {
			if err != nil {
				return ToolResult{Success: false, Output: "permission denied: " + err.Error()}
			}
			return ToolResult{Success: false, Output: "permission denied by operator"}
		}
	}
	var outs []string
	for _, h := range hunks {
		res := e.applyHunk(h)
		if !res.Success {
			return res
		}
		outs = append(outs, res.Output)
	}
	return ToolResult{Success: true, Output: strings.Join(outs, "\n")}
}

// executeMcpTool resolves mcp__<server>__<tool> and forwards the call to the
// connected server. The call is gated by the EXECUTE permission so operators
// keep control over arbitrary third-party tool execution.
func (e *Engine) executeMcpTool(ctx context.Context, name string, args map[string]any) ToolResult {
	rest := strings.TrimPrefix(name, mcpToolPrefix)
	idx := strings.Index(rest, "__")
	if idx <= 0 {
		return ToolResult{Success: false, Output: "malformed mcp tool name: " + name}
	}
	serverID := rest[:idx]
	toolName := rest[idx+2:]
	if e.mcpToolDisabled(serverID, toolName) {
		return ToolResult{Success: false, Output: fmt.Sprintf("mcp tool %s.%s is disabled by config", serverID, toolName)}
	}
	e.mu.Lock()
	var client mcp.ToolClient
	for _, c := range e.mcpClients {
		if c.ID() == serverID {
			client = c
			break
		}
	}
	e.mu.Unlock()
	if client == nil {
		return ToolResult{Success: false, Output: fmt.Sprintf("mcp server %q is not connected", serverID)}
	}
	allowed, err := e.Perm.Check(PermExecute, "mcp:"+serverID+":"+toolName, "call MCP tool", "")
	if err != nil {
		return ToolResult{Success: false, Output: "permission denied: " + err.Error()}
	}
	if !allowed {
		return ToolResult{Success: false, Output: "permission denied by operator"}
	}
	res, err := client.CallTool(ctx, toolName, args)
	if err != nil {
		return ToolResult{Success: false, Output: "mcp call failed: " + err.Error()}
	}
	return ToolResult{Success: !res.IsError, Output: res.Text}
}

func (e *Engine) toolSearch(ctx context.Context, args map[string]any) ToolResult {
	query := argString(args, "query", "")
	if query == "" {
		return ToolResult{Success: false, Output: "query is required"}
	}
	limit := argInt(args, "max_results", 20)
	results, err := e.Index.Search(ctx, query, limit)
	if err != nil {
		return ToolResult{Success: false, Output: "search failed: " + err.Error()}
	}
	if len(results) == 0 {
		return ToolResult{Success: true, Output: "No matches found."}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d result(s):\n", len(results))
	for _, r := range results {
		fmt.Fprintf(&b, "%s:%d: %s\n", relPath(e.Root, r.Path), r.Line, r.Content)
	}
	return ToolResult{Success: true, Output: b.String()}
}

func (e *Engine) toolRead(args map[string]any) ToolResult {
	path, err := e.Perm.SafePath(argString(args, "path", ""))
	if err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	start := argInt(args, "start_line", 1)
	end := argInt(args, "end_line", len(lines))
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return ToolResult{Success: false, Output: "start_line must be <= end_line"}
	}
	var b strings.Builder
	for i := start - 1; i < end; i++ {
		fmt.Fprintf(&b, "%6d │ %s\n", i+1, lines[i])
	}
	out := b.String()
	if len(out) > toolOutputLimit {
		out = out[:toolOutputLimit] + "\n...[truncated]"
	}
	return ToolResult{Success: true, Output: out, Metadata: map[string]string{"path": relPath(e.Root, path), "lines": fmt.Sprint(end - start + 1)}}
}

func (e *Engine) toolListDir(args map[string]any) ToolResult {
	dir, err := e.Perm.SafePath(argString(args, "path", "."))
	if err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	max := argInt(args, "max_entries", 100)
	var b strings.Builder
	fmt.Fprintf(&b, "%s:\n", relPath(e.Root, dir))
	count := 0
	for _, en := range entries {
		name := en.Name()
		if en.IsDir() {
			name += "/"
		} else if info, err := en.Info(); err == nil {
			name = fmt.Sprintf("%s (%d B)", name, info.Size())
		}
		fmt.Fprintln(&b, "  "+name)
		count++
		if count >= max {
			fmt.Fprintf(&b, "  ... %d more entries\n", len(entries)-count)
			break
		}
	}
	return ToolResult{Success: true, Output: b.String()}
}

func (e *Engine) toolEdit(args map[string]any) ToolResult {
	path, err := e.Perm.SafePath(argString(args, "path", ""))
	if err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	old := argString(args, "old_string", "")
	newStr := argString(args, "new_string", "")
	if old == "" {
		return ToolResult{Success: false, Output: "old_string is required"}
	}
	if _, err := os.Stat(path); err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	before, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	content := string(before)
	normalizedOld := strings.ReplaceAll(strings.ReplaceAll(old, "\r\n", "\n"), "\r", "\n")
	normalized := strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	idx := strings.Index(normalized, normalizedOld)
	if idx < 0 {
		return ToolResult{Success: false, Output: fmt.Sprintf("old_string not found in %s. Provide exact text with enough context.", relPath(e.Root, path))}
	}
	if argBool(args, "replace_all", false) {
		normalized = strings.ReplaceAll(normalized, normalizedOld, newStr)
	} else {
		normalized = strings.Replace(normalized, normalizedOld, newStr, 1)
	}
	// Preserve original newline style when possible.
	out := normalized
	if strings.Contains(string(before), "\r\n") {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	diff := simpleDiff(string(before), out, relPath(e.Root, path))
	if allowed, err := e.Perm.CheckWithPreview(PermWrite, relPath(e.Root, path), "edit_file", "", diff); err != nil || !allowed {
		if err != nil {
			return ToolResult{Success: false, Output: "permission denied: " + err.Error()}
		}
		return ToolResult{Success: false, Output: "permission denied by operator"}
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	e.Index.Touch(path)
	e.recordFileChange(relPath(e.Root, path), diff)
	return ToolResult{Success: true, Output: "Edited " + relPath(e.Root, path) + "\n\n" + diff}
}

func (e *Engine) toolWrite(args map[string]any) ToolResult {
	path, err := e.Perm.SafePath(argString(args, "path", ""))
	if err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	content := argString(args, "content", "")
	var before []byte
	if b, err := os.ReadFile(path); err == nil {
		before = b
	}
	diff := simpleDiff(string(before), content, relPath(e.Root, path))
	if allowed, err := e.Perm.CheckWithPreview(PermWrite, relPath(e.Root, path), "write_file", "", diff); err != nil || !allowed {
		if err != nil {
			return ToolResult{Success: false, Output: "permission denied: " + err.Error()}
		}
		return ToolResult{Success: false, Output: "permission denied by operator"}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	e.Index.Touch(path)
	e.recordFileChange(relPath(e.Root, path), diff)
	return ToolResult{Success: true, Output: "Wrote " + relPath(e.Root, path) + "\n\n" + diff}
}

func (e *Engine) toolRun(ctx context.Context, args map[string]any) ToolResult {
	command := argString(args, "command", "")
	if command == "" {
		return ToolResult{Success: false, Output: "command is required"}
	}
	desc := argString(args, "description", "run command: "+command)
	timeout := time.Duration(argInt(args, "timeout_seconds", e.Config.TimeoutSeconds)) * time.Second
	return e.runShell(ctx, command, desc, timeout)
}

func (e *Engine) toolTests(ctx context.Context, args map[string]any) ToolResult {
	command := argString(args, "command", "")
	if command == "" {
		command = DetectTestCommand(e.Root)
	}
	if command == "" {
		return ToolResult{Success: false, Output: "no test runner detected (looked for go.mod, Cargo.toml, package.json, pytest, Makefile, gradle)"}
	}
	return e.runShell(ctx, command, "run tests: "+command, time.Duration(e.Config.TimeoutSeconds)*time.Second)
}

func (e *Engine) toolBuild(ctx context.Context, args map[string]any) ToolResult {
	command := argString(args, "command", "")
	if command == "" {
		command = DetectBuildCommand(e.Root)
	}
	if command == "" {
		return ToolResult{Success: false, Output: "no build command detected"}
	}
	return e.runShell(ctx, command, "run build: "+command, time.Duration(e.Config.TimeoutSeconds)*time.Second)
}

func (e *Engine) toolGitDiff(args map[string]any) ToolResult {
	p := argString(args, "path", "")
	if p == "" {
		return ToolResult{Success: true, Output: e.Git.Diff()}
	}
	full, err := e.Perm.SafePath(p)
	if err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	rel, _ := filepath.Rel(e.Root, full)
	out, err := e.Git.Output("diff", "--", filepath.ToSlash(rel))
	if err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	return ToolResult{Success: true, Output: out}
}

func (e *Engine) toolAskUser(args map[string]any) ToolResult {
	q := argString(args, "question", "")
	if q == "" {
		return ToolResult{Success: false, Output: "question is required"}
	}
	answer, err := e.askUser(q)
	if err != nil {
		return ToolResult{Success: false, Output: "ask_user failed: " + err.Error()}
	}
	return ToolResult{Success: true, Output: "Human answer: " + answer}
}

func (e *Engine) toolVerify(ctx context.Context, args map[string]any) ToolResult {
	_ = argString(args, "scope", "")
	return e.Verify(ctx)
}

// streamBatchSize coalesces stdout/stderr lines into chunks of this size
// before emitting EvToolStream, bounding the UI event rate for chatty tools.
const streamBatchSize = 10

// runShell executes a command with permission checking, evidence capture and
// live output streaming (Codex exec-server style): stdout/stderr lines are
// batched and emitted as EvToolStream events while the process runs, so the
// TUI and headless CLI show partial output instead of waiting for exit. The
// process is bound to ctx (cancelled by ctrl+c in the TUI) and an optional
// timeout.
func (e *Engine) runShell(ctx context.Context, command, desc string, timeout time.Duration) ToolResult {
	allowed, err := e.Perm.Check(PermExecute, command, desc, command)
	if err != nil {
		return ToolResult{Success: false, Output: "permission denied: " + err.Error()}
	}
	if !allowed {
		return ToolResult{Success: false, Output: "permission denied by operator"}
	}
	runCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	cmd := shellCommand(runCtx, command)
	cmd.Dir = e.Root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	if err := cmd.Start(); err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}

	var (
		outMu sync.Mutex
		out   strings.Builder
		wg    sync.WaitGroup
	)
	pump := func(r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		var batch strings.Builder
		flush := func() {
			if batch.Len() == 0 {
				return
			}
			chunk := batch.String()
			outMu.Lock()
			out.WriteString(chunk)
			outMu.Unlock()
			e.emit(EvToolStream, map[string]any{"chunk": chunk})
			batch.Reset()
		}
		lines := 0
		for sc.Scan() {
			batch.WriteString(sc.Text())
			batch.WriteString("\n")
			lines++
			if lines >= streamBatchSize {
				flush()
				lines = 0
			}
		}
		flush() // trailing partial batch
	}
	wg.Add(2)
	go pump(stdout)
	go pump(stderr)
	waitErr := cmd.Wait()
	wg.Wait() // drain anything still in flight after exit

	outMu.Lock()
	full := out.String()
	outMu.Unlock()
	truncated := full
	if len(truncated) > toolOutputLimit {
		truncated = truncated[:toolOutputLimit] + "\n...[truncated]"
	}
	res := ToolResult{
		Success:  waitErr == nil,
		Output:   truncated,
		Metadata: map[string]string{"command": command, "exit_code": exitCodeString(waitErr)},
	}
	kind := EvidenceKindForCommand(command)
	e.recordEvidence(kind, desc, truncated, res.Success, map[string]string{
		"command": command, "exit_code": exitCodeString(waitErr),
	})
	return res
}

func exitCodeString(err error) string {
	if err == nil {
		return "0"
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return fmt.Sprint(ee.ExitCode())
	}
	return "-1"
}

// Verify runs tests and build, recording evidence and claims.
func (e *Engine) Verify(ctx context.Context) ToolResult {
	testCmd := DetectTestCommand(e.Root)
	buildCmd := DetectBuildCommand(e.Root)
	var b strings.Builder
	ok := true
	if testCmd != "" {
		res := e.runShell(ctx, testCmd, "verify tests: "+testCmd, time.Duration(e.Config.TimeoutSeconds)*time.Second)
		fmt.Fprintf(&b, "TESTS (%s): %s\n%s\n", testCmd, statusWord(res.Success), res.Output)
		if !res.Success {
			ok = false
		}
		e.recordTestClaim(res.Success, testCmd)
		if !res.Success {
			e.discoverUnknown(fmt.Sprintf("Test suite fails: %s", testCmd), 0.8, 0.3, 0.4, "verify")
		}
	} else {
		fmt.Fprintln(&b, "TESTS: no runner detected")
	}
	if buildCmd != "" {
		res := e.runShell(ctx, buildCmd, "verify build: "+buildCmd, time.Duration(e.Config.TimeoutSeconds)*time.Second)
		fmt.Fprintf(&b, "BUILD (%s): %s\n%s\n", buildCmd, statusWord(res.Success), res.Output)
		if !res.Success {
			ok = false
		}
		if !res.Success {
			e.discoverUnknown(fmt.Sprintf("Build fails: %s", buildCmd), 0.9, 0.3, 0.3, "verify")
		}
	} else {
		fmt.Fprintln(&b, "BUILD: no build command detected")
	}
	// Fresh claims were recorded at the current code state; anything older
	// becomes STALE (also covers `astra verify` run outside a session).
	e.ReconcileClaims()
	return ToolResult{Success: ok, Output: strings.TrimSpace(b.String())}
}

func statusWord(ok bool) string {
	if ok {
		return "PASSED"
	}
	return "FAILED"
}

// DetectTestCommand guesses the test runner for a project.
func DetectTestCommand(root string) string {
	if fileExists(root, "go.mod") {
		return "go test ./..."
	}
	if fileExists(root, "Cargo.toml") {
		return "cargo test"
	}
	if fileExists(root, "package.json") {
		if hasNpmScript(root, "test") {
			return "npm test"
		}
	}
	if fileExists(root, "pyproject.toml") || fileExists(root, "setup.py") || fileExists(root, "requirements.txt") {
		return "python3 -m pytest -q"
	}
	if fileExists(root, "Makefile") {
		return "make test"
	}
	if fileExists(root, "build.gradle") || fileExists(root, "build.gradle.kts") {
		return "./gradlew test"
	}
	return ""
}

// DetectBuildCommand guesses the build command for a project.
func DetectBuildCommand(root string) string {
	if fileExists(root, "go.mod") {
		return "go build ./..."
	}
	if fileExists(root, "Cargo.toml") {
		return "cargo build"
	}
	if fileExists(root, "package.json") {
		if hasNpmScript(root, "build") {
			return "npm run build"
		}
		if hasNpmScript(root, "typecheck") {
			return "npm run typecheck"
		}
	}
	return ""
}

func fileExists(root, name string) bool {
	_, err := os.Stat(filepath.Join(root, name))
	return err == nil
}

func hasNpmScript(root, script string) bool {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return false
	}
	_, ok := pkg.Scripts[script]
	return ok
}

func EvidenceKindForCommand(cmd string) string {
	lower := strings.ToLower(cmd)
	switch {
	case strings.Contains(lower, "test") || strings.Contains(lower, "pytest") || strings.Contains(lower, "jest") || strings.Contains(lower, "vitest"):
		return core.EvidenceTestResult
	case strings.Contains(lower, "build") || strings.Contains(lower, "compile") || strings.Contains(lower, "typecheck"):
		return core.EvidenceBuildResult
	default:
		return core.EvidenceRuntimeResult
	}
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

func simpleDiff(before, after, name string) string {
	beforeLines := splitLines(before)
	afterLines := splitLines(after)
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", name, name)
	max := len(beforeLines)
	if len(afterLines) > max {
		max = len(afterLines)
	}
	for i := 0; i < max; i++ {
		var bl, al string
		if i < len(beforeLines) {
			bl = beforeLines[i]
		}
		if i < len(afterLines) {
			al = afterLines[i]
		}
		switch {
		case bl != al && bl != "" && al != "":
			fmt.Fprintf(&b, "-%s\n+%s\n", bl, al)
		case bl != "" && al == "":
			fmt.Fprintf(&b, "-%s\n", bl)
		case bl == "" && al != "":
			fmt.Fprintf(&b, "+%s\n", al)
		}
	}
	return b.String()
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func argString(args map[string]any, key, def string) string {
	if args == nil {
		return def
	}
	if v, ok := args[key]; ok {
		switch t := v.(type) {
		case string:
			return t
		case float64:
			return fmt.Sprint(t)
		case bool:
			return fmt.Sprint(t)
		}
	}
	return def
}

func argInt(args map[string]any, key string, def int) int {
	if args == nil {
		return def
	}
	if v, ok := args[key]; ok {
		switch t := v.(type) {
		case float64:
			return int(t)
		case int:
			return t
		case string:
			var n int
			if _, err := fmt.Sscanf(t, "%d", &n); err == nil {
				return n
			}
		}
	}
	return def
}

func argBool(args map[string]any, key string, def bool) bool {
	if args == nil {
		return def
	}
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}
