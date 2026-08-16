package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kevenhu001-cyber/astra-harness/internal/llm"
)

// PushRecentModel records a recently-used model identifier (provider|model)
// in the project config so it surfaces in the model picker.
func (e *Engine) PushRecentModel(id string) {
	recents := e.Config.RecentModels
	for _, r := range recents {
		if r == id {
			return
		}
	}
	recents = append(recents, id)
	if len(recents) > 8 {
		recents = recents[len(recents)-8:]
	}
	e.Config.RecentModels = recents
}

// RenameSession renames the active session by removing the old file and
// rewriting the session with the new ID. Returns an error if the new ID is
// empty, contains path separators, or would escape the sessions directory.
func (e *Engine) RenameSession(newID string) error {
	newID = strings.TrimSpace(newID)
	if newID == "" {
		return errors.New("empty session id")
	}
	if strings.ContainsAny(newID, "/\\") || strings.Contains(newID, "..") {
		return errors.New("session id must not contain path separators or '..'")
	}
	e.mu.Lock()
	if e.session == nil {
		e.mu.Unlock()
		return errors.New("no active session")
	}
	oldID := e.session.ID
	old := *e.session
	e.mu.Unlock()

	// Validate that both paths land inside the same session directory before
	// touching anything.
	newPath := e.Store.SessionPath(newID)
	oldPath := e.Store.SessionPath(oldID)
	dir := filepath.Dir(newPath)
	if !strings.HasPrefix(oldPath, dir+string(filepath.Separator)) || !strings.HasPrefix(newPath, dir+string(filepath.Separator)) {
		return errors.New("session path escapes sessions dir")
	}
	_ = os.Remove(oldPath)
	old.ID = newID
	if err := e.Store.SaveSession(&old); err != nil {
		return err
	}
	e.mu.Lock()
	e.session = &old
	e.mu.Unlock()
	return nil
}

// SetReasoningEffort changes the reasoning effort knob (and persists it).
func (e *Engine) SetReasoningEffort(level string) error {
	switch level {
	case "low", "medium", "high", "xhigh":
	default:
		return errors.New("reasoning effort must be low|medium|high|xhigh")
	}
	e.Config.ReasoningEffort = level
	return SaveConfig(e.Root, e.Config)
}

// ReasoningEffort returns the configured reasoning effort knob.
func (e *Engine) ReasoningEffort() string {
	if e.Config.ReasoningEffort == "" {
		return "medium"
	}
	return e.Config.ReasoningEffort
}

// RecentModels returns the recently-used model identifiers.
func (e *Engine) RecentModels() []string { return e.Config.RecentModels }

// RelPath returns a forward-slashed, project-relative path.
func RelPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// GitCommit stages all changes and commits with the given message.
func (e *Engine) GitCommit(message string) (string, error) {
	return e.Git.Commit(message)
}

// GitBranch switches to (or creates) the given branch.
func (e *Engine) GitBranch(name string) error {
	_, err := e.Git.SwitchBranch(name)
	return err
}

// NewSession resets the conversation memory and starts a fresh session id.
func (e *Engine) NewSession() error {
	e.mu.Lock()
	e.session = nil
	e.messages = nil
	e.usage = llm.Usage{}
	e.lastEdits = nil
	e.mu.Unlock()
	// Touch SessionID() to allocate and persist a new session.
	_ = e.SessionID()
	e.saveSession()
	return nil
}

// UndoLastTurn removes the most recent assistant turn and its tool results.
func (e *Engine) UndoLastTurn() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	// Walk backwards, dropping messages until we've discarded the latest
	// assistant turn along with its tool calls.
	i := len(e.messages) - 1
	dropped := 0
	droppedAssistant := false
	for i >= 0 {
		msg := e.messages[i]
		if msg.Role == llm.RoleAssistant {
			dropped++
			i--
			droppedAssistant = true
			break
		}
		if msg.Role == llm.RoleTool {
			dropped++
			i--
			continue
		}
		if msg.Role == llm.RoleUser {
			// don't drop the original user prompt; stop here
			break
		}
		i--
	}
	if dropped > 0 {
		e.messages = e.messages[:max(0, i+1)]
	}
	if !droppedAssistant {
		return "nothing to undo"
	}
	if e.session != nil {
		// Mirror message-drop on the session transcript.
		keep := e.session.Messages[:max(0, len(e.session.Messages)-dropped)]
		e.session.Messages = keep
		_ = e.Store.SaveSession(e.session)
	}
	return "undid last assistant turn (dropped " + strconv.Itoa(dropped) + " message(s))"
}
