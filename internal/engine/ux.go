package engine

import (
	"path/filepath"
	"strconv"

	"github.com/kevenhu001-cyber/astra-harness/internal/llm"
)

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
