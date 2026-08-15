package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kevenhu001-cyber/astra-harness/internal/core"
	"github.com/kevenhu001-cyber/astra-harness/internal/knowledge"
	"github.com/kevenhu001-cyber/astra-harness/internal/llm"
)

// EventType is the UI/observer event taxonomy.
type EventType string

const (
	EvStatus         EventType = "status"
	EvDelta          EventType = "delta"
	EvAssistantStart EventType = "assistant_start"
	EvAssistantEnd   EventType = "assistant_end"
	EvToolStart      EventType = "tool_start"
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
}

// NewEngine wires store, index, router and permissions for a workspace.
func NewEngine(root string, cfg *Config) (*Engine, error) {
	st, err := core.NewStore(root)
	if err != nil {
		return nil, err
	}
	g := &knowledge.Git{Root: root}
	ix := knowledge.NewIndex(root)
	if loaded, err := knowledge.LoadIndex(root); err == nil {
		ix = loaded
	} else {
		if err := ix.Build(); err != nil {
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
		Events:    make(chan Event, 256),
		askChans:  map[string]chan string{},
		permChans: map[string]chan PermissionDecision{},
	}
	e.Perm = NewPermissionManager(root, cfg.PermissionMode, e.promptPermission)
	e.initProject()
	return e, nil
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

func (e *Engine) callModel(ctx context.Context) (string, []llm.ToolCall, string, error) {
	e.emit(EvAssistantStart, map[string]any{"model": e.Model})
	req := llm.Request{
		Model:       e.Model,
		System:      e.buildSystemPrompt(),
		Messages:    e.messages,
		Tools:       ToolDefs(),
		MaxTokens:   4096,
		Temperature: 0.2,
	}
	stream, err := e.Provider.Stream(ctx, &req)
	if err != nil {
		return "", nil, "", err
	}
	var content strings.Builder
	acc := map[int]*llm.ToolCall{}
	var finish string
	for ev := range stream {
		if ev.Error != nil {
			return "", nil, "", ev.Error
		}
		if ev.Content != "" {
			content.WriteString(ev.Content)
			e.emit(EvDelta, ev.Content)
		}
		for _, d := range ev.ToolCalls {
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

func (e *Engine) buildSystemPrompt() string {
	var b strings.Builder
	b.WriteString(`You are Astra, an uncertainty-driven software engineering runtime.

CORE PRINCIPLES
1. The agent is disposable; durable intelligence lives in the state, evidence and unknown records.
2. Claims require evidence. Never assert that something works without a test/build/runtime result.
3. Unknowns drive planning. Prefer actions that resolve the highest-priority unknown.
4. Actions compete on expected value: goal progress + information gain - cost - risk.
5. Deterministic work (searching, reading, diffing, running tests) must be done with tools, not guessed.
6. Never declare a task done by saying "Done". Finish with a concise summary: what changed, what was verified, what remains unknown.

TOOL DISCIPLINE
- Use search/read before editing; use git_status/git_diff to understand changes.
- Use edit_file with precise, unique old_string context. Use run_tests/run_build/verify to collect evidence.
- If a test fails, read the failure, investigate, fix, and re-run.
- Use ask_user only when a decision genuinely requires the human operator.

`)
	b.WriteString("=== COMPILED KNOWLEDGE STATE (query result, not chat history) ===\n")
	b.WriteString(e.CompilerOutput())
	b.WriteString("\n=== TOOLS ===\n")
	for _, t := range ToolDefs() {
		fmt.Fprintf(&b, "- %s: %s\n", t.Name, t.Description)
	}
	return b.String()
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

// ProviderID returns the active provider id (safe for display).
func (e *Engine) ProviderID() string {
	if e.Provider != nil {
		return e.Provider.ID()
	}
	return ""
}

// SwitchModel changes provider/model at runtime.
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
	return nil
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
	case "edit_file", "write_file":
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
	select {
	case e.Events <- Event{Type: t, Data: data, Time: time.Now()}:
	default:
	}
}

// Helpers -------------------------------------------------------------------

func actionTypeForTool(tool string) string {
	switch tool {
	case "search":
		return core.ActionSearch
	case "read", "list_dir", "git_status", "git_diff", "git_log":
		return core.ActionRead
	case "edit_file", "write_file":
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
	case "edit_file", "write_file", "run_tests", "run_build", "run_command", "verify":
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
	ix := knowledge.NewIndex(e.Root)
	if err := ix.Build(); err != nil {
		return err
	}
	if err := ix.Save(); err != nil {
		return err
	}
	e.Index = ix
	return nil
}

// Compact reduces the conversation to a state-derived summary instead of
// accumulating chat history (State Compiler principle).
func (e *Engine) Compact() string {
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
	return "conversation compacted; context rebuilt from knowledge state"
}
