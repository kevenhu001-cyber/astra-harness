package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Patch markers, mirroring codex-rs/apply-patch/src/parser.rs. The format lets
// the model describe edits as context-matched diff hunks instead of exact
// old_string/new_string pairs (and across several files in one call).
const (
	patchBeginMarker  = "*** Begin Patch"
	patchEndMarker    = "*** End Patch"
	patchAddMarker    = "*** Add File: "
	patchDeleteMarker = "*** Delete File: "
	patchUpdateMarker = "*** Update File: "
	patchMoveMarker   = "*** Move to: "
	patchEOFMaker     = "*** End of File"
)

type patchChunk struct {
	context   string   // text after @@ (used to anchor the search)
	oldLines  []string // lines to find (context " " + removed "-")
	newLines  []string // lines to write (context " " + added "+")
	endOfFile bool     // oldLines must match at the very end of the file
}

type patchHunk struct {
	kind    string // "add" | "delete" | "update"
	path    string
	moveTo  string
	content []string     // add-file lines
	chunks  []patchChunk // update-file hunks
}

// parsePatch parses the Codex apply_patch format into ordered hunks.
func parsePatch(patch string) ([]patchHunk, error) {
	lines := strings.Split(strings.TrimSpace(patch), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != patchBeginMarker {
		return nil, fmt.Errorf("patch must start with %q", patchBeginMarker)
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	if last != patchEndMarker {
		return nil, fmt.Errorf("patch must end with %q", patchEndMarker)
	}
	body := lines[1 : len(lines)-1]

	var hunks []patchHunk
	var cur *patchHunk
	var chunk *patchChunk

	flushChunk := func() {
		if chunk != nil && cur != nil && len(chunk.oldLines)+len(chunk.newLines) > 0 {
			cur.chunks = append(cur.chunks, *chunk)
		}
		chunk = nil
	}
	flushHunk := func() {
		if cur != nil {
			flushChunk()
			hunks = append(hunks, *cur)
			cur = nil
		}
	}

	for i, raw := range body {
		line := raw
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, patchAddMarker):
			flushHunk()
			h := &patchHunk{kind: "add", path: strings.TrimSpace(strings.TrimPrefix(trimmed, patchAddMarker))}
			if h.path == "" {
				return nil, fmt.Errorf("line %d: add file requires a path", i+2)
			}
			cur = h
		case strings.HasPrefix(trimmed, patchDeleteMarker):
			flushHunk()
			p := strings.TrimSpace(strings.TrimPrefix(trimmed, patchDeleteMarker))
			if p == "" {
				return nil, fmt.Errorf("line %d: delete file requires a path", i+2)
			}
			hunks = append(hunks, patchHunk{kind: "delete", path: p})
		case strings.HasPrefix(trimmed, patchUpdateMarker):
			flushHunk()
			h := &patchHunk{kind: "update", path: strings.TrimSpace(strings.TrimPrefix(trimmed, patchUpdateMarker))}
			if h.path == "" {
				return nil, fmt.Errorf("line %d: update file requires a path", i+2)
			}
			cur = h
		case strings.HasPrefix(trimmed, patchMoveMarker):
			if cur == nil {
				return nil, fmt.Errorf("line %d: %q outside an update hunk", i+2, patchMoveMarker)
			}
			cur.moveTo = strings.TrimSpace(strings.TrimPrefix(trimmed, patchMoveMarker))
		case trimmed == patchEOFMaker:
			if chunk == nil {
				return nil, fmt.Errorf("line %d: %q without a preceding @@", i+2, patchEOFMaker)
			}
			chunk.endOfFile = true
		case strings.HasPrefix(trimmed, "@@"):
			flushChunk()
			chunk = &patchChunk{context: strings.TrimSpace(strings.TrimPrefix(trimmed, "@@"))}
		default:
			if cur == nil {
				return nil, fmt.Errorf("line %d: content outside a file hunk: %q", i+2, line)
			}
			switch cur.kind {
			case "add":
				if strings.HasPrefix(line, "+") {
					cur.content = append(cur.content, strings.TrimPrefix(line, "+"))
					continue
				}
				return nil, fmt.Errorf("line %d: add-file lines must start with '+'", i+2)
			case "update":
				if chunk == nil {
					return nil, fmt.Errorf("line %d: update lines must follow an @@ marker", i+2)
				}
				switch {
				case strings.HasPrefix(line, "-"):
					chunk.oldLines = append(chunk.oldLines, strings.TrimPrefix(line, "-"))
				case strings.HasPrefix(line, "+"):
					chunk.newLines = append(chunk.newLines, strings.TrimPrefix(line, "+"))
				case strings.HasPrefix(line, " "):
					content := strings.TrimPrefix(line, " ")
					chunk.oldLines = append(chunk.oldLines, content)
					chunk.newLines = append(chunk.newLines, content)
				default:
					return nil, fmt.Errorf("line %d: update lines must start with '-', '+', ' ' or '@@': %q", i+2, line)
				}
			}
		}
	}
	flushHunk()
	if len(hunks) == 0 {
		return nil, fmt.Errorf("patch contains no file hunks")
	}
	return hunks, nil
}

// applyHunk executes one parsed hunk against the workspace, recording evidence
// and returning a human-readable diff for the tool card.
func (e *Engine) applyHunk(h patchHunk) ToolResult {
	switch h.kind {
	case "add":
		return e.patchAdd(h)
	case "delete":
		return e.patchDelete(h)
	default:
		return e.patchUpdate(h)
	}
}

func (e *Engine) patchAdd(h patchHunk) ToolResult {
	full, err := e.Perm.SafePath(h.path)
	if err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	if _, err := os.Stat(full); err == nil {
		return ToolResult{Success: false, Output: fmt.Sprintf("cannot add %s: file already exists", h.path)}
	}
	content := strings.Join(h.content, "\n")
	if len(h.content) > 0 && h.content[len(h.content)-1] != "" {
		content += "\n"
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	e.Index.Touch(h.path)
	e.recordFileChange(h.path, simpleDiff("", content, h.path))
	return ToolResult{Success: true, Output: fmt.Sprintf("Added %s (%d lines)", h.path, len(h.content))}
}

func (e *Engine) patchDelete(h patchHunk) ToolResult {
	full, err := e.Perm.SafePath(h.path)
	if err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	before, err := os.ReadFile(full)
	if err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	if err := os.Remove(full); err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	e.Index.Touch(h.path)
	e.recordFileChange(h.path, simpleDiff(string(before), "", h.path))
	return ToolResult{Success: true, Output: fmt.Sprintf("Deleted %s", h.path)}
}

func (e *Engine) patchUpdate(h patchHunk) ToolResult {
	full, err := e.Perm.SafePath(h.path)
	if err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	before, err := os.ReadFile(full)
	if err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	content := string(before)
	normalized := strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	crlf := strings.Contains(content, "\r\n")

	// Split into lines, dropping the trailing empty element from a final
	// newline so line counts match standard diff semantics (Codex file_update).
	lines := strings.Split(normalized, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	var replacements []struct {
		start int
		old   []string
		new   []string
	}
	cursor := 0
	for ci, chunk := range h.chunks {
		pos := cursor
		// Narrow the search with the @@ context anchor when provided.
		if chunk.context != "" {
			anchor := -1
			for i := cursor; i < len(lines); i++ {
				if linesMatch([]string{lines[i]}, []string{chunk.context}) {
					anchor = i
					break
				}
			}
			if anchor < 0 {
				return ToolResult{Success: false, Output: fmt.Sprintf("%s chunk %d: context line %q not found", h.path, ci+1, chunk.context)}
			}
			pos = anchor + 1
		}
		if len(chunk.oldLines) == 0 {
			// Pure insertion without an anchor appends at end of file (Codex
			// semantics); with an anchor it inserts right after it.
			if chunk.context == "" {
				pos = len(lines)
			}
			replacements = append(replacements, struct {
				start int
				old   []string
				new   []string
			}{pos, nil, chunk.newLines})
			cursor = pos + len(chunk.newLines)
			continue
		}
		oldLines, newLines := chunk.oldLines, chunk.newLines
		match := findLines(lines, pos, oldLines, chunk.endOfFile)
		if match < 0 && len(oldLines) > 0 && oldLines[len(oldLines)-1] == "" {
			// Retry without the trailing empty line that represents a final
			// newline (Codex file_update behaviour).
			oldLines = oldLines[:len(oldLines)-1]
			if len(newLines) > 0 && newLines[len(newLines)-1] == "" {
				newLines = newLines[:len(newLines)-1]
			}
			match = findLines(lines, pos, oldLines, chunk.endOfFile)
		}
		if match < 0 {
			return ToolResult{Success: false, Output: fmt.Sprintf(
				"%s chunk %d: hunk did not match. Provide exact context lines around the change.", h.path, ci+1)}
		}
		replacements = append(replacements, struct {
			start int
			old   []string
			new   []string
		}{match, oldLines, newLines})
		cursor = match + len(oldLines)
	}

	// Apply replacements from last to first so earlier indices stay valid.
	for r := len(replacements) - 1; r >= 0; r-- {
		rep := replacements[r]
		lines = append(lines[:rep.start], append(rep.new, lines[rep.start+len(rep.old):]...)...)
	}

	// Rename (*** Move to) after content changes.
	outPath := full
	if h.moveTo != "" {
		dst, err := e.Perm.SafePath(h.moveTo)
		if err != nil {
			return ToolResult{Success: false, Output: err.Error()}
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return ToolResult{Success: false, Output: err.Error()}
		}
		outPath = dst
	}

	out := strings.Join(lines, "\n")
	if len(lines) > 0 || normalized != "" {
		out += "\n"
	}
	if crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	if err := os.WriteFile(outPath, []byte(out), 0o644); err != nil {
		return ToolResult{Success: false, Output: err.Error()}
	}
	if outPath != full {
		_ = os.Remove(full)
		e.Index.Touch(h.moveTo)
	}
	e.Index.Touch(h.path)
	e.recordFileChange(relPath(e.Root, outPath), simpleDiff(content, out, h.path))
	label := h.path
	if h.moveTo != "" {
		label = h.path + " → " + h.moveTo
	}
	return ToolResult{Success: true, Output: fmt.Sprintf("Updated %s (%d chunk(s))", label, len(h.chunks))}
}

// findLines locates the line sequence old in lines[start:], mirroring Codex's
// seek_sequence: exact match, then trailing-whitespace-insensitive, then
// fully-trimmed. When endOfFile is set the match must occupy the final lines.
func findLines(lines []string, start int, old []string, endOfFile bool) int {
	if len(old) == 0 || len(old) > len(lines) {
		return -1
	}
	search := start
	if endOfFile && len(lines) >= len(old) {
		search = len(lines) - len(old)
	}
	if search < 0 {
		search = 0
	}
	limit := len(lines) - len(old)
	for i := search; i <= limit; i++ {
		if linesMatch(lines[i:i+len(old)], old) {
			return i
		}
	}
	return -1
}

// linesMatch compares with decreasing strictness like Codex's seek_sequence.
func linesMatch(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	allExact := true
	allRstrip := true
	allTrim := true
	for i := range a {
		if a[i] != b[i] {
			allExact = false
		}
		if strings.TrimRight(a[i], " \t") != strings.TrimRight(b[i], " \t") {
			allRstrip = false
		}
		if strings.TrimSpace(a[i]) != strings.TrimSpace(b[i]) {
			allTrim = false
		}
	}
	return allExact || allRstrip || allTrim
}
