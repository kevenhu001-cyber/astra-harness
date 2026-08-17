package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kevenhu001-cyber/astra-harness/internal/core"
	"github.com/kevenhu001-cyber/astra-harness/internal/knowledge"
	"github.com/kevenhu001-cyber/astra-harness/internal/llm"
	"github.com/kevenhu001-cyber/astra-harness/internal/mcp"
)

// EventType is the UI/observer event taxonomy.
type EventType string

const (
	EvStatus         EventType = "status"
	EvDelta          EventType = "delta"
	EvAssistantStart EventType = "assistant_start"
	EvAssistantEnd   EventType = "assistant_end"
	EvToolStart      EventType = "tool_start"
	EvToolStream     EventType = "tool_stream"
	EvToolEnd        EventType = "tool_end"
	EvPermission     EventType = "permission"
	EvAskUser        EventType = "ask_user"
	EvEvidence       EventType = "evidence"
	EvUnknown        EventType = "unknown"
	EvClaim          EventType = "claim"
	EvAction         EventType = "action"
	EvGoal           EventType = "goal"
	EvSystem         EventType = "system"
	EvError          EventType = "error"
	EvDone           EventType = "done"
	EvUsage          EventType = "usage"
	EvMessage        EventType = "message"
)

// Event carries state transitions to observers.
type Event struct {
	Type EventType
	Data any
	Time time.Time
}

// Engine is the disposable agent runtime. Durable intelligence lives in
// Store; the engine can be killed, restarted or swapped at any time.
type Engine struct {
	Root     string
	Config   *Config
	Store    *core.Store
	Index    *knowledge.Index
	Git      *knowledge.Git
	Router   *llm.Router
	Provider llm.Provider
	Model    string
	Perm     *PermissionManager
	Events   chan Event

	mu        sync.Mutex
	session   *core.Session
	messages  []llm.Message
	usage     llm.Usage
	lastEdits []string
	askChans  map[string]chan string
	permChans map[string]chan PermissionDecision
	cancel    context.CancelFunc
	running   bool

	mcpClients []mcp.ToolClient
}

// NewEngine wires store, index, router and permissions for a workspace.
func NewEngine(root string, cfg *Config) (*Engine, error) {
	return NewEngineWithProgress(root, cfg, nil)
}

// NewEngineWithProgress is NewEngine with an optional index progress callback
// (done, total files) invoked while the first knowledge index is built.
func NewEngineWithProgress(root string, cfg *Config, progress func(done, total int)) (*Engine, error) {
	st, err := core.NewStore(root)
	if err != nil {
		return nil, err
	}
	g := &knowledge.Git{Root: root}
	ix := knowledge.NewIndex(root)
	if loaded, err := knowledge.LoadIndex(root); err == nil {
		ix = loaded
	} else {
		if err := ix.BuildWithProgress(progress); err != nil {
			return nil, err
		}
		if err := ix.Save(); err != nil {
			return nil, err
		}
	}
	providers := BuildProviders(cfg)
	router := llm.NewRouter(providers, cfg.DefaultProvider, cfg.DefaultModel)
	provider, model, err := router.Default()
	if err != nil {
		return nil, err
	}
	e := &Engine{
		Root: root, Config: cfg, Store: st, Index: ix, Git: g, Router: router,
		Provider: provider, Model: model,
		// Keep enough room for normal streaming bursts, while emit() still
		// preserves terminal events when a renderer is temporarily busy.
		Events:    make(chan Event, 1024),
		askChans:  map[string]chan string{},
		permChans: map[string]chan PermissionDecision{},
	}
	e.Perm = NewPermissionManager(root, cfg.PermissionMode, e.promptPermission)
	e.initProject()
	e.startMcpServers()
	return e, nil
}

// Close releases engine resources (MCP server processes and the state store).
func (e *Engine) Close() error {
	e.mu.Lock()
	clients := e.mcpClients
	e.mcpClients = nil
	e.mu.Unlock()
	for _, c := range clients {
		_ = c.Close()
	}
	if e.Store != nil {
		return e.Store.Close()
	}
	return nil
}

// startMcpServers spawns configured stdio MCP servers and records their tools.
// A failing or unresponsive server is reported as a system event but does not
// abort startup (each handshake is bounded by mcpStartTimeout).
const mcpStartTimeout = 15 * time.Second

func (e *Engine) startMcpServers() {
	for _, sc := range e.Config.McpServers {
		ctx, cancel := context.WithTimeout(context.Background(), mcpStartTimeout)
		mcfg := mcp.ServerConfig{
			ID: sc.ID, Type: sc.Type, Command: sc.Command, Args: sc.Args,
			Env: sc.Env, URL: sc.URL, Headers: sc.Headers,
		}
		var client mcp.ToolClient
		var err error
		if sc.Type == "http" {
			client, err = mcp.StartHTTP(ctx, mcfg)
		} else {
			client, err = mcp.Start(ctx, mcfg)
		}
		cancel()
		if err != nil {
			e.emit(EvSystem, fmt.Sprintf("mcp: server %q failed to start: %s", sc.ID, firstLine(err.Error())))
			continue
		}
		tools, err := client.ListTools()
		if err != nil {
			e.emit(EvSystem, fmt.Sprintf("mcp: server %q tools/list failed: %s", sc.ID, firstLine(err.Error())))
			_ = client.Close()
			continue
		}
		exposed := 0
		for _, t := range tools {
			if !e.mcpToolDisabled(sc.ID, t.Name) {
				exposed++
			}
		}
		if exposed == 0 {
			e.emit(EvSystem, fmt.Sprintf("mcp: server %q exposed no enabled tools", sc.ID))
			_ = client.Close()
			continue
		}
		e.mu.Lock()
		e.mcpClients = append(e.mcpClients, client)
		e.mu.Unlock()
		e.emit(EvSystem, fmt.Sprintf("mcp: connected %q with %d tool(s) (%d enabled)", sc.ID, len(tools), exposed))
	}
}

// mcpToolDisabled reports whether a tool is disabled by per-tool config
// (Codex [mcp_servers.<name>.tools.<tool>].disabled).
func (e *Engine) mcpToolDisabled(serverID, tool string) bool {
	for _, sc := range e.Config.McpServers {
		if sc.ID != serverID {
			continue
		}
		if t, ok := sc.Tools[tool]; ok && t.Disabled {
			return true
		}
	}
	return false
}

// McpToolNames returns "server-id/tool" pairs for all connected servers
// (used by /mcp and /debug overlays).
func (e *Engine) McpToolNames() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, c := range e.mcpClients {
		for _, name := range c.ToolNames() {
			out = append(out, c.ID()+"/"+name)
		}
	}
	return out
}

func (e *Engine) initProject() {
	if e.Store.State.Project != nil {
		return
	}
	langs := map[string]int{}
	for lang, n := range e.Index.Languages {
		langs[lang] = n
	}
	branch := e.Git.Branch()
	if branch == "" {
		branch = e.Git.DefaultBranch()
	}
	p := &core.Project{
		Name: filepath.Base(e.Root), Root: e.Root, DefaultBranch: branch,
		Languages: langs, InitializedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	_ = e.Store.SetProject(p)
}

// SetPermissionPrompt overrides the interactive prompt (used by headless runs).
func (e *Engine) SetPermissionPrompt(fn PromptFunc) {
	e.Perm.Prompt = fn
}

// Run executes the uncertainty-driven loop for one user prompt.
func (e *Engine) Run(ctx context.Context, prompt string) error {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return errors.New("engine already running")
	}
	e.running = true
	runCtx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.running = false
		e.mu.Unlock()
	}()

	if e.Provider == nil || !e.Provider.Available() {
		err := fmt.Errorf("provider %q is not configured; set its API key (e.g. OPENAI_API_KEY, ANTHROPIC_AUTH_TOKEN, DEEPSEEK_API_KEY) or run /model", e.ProviderID())
		e.emit(EvError, err.Error())
		e.emit(EvDone, map[string]any{"error": err.Error()})
		return err
	}

	goal := e.ensureGoal(prompt)
	e.emit(EvGoal, goal)
	e.addMessage(llm.RoleUser, prompt)
	e.saveSession()

	// Re-evaluate claim/evidence validity against the current working tree
	// before the model sees the compiled state (evidence invalidation).
	e.ReconcileClaims()
	if msg := e.memorySummary(); msg != "" {
		e.emit(EvSystem, msg)
	}

	maxIter := e.Config.MaxIterations
	if maxIter <= 0 {
		maxIter = 20
	}
	var lastErr error
	for iter := 1; iter <= maxIter; iter++ {
		e.emit(EvStatus, fmt.Sprintf("Step %d/%d — compiling state & planning", iter, maxIter))
		if err := runCtx.Err(); err != nil {
			lastErr = err
			break
		}
		// Auto-compact when the estimated context approaches the token budget
		// (Codex: CompactionTrigger::Auto in compact_token_budget.rs).
		if e.shouldAutoCompact() {
			e.emit(EvSystem, fmt.Sprintf("context at ~%d tokens — auto-compacting", e.estimateTokens()))
			if msg := e.Compact(); msg != "nothing to compact" {
				e.emit(EvSystem, msg)
				e.saveSession()
			}
		}
		content, calls, finish, err := e.callModel(runCtx)
		if err != nil {
			lastErr = err
			e.emit(EvError, err.Error())
			break
		}
		if content != "" || len(calls) > 0 {
			e.addMessage(llm.RoleAssistant, content)
			last := e.messages[len(e.messages)-1]
			last.ToolCalls = calls
			e.messages[len(e.messages)-1] = last
		}
		e.emit(EvAssistantEnd, map[string]any{"content": content, "finish_reason": finish})
		e.saveSession()
		if len(calls) == 0 {
			break
		}
		for _, call := range calls {
			e.emit(EvToolStart, map[string]any{"name": call.Name, "arguments": call.Arguments})
			action := e.createAction(call.Name, call.Arguments)
			res := e.ExecuteTool(runCtx, call.Name, call.Arguments)
			e.finishAction(action, res)
			e.addMessage(llm.RoleTool, res.Output)
			msg := e.messages[len(e.messages)-1]
			msg.ToolCallID = call.ID
			msg.Name = call.Name
			e.messages[len(e.messages)-1] = msg
			e.emit(EvToolEnd, map[string]any{
				"name": call.Name, "success": res.Success, "output": res.Output,
				"action_id": action.ID,
			})
			e.saveSession()
			if !res.Success && isFatalTool(call.Name) {
				e.discoverUnknown(fmt.Sprintf("Tool %s failed: %s", call.Name, firstLine(res.Output)), 0.6, 0.3, 0.4, "tool")
			}
		}
		if iter >= maxIter {
			e.emit(EvSystem, fmt.Sprintf("Reached max iterations (%d); consider /verify or /compact", maxIter))
		}
	}

	// Auto-verify if code changed and verification was not already run.
	shouldVerify := e.Config.AutoVerify != nil && *e.Config.AutoVerify
	if shouldVerify && len(e.lastEdits) > 0 && lastErr == nil && !e.ranVerification() {
		e.emit(EvStatus, "Edits detected — running verification")
		e.Verify(runCtx)
	}
	e.updateGoalProgress(goal)
	e.emit(EvUsage, e.usage)
	e.emit(EvDone, map[string]any{"goal_id": goal.ID, "error": errString(lastErr)})
	e.saveSession()
	return lastErr
}

// Stop cancels the running agent loop.
func (e *Engine) Stop() {
	e.mu.Lock()
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.Unlock()
}

// retryableError marks a model-call failure that is safe to retry (transient
// network or HTTP 429/5xx) and that occurred before any output was produced.
type retryableError struct{ err error }

func (e retryableError) Error() string { return e.err.Error() }
func (e retryableError) Unwrap() error { return e.err }

// isRetryable reports whether a model-call failure can be retried with
// backoff (Codex: util::backoff). Deterministic failures (bad requests,
// auth, no provider) are not retried.
func isRetryable(err error) bool {
	var re retryableError
	if errors.As(err, &re) {
		var he *llm.HTTPStatusError
		if errors.As(re.err, &he) {
			return he.IsRetryable()
		}
		return true // request never reached the API (network-level failure)
	}
	return false
}

// callModel runs the model with retry-and-backoff for transient failures.
func (e *Engine) callModel(ctx context.Context) (string, []llm.ToolCall, string, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		content, calls, finish, err := e.callModelOnce(ctx)
		if err == nil {
			return content, calls, finish, nil
		}
		lastErr = err
		if !isRetryable(err) {
			break
		}
		if attempt >= maxAttempts {
			break
		}
		delay := time.Duration(1<<(attempt-1)) * time.Second
		e.emit(EvSystem, fmt.Sprintf("transient model error (attempt %d/%d), retrying in %s: %s",
			attempt, maxAttempts, delay, firstLine(err.Error())))
		select {
		case <-ctx.Done():
			return "", nil, "", ctx.Err()
		case <-time.After(delay):
		}
	}
	return "", nil, "", lastErr
}

func (e *Engine) callModelOnce(ctx context.Context) (string, []llm.ToolCall, string, error) {
	e.emit(EvAssistantStart, map[string]any{"model": e.Model})
	req := llm.Request{
		Model:       e.Model,
		System:      e.buildSystemPrompt(),
		Messages:    e.messages,
		Tools:       e.AllToolDefs(),
		MaxTokens:   4096,
		Temperature: 0.2,
	}
	stream, err := e.Provider.Stream(ctx, &req)
	if err != nil {
		return "", nil, "", retryableError{err}
	}
	var content strings.Builder
	acc := map[int]*llm.ToolCall{}
	var finish string
	emitted := false
	for ev := range stream {
		if ev.Error != nil {
			if emitted {
				return content.String(), nil, finish, ev.Error
			}
			return "", nil, "", retryableError{ev.Error}
		}
		if ev.Content != "" {
			emitted = true
			content.WriteString(ev.Content)
			e.emit(EvDelta, ev.Content)
		}
		for _, d := range ev.ToolCalls {
			emitted = true
			tc := acc[d.Index]
			if tc == nil {
				tc = &llm.ToolCall{}
				acc[d.Index] = tc
			}
			if d.ID != "" {
				tc.ID = d.ID
			}
			if d.Name != "" {
				tc.Name = d.Name
			}
			// Providers may emit cumulative argument text; only append the
			// un-seen suffix.
			if len(d.Arguments) > len(tc.Arguments) {
				tc.Arguments += d.Arguments[len(tc.Arguments):]
			}
		}
		if ev.Usage != nil {
			e.usage = *ev.Usage
		}
		if ev.FinishReason != "" {
			finish = ev.FinishReason
		}
	}
	calls := make([]llm.ToolCall, 0, len(acc))
	for _, tc := range acc {
		calls = append(calls, *tc)
	}
	return content.String(), calls, finish, nil
}

// CompilerOutput renders the current state for prompts and status views.
func (e *Engine) CompilerOutput() string {
	recent := make([]string, 0, 12)
	for _, a := range e.Store.ActionsRecent(6) {
		recent = append(recent, fmt.Sprintf("%s %s: %s", a.Status, a.Type, truncateText(a.Description, 100)))
	}
	for _, ev := range e.Store.EvidenceRecent(4) {
		recent = append(recent, fmt.Sprintf("%s: %s (%s)", ev.Kind, truncateText(ev.Source, 60), ev.Status))
	}
	compiler := core.NewCompiler()
	return compiler.Compile(e.Store.State, recent)
}

// agentsMDCandidates are the filenames scanned per directory, in priority
// order (local override first), mirroring Codex agents_md.rs.
var agentsMDCandidates = []string{"AGENTS.override.md", "AGENTS.md"}

// agentsMDSeparator joins multiple project docs, matching Codex's separator.
const agentsMDSeparator = "\n\n--- project-doc ---\n\n"

// LoadProjectInstructions collects project documentation from the project
// root down to the current working directory, concatenated root-first with a
// separator, capped at MaxProjectDocBytes. Mirrors Codex's AGENTS.md
// discovery (codex-rs/core/src/agents_md.rs): every directory between root
// and cwd (inclusive) contributes its first matching candidate file.
func (e *Engine) LoadProjectInstructions() string {
	maxBytes := e.Config.MaxProjectDocBytes
	if maxBytes <= 0 {
		maxBytes = defaultProjectDocBytes
	}
	root := filepath.Clean(e.Root)
	cwd, err := os.Getwd()
	if err != nil {
		cwd = root
	}
	cwd = filepath.Clean(cwd)

	// Collect the chain cwd → … → root, then reverse so root comes first.
	// When cwd is outside the project root, fall back to scanning root only.
	var dirs []string
	if rel, relErr := filepath.Rel(root, cwd); relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		dirs = []string{root}
	} else {
		cur := cwd
		for {
			dirs = append(dirs, cur)
			if cur == root {
				break
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				break // walked past the project root without finding it
			}
			cur = parent
		}
	}
	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}

	var parts []string
	remaining := maxBytes
	for _, dir := range dirs {
		if remaining <= 0 {
			break
		}
		for _, name := range agentsMDCandidates {
			p := filepath.Join(dir, name)
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			rel, _ := filepath.Rel(e.Root, p)
			header := fmt.Sprintf("# %s\n\n", filepath.ToSlash(rel))
			if len(header) >= remaining {
				break
			}
			content := string(data)
			if space := remaining - len(header); len(content) > space {
				content = content[:space]
			}
			remaining -= len(header) + len(content)
			parts = append(parts, header+content)
			break // only the first matching candidate per directory
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, agentsMDSeparator)
}

// autoCompactRatio triggers compaction at 80% of the configured budget.
const autoCompactRatio = 0.8

// estimateTokens approximates the model context size in tokens (chars/4) so
// we can trigger compaction before hitting the budget.
func (e *Engine) estimateTokens() int {
	total := len(e.buildSystemPrompt())
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, m := range e.messages {
		total += len(m.Content)
		for _, tc := range m.ToolCalls {
			total += len(tc.Arguments)
		}
	}
	return total / 4
}

// shouldAutoCompact reports whether the estimated context exceeds the
// configured budget threshold (Codex: CompactionTrigger::Auto).
func (e *Engine) shouldAutoCompact() bool {
	budget := e.Config.MaxContextTokens
	if budget <= 0 {
		return false
	}
	return e.estimateTokens() > int(float64(budget)*autoCompactRatio)
}

func (e *Engine) ensureGoal(prompt string) *core.Goal {
	if g := e.Store.ActiveGoal(); g != nil {
		return g
	}
	g := &core.Goal{
		ID: core.NewID("goal"), Description: prompt, Priority: 1, Status: core.StatusActive,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if len(prompt) > 160 {
		g.Description = prompt[:157] + "..."
	}
	_ = e.Store.AddGoal(g)
	return g
}

// SetGoal replaces/creates the active goal and records acceptance criteria.
func (e *Engine) SetGoal(description string, criteria []string) *core.Goal {
	g := &core.Goal{
		ID: core.NewID("goal"), Description: description, Priority: 1,
		Status: core.StatusActive, AcceptanceCriteria: criteria,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	for _, old := range e.Store.State.Goals {
		if old.Status == core.StatusActive {
			old.Status = core.StatusCancelled
			_ = e.Store.UpdateGoal(old)
		}
	}
	_ = e.Store.AddGoal(g)
	e.emit(EvGoal, g)
	return g
}

func (e *Engine) updateGoalProgress(goal *core.Goal) {
	if goal == nil {
		return
	}
	var verified, failed int
	for _, ev := range e.Store.State.Evidence {
		if ev.Kind == core.EvidenceTestResult || ev.Kind == core.EvidenceBuildResult {
			if ev.Status == "VALID" && ev.Confidence >= 0.8 {
				verified++
			} else if strings.Contains(ev.Source, "FAILED") || strings.Contains(ev.Content, "FAILED") {
				failed++
			}
		}
	}
	progress := 0.0
	if verified+failed > 0 {
		progress = float64(verified) / float64(verified+failed)
	}
	if len(e.lastEdits) > 0 && verified == 0 {
		progress = 0.25
	}
	goal.Progress = progress
	goal.UpdatedAt = time.Now().UTC()
	if progress >= 0.99 && len(goal.AcceptanceCriteria) == 0 {
		goal.Status = core.StatusDone
	}
	_ = e.Store.UpdateGoal(goal)
}

func (e *Engine) addMessage(role, content string) {
	e.mu.Lock()
	e.messages = append(e.messages, llm.Message{Role: role, Content: content})
	e.mu.Unlock()
	if e.session != nil {
		e.session.Messages = append(e.session.Messages, core.SessionMessage{
			Role: role, Content: truncateText(content, 500), Timestamp: time.Now().UTC(),
		})
	}
}

// Session helpers ------------------------------------------------------------

func (e *Engine) SessionID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session == nil {
		e.session = &core.Session{ID: core.NewID("ses"), Root: e.Root, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	}
	return e.session.ID
}

func (e *Engine) saveSession() {
	e.mu.Lock()
	if e.session == nil {
		e.session = &core.Session{ID: core.NewID("ses"), Root: e.Root, CreatedAt: time.Now().UTC()}
	}
	e.session.Provider = e.ProviderID()
	e.session.Model = e.Model
	e.session.UpdatedAt = time.Now().UTC()
	sess := e.session
	e.mu.Unlock()
	_ = e.Store.SaveSession(sess)
}

// LoadSession restores a previous session transcript into the engine.
func (e *Engine) LoadSession(sess *core.Session) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.session = sess
	e.messages = nil
	for _, m := range sess.Messages {
		if m.Role == "" {
			continue
		}
		msg := llm.Message{Role: m.Role, Content: m.Content}
		if m.Role == llm.RoleTool {
			msg.ToolCallID = m.ToolCallID
			msg.Name = m.ToolName
		}
		e.messages = append(e.messages, msg)
	}
	return nil
}

// BacktrackToUserMessage truncates the transcript after the n-th user message
// (0-based) and returns that message's content so the UI can re-edit it. This
// is the practical Codex backtrack behavior: no branch session is created.
func (e *Engine) BacktrackToUserMessage(n int) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	idx := -1
	count := -1
	for i, m := range e.messages {
		if m.Role == llm.RoleUser {
			count++
			if count == n {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		return "", fmt.Errorf("no such user message")
	}
	content := e.messages[idx].Content
	e.messages = e.messages[:idx+1]
	e.running = false
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	if e.session != nil {
		userSeen := -1
		for i, m := range e.session.Messages {
			if m.Role == llm.RoleUser {
				userSeen++
				if userSeen == n {
					e.session.Messages = e.session.Messages[:i+1]
					break
				}
			}
		}
	}
	return content, nil
}

// BranchBacktrackToUserMessage is the full Codex backtrack: truncate the
// transcript after the n-th user message and keep the result as a new branch
// session, leaving the original session untouched on disk.
func (e *Engine) BranchBacktrackToUserMessage(n int) (string, error) {
	e.mu.Lock()
	idx := -1
	count := -1
	for i, m := range e.messages {
		if m.Role == llm.RoleUser {
			count++
			if count == n {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		e.mu.Unlock()
		return "", fmt.Errorf("no such user message")
	}
	content := e.messages[idx].Content
	e.messages = e.messages[:idx+1]
	e.running = false
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	sess := &core.Session{
		ID:        core.NewID("ses"),
		Root:      e.Root,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Provider:  e.ProviderID(),
		Model:     e.Model,
	}
	for _, m := range e.messages {
		sess.Messages = append(sess.Messages, core.SessionMessage{
			Role: m.Role, Content: truncateText(m.Content, 500),
			ToolCallID: m.ToolCallID, ToolName: m.Name,
			Timestamp: time.Now().UTC(),
		})
	}
	e.session = sess
	e.mu.Unlock()
	_ = e.Store.SaveSession(sess)
	return content, nil
}

// ProviderID returns the active provider id (safe for display).
func (e *Engine) ProviderID() string {
	if e.Provider != nil {
		return e.Provider.ID()
	}
	return ""
}

// SwitchModel changes provider/model at runtime and tracks the choice in
// recent-models (persisted to .astra/config.json).
func (e *Engine) SwitchModel(providerID, model string) error {
	p, m, err := e.Router.Pick(providerID, model)
	if err != nil {
		return err
	}
	if !p.Available() {
		return fmt.Errorf("provider %s is not available (missing API key?)", p.ID())
	}
	e.mu.Lock()
	e.Provider = p
	e.Model = m
	e.mu.Unlock()
	e.Config.DefaultProvider = p.ID()
	e.Config.DefaultModel = m
	if id := p.ID() + "|" + m; id != "" {
		e.PushRecentModel(id)
		_ = SaveConfig(e.Root, e.Config)
	}
	return nil
}

// UpdateProvider changes a provider's base URL, API key, or model list and
// persists the change to .astra/config.json. Empty fields keep current values;
// a non-empty model also becomes the default model for the provider.
func (e *Engine) UpdateProvider(id, baseURL, apiKey, model string) error {
	e.mu.Lock()
	idx := -1
	for i := range e.Config.Providers {
		if e.Config.Providers[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		e.mu.Unlock()
		return fmt.Errorf("unknown provider %q", id)
	}
	p := &e.Config.Providers[idx]
	if baseURL != "" {
		p.BaseURL = baseURL
	}
	if apiKey != "" {
		p.APIKey = apiKey
	}
	if model != "" {
		found := false
		for _, m := range p.Models {
			if m == model {
				found = true
				break
			}
		}
		if !found {
			p.Models = append(p.Models, model)
		}
		e.Config.DefaultProvider = id
		e.Config.DefaultModel = model
	}
	e.mu.Unlock()
	if err := SaveConfig(e.Root, e.Config); err != nil {
		return err
	}
	e.refreshRouter()
	return nil
}

// UpsertProvider replaces an existing provider (matched by ID) or appends a
// new one, then persists and rebuilds the router. An empty APIKey keeps the
// previously stored key so a masked key field can be left untouched.
func (e *Engine) UpsertProvider(p ProviderConfig) error {
	if p.ID == "" {
		return fmt.Errorf("provider id is required")
	}
	e.mu.Lock()
	idx := -1
	for i := range e.Config.Providers {
		if e.Config.Providers[i].ID == p.ID {
			idx = i
			break
		}
	}
	if idx >= 0 {
		if p.APIKey == "" {
			p.APIKey = e.Config.Providers[idx].APIKey
		}
		e.Config.Providers[idx] = p
	} else {
		e.Config.Providers = append(e.Config.Providers, p)
	}
	e.mu.Unlock()
	if err := SaveConfig(e.Root, e.Config); err != nil {
		return err
	}
	e.refreshRouter()
	return nil
}

// DeleteProvider removes a provider by ID and persists the change. If the
// deleted provider was the active one, the engine falls back to the first
// remaining provider/model.
func (e *Engine) DeleteProvider(id string) error {
	e.mu.Lock()
	out := make([]ProviderConfig, 0, len(e.Config.Providers))
	removed := false
	for _, p := range e.Config.Providers {
		if p.ID == id {
			removed = true
			continue
		}
		out = append(out, p)
	}
	e.Config.Providers = out
	if e.ProviderID() == id {
		if len(out) > 0 {
			e.Config.DefaultProvider = out[0].ID
			if len(out[0].Models) > 0 {
				e.Config.DefaultModel = out[0].Models[0]
			}
		} else {
			e.Config.DefaultProvider = ""
			e.Config.DefaultModel = ""
		}
	}
	e.mu.Unlock()
	if err := SaveConfig(e.Root, e.Config); err != nil {
		return err
	}
	e.refreshRouter()
	if removed && e.ProviderID() == "" && len(e.Config.Providers) > 0 {
		first := e.Config.Providers[0]
		_ = e.SwitchModel(first.ID, firstModel(first))
	}
	return nil
}

func firstModel(p ProviderConfig) string {
	if len(p.Models) > 0 {
		return p.Models[0]
	}
	return ""
}

// refreshRouter rebuilds providers from Config and keeps the active provider
// when it is still available after a configuration change.
func (e *Engine) refreshRouter() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Router = llm.NewRouter(BuildProviders(e.Config), e.Config.DefaultProvider, e.Config.DefaultModel)
	if e.Provider != nil {
		if p, m, err := e.Router.Pick(e.Provider.ID(), e.Model); err == nil && p.Available() {
			e.Provider = p
			e.Model = m
			return
		}
	}
	if p, m, err := e.Router.Default(); err == nil && p.Available() {
		e.Provider = p
		e.Model = m
	}
}

// State recording ------------------------------------------------------------

func (e *Engine) recordEvidence(kind, source, content string, success bool, meta map[string]string) {
	sum := sha256.Sum256([]byte(kind + "|" + source + "|" + content))
	ev := &core.Evidence{
		ID: core.NewID("ev"), Kind: kind, Source: source, Content: truncateText(content, 4000),
		Hash: hex.EncodeToString(sum[:]), CodeState: e.Git.StateHash(),
		Confidence: 0.9, Status: "VALID", Metadata: meta,
		CreatedAt: time.Now().UTC(),
	}
	if !success {
		ev.Confidence = 0.5
		ev.Status = "INVALID"
	}
	_ = e.Store.AddEvidence(ev)
	e.emit(EvEvidence, ev)
}

func (e *Engine) recordTestClaim(success bool, command string) {
	status := core.ClaimVerified
	confidence := 0.9
	obj := "passes"
	if !success {
		status = core.ClaimContradicted
		confidence = 0.5
		obj = "fails"
	}
	c := &core.Claim{
		ID: core.NewID("clm"), Subject: "test suite", Predicate: obj, Object: command,
		ClaimType: "TEST_RESULT", Status: status, Confidence: confidence,
		Source: "verify", CodeState: e.Git.StateHash(),
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	// Link recent evidence produced by the same command, skipping records
	// invalidated by code changes (STALE).
	for _, ev := range e.Store.State.Evidence {
		if ev.Status == core.EvidenceStale {
			continue
		}
		if strings.Contains(ev.Source, command) || ev.Metadata["command"] == command {
			c.EvidenceIDs = append(c.EvidenceIDs, ev.ID)
		}
	}
	_ = e.Store.AddClaim(c)
	e.emit(EvClaim, c)
}

func (e *Engine) discoverUnknown(desc string, impact, cost, confidence float64, source string) {
	norm := strings.ToLower(strings.Join(strings.Fields(desc), " "))
	for _, u := range e.Store.State.Unknowns {
		if strings.EqualFold(strings.Join(strings.Fields(u.Description), " "), norm) {
			return
		}
	}
	u := &core.Unknown{
		ID: core.NewID("unk"), Description: desc, Impact: impact,
		Confidence: confidence, ResolutionCost: cost, DependencyWeight: 0.2,
		Status: core.StatusActive, Source: source,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	u.ComputePriority()
	_ = e.Store.AddUnknown(u)
	e.emit(EvUnknown, u)
}

func (e *Engine) recordFileChange(path, diff string) {
	e.mu.Lock()
	e.lastEdits = append(e.lastEdits, path)
	e.mu.Unlock()
	e.recordEvidence(core.EvidenceSourceCode, "edit_file:"+path, diff, true, map[string]string{"path": path})
	related := e.Index.RelatedFiles([]string{filepath.Join(e.Root, path)})
	hasTest := false
	for _, r := range related {
		if e.Index.Files[r] != nil && e.Index.Files[r].IsTest {
			hasTest = true
			break
		}
	}
	if !hasTest {
		e.discoverUnknown(fmt.Sprintf("No test coverage identified for modified file %s", path), 0.5, 0.3, 0.6, "edit")
	}
	// The working tree changed: previously recorded evidence is no longer
	// valid for the current code state.
	e.ReconcileClaims()
}

// memorySummary describes the durable knowledge the current run inherits from
// prior sessions (cross-session memory activation). Empty when there is
// nothing to surface.
func (e *Engine) memorySummary() string {
	st := e.Store.State
	if len(st.Claims) == 0 && len(st.Unknowns) == 0 {
		return ""
	}
	verified := 0
	for _, c := range st.Claims {
		if c.Status == core.ClaimVerified {
			verified++
		}
	}
	// Surface the most goal-relevant verified conclusions, if any.
	top := ""
	if g := e.Store.ActiveGoal(); g != nil && verified > 0 {
		for _, c := range core.RankClaimsByGoal(st.Claims, g.Description) {
			if c.Status != core.ClaimVerified {
				continue
			}
			top = fmt.Sprintf(" · top: %s %s %s", c.Subject, c.Predicate, c.Object)
			break
		}
	}
	return fmt.Sprintf("memory loaded from state: %d claim(s) (%d verified), %d unknown(s), %d evidence%s",
		len(st.Claims), verified, len(st.Unknowns), len(st.Evidence), top)
}

// ReconcileClaims applies the staleness rules from core.ReconcileState against
// the current git state hash and persists the transitions through the event
// log. Returns the number of records flagged STALE.
func (e *Engine) ReconcileClaims() int {
	evidence, claims := core.ReconcileState(e.Git.StateHash(), e.Store.State)
	changed := 0
	for _, ev := range evidence {
		ev.Status = core.EvidenceStale
		if e.Store.UpdateEvidence(ev) == nil {
			changed++
		}
	}
	for _, c := range claims {
		c.Status = core.ClaimStale
		if e.Store.UpdateClaim(c) == nil {
			changed++
		}
	}
	if changed > 0 {
		e.emit(EvSystem, fmt.Sprintf("code changed — %d claim/evidence record(s) marked STALE; re-verify to refresh", changed))
	}
	return changed
}

func (e *Engine) createAction(tool, argsJSON string) *core.Action {
	args := map[string]any{}
	_ = json.Unmarshal([]byte(argsJSON), &args)
	desc := tool + " " + truncateText(argsJSON, 120)
	a := &core.Action{
		ID: core.NewID("act"), Type: actionTypeForTool(tool), Tool: tool,
		Description: desc, Input: args, Status: core.StatusRunning,
		CreatedAt: time.Now().UTC(), StartedAt: time.Now().UTC(),
	}
	switch tool {
	case "search", "read", "list_dir", "git_status", "git_diff", "git_log":
		a.ExpectedInfoGain, a.Cost, a.Risk = 0.5, 0.1, 0.02
	case "edit_file", "write_file", "apply_patch":
		a.ExpectedGoalProgress, a.Cost, a.Risk = 0.35, 0.15, 0.3
	case "run_tests", "verify":
		a.ExpectedInfoGain, a.ExpectedGoalProgress, a.Cost, a.Risk = 0.7, 0.3, 0.35, 0.15
	case "run_build":
		a.ExpectedInfoGain, a.ExpectedGoalProgress, a.Cost, a.Risk = 0.6, 0.3, 0.35, 0.15
	case "run_command":
		a.ExpectedInfoGain, a.Cost, a.Risk = 0.4, 0.4, 0.5
	case "ask_user":
		a.ExpectedGoalProgress, a.Cost, a.Risk = 0.2, 0.05, 0.02
	default:
		a.ExpectedInfoGain, a.Cost, a.Risk = 0.3, 0.2, 0.2
	}
	a.ComputeUtility()
	_ = e.Store.AddAction(a)
	e.emit(EvAction, a)
	return a
}

func (e *Engine) finishAction(a *core.Action, res ToolResult) {
	a.Status = core.StatusSucceeded
	if !res.Success {
		a.Status = core.StatusFailed
	}
	a.FinishedAt = time.Now().UTC()
	a.ResultSummary = truncateText(res.Output, 300)
	if res.Success {
		a.ExpectedInfoGain = a.ExpectedInfoGain*0.8 + 0.2
	}
	a.ComputeUtility()
	_ = e.Store.UpdateAction(a)
}

func (e *Engine) ranVerification() bool {
	for _, a := range e.Store.ActionsRecent(20) {
		if (a.Tool == "verify" || a.Tool == "run_tests") && a.Status == core.StatusSucceeded &&
			time.Since(a.FinishedAt) < 10*time.Minute {
			return true
		}
	}
	return false
}

// Interactive channels -------------------------------------------------------

func (e *Engine) askUser(question string) (string, error) {
	id := core.NewID("ask")
	ch := make(chan string, 1)
	e.mu.Lock()
	e.askChans[id] = ch
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.askChans, id)
		e.mu.Unlock()
	}()
	e.emit(EvAskUser, map[string]any{"id": id, "question": question})
	select {
	case answer := <-ch:
		return answer, nil
	case <-time.After(30 * time.Minute):
		return "", errors.New("ask_user timed out")
	}
}

// AnswerAsk delivers the operator's answer to a pending ask_user tool.
func (e *Engine) AnswerAsk(id, answer string) bool {
	e.mu.Lock()
	ch := e.askChans[id]
	e.mu.Unlock()
	if ch == nil {
		return false
	}
	ch <- answer
	return true
}

func (e *Engine) promptPermission(req PermissionRequest) (PermissionDecision, error) {
	ch := make(chan PermissionDecision, 1)
	e.mu.Lock()
	e.permChans[req.ID] = ch
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.permChans, req.ID)
		e.mu.Unlock()
	}()
	e.emit(EvPermission, req)
	select {
	case dec := <-ch:
		return dec, nil
	case <-time.After(30 * time.Minute):
		return PermissionDecision{}, errors.New("permission prompt timed out")
	}
}

// AnswerPermission delivers the operator's decision.
func (e *Engine) AnswerPermission(id string, dec PermissionDecision) bool {
	e.mu.Lock()
	ch := e.permChans[id]
	e.mu.Unlock()
	if ch == nil {
		return false
	}
	ch <- dec
	return true
}

func (e *Engine) emit(t EventType, data any) {
	if e.Events == nil {
		return
	}
	ev := Event{Type: t, Data: data, Time: time.Now()}
	if terminalEvent(t) {
		// Dropping EvDone/EvPermission/EvAskUser is indistinguishable from a
		// frozen UI: the engine keeps waiting or the composer stays busy. These
		// state transitions must be delivered even when a large tool output
		// burst temporarily fills the queue.
		e.Events <- ev
		return
	}
	// Deltas are best-effort. Losing an intermediate frame is harmless and
	// prevents a chatty process from back-pressuring the agent forever.
	select {
	case e.Events <- ev:
	default:
	}
}

func terminalEvent(t EventType) bool {
	switch t {
	case EvAssistantStart, EvAssistantEnd, EvToolStart, EvToolEnd,
		EvPermission, EvAskUser, EvError, EvDone:
		return true
	default:
		return false
	}
}

// Helpers -------------------------------------------------------------------

func actionTypeForTool(tool string) string {
	switch tool {
	case "search":
		return core.ActionSearch
	case "read", "list_dir", "git_status", "git_diff", "git_log":
		return core.ActionRead
	case "edit_file", "write_file", "apply_patch":
		return core.ActionEdit
	case "run_tests":
		return core.ActionRunTest
	case "run_build":
		return core.ActionRunBuild
	case "run_command":
		return core.ActionRunProgram
	case "ask_user":
		return core.ActionAskUser
	case "verify":
		return core.ActionReview
	default:
		return strings.ToUpper(tool)
	}
}

func isFatalTool(name string) bool {
	switch name {
	case "edit_file", "write_file", "apply_patch", "run_tests", "run_build", "run_command", "verify":
		return true
	default:
		return false
	}
}

func truncateText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Usage returns accumulated token usage.
func (e *Engine) Usage() llm.Usage {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.usage
}

// IndexPath exposes the index path for CLI commands.
func (e *Engine) IndexPath() string {
	return filepath.Join(e.Root, ".astra", "index.json")
}

func (e *Engine) StateDir() string {
	return filepath.Join(e.Root, ".astra")
}

// RebuildIndex re-scans the repository.
func (e *Engine) RebuildIndex() error {
	return e.RebuildIndexWithProgress(nil)
}

// RebuildIndexWithProgress is RebuildIndex with an optional progress callback.
func (e *Engine) RebuildIndexWithProgress(progress func(done, total int)) error {
	ix := knowledge.NewIndex(e.Root)
	if err := ix.BuildWithProgress(progress); err != nil {
		return err
	}
	if err := ix.Save(); err != nil {
		return err
	}
	e.Index = ix
	return nil
}

// Compact reduces the conversation to a state-derived summary instead of
// accumulating chat history (State Compiler principle). PreCompact hooks can
// abort the compaction; PostCompact hooks observe the summary.
func (e *Engine) Compact() string {
	if denied, reason := e.runHooks(HookPreCompact, "", nil); denied {
		return "compact blocked by hook: " + reason
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.messages) <= 2 {
		return "nothing to compact"
	}
	var goalDesc string
	if g := e.Store.ActiveGoal(); g != nil {
		goalDesc = g.Description
	}
	state := e.CompilerOutput()
	summary := fmt.Sprintf("Continue the current task without repeating completed work.\n\nGOAL: %s\n\n%s",
		goalDesc, truncateText(state, 6000))
	e.messages = []llm.Message{
		{Role: llm.RoleUser, Content: "Task context (compacted):\n" + summary},
	}
	e.runHooks(HookPostCompact, "", map[string]any{"summary": truncateText(summary, 500)})
	return "conversation compacted; context rebuilt from knowledge state"
}
