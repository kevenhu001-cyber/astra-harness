package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Codex-style conversation rendering helpers.
//
// These mirror the visual language of codex-rs/tui (exec_cell/render.rs,
// history_cell/separators.rs and history_cell/messages.rs):
//
//	• Running cargo build          ← live tool cell
//	  └ Compiling foo...
//
//	• Ran go test ./...
//	  │ ok  github.com/x 3.4s
//	  └ ok  github.com/y 2.1s
//
//	• Called search({"q":"x"})
//	  └ 3 results
//
//	• You ran ls                   ← user `!` shell cell
//	  └ file1
//	    file2
//
//	$ ls                            ← transcript overlay / export form
//	file1
//	✗ (1) • 0ms
//
//	──── Worked for 42s ────        ← turn separator

const (
	codexBullet   = "•"
	codexBranch   = "│"
	codexCorner   = "└"
	codexPrompt   = "$"
	codexOK       = "✓"
	codexErr      = "✗"
	codexSepChar  = "─"
	codexHint     = "ctrl + t to view transcript"
	toolMaxLines  = 5
	shellMaxLines = 50
)

// isShellTool reports whether a tool renders in Codex's exec/transcript form
// (`$ cmd` + output + status row) rather than a generic called-cell.
func isShellTool(name string) bool {
	switch name {
	case "run_command", "run_tests", "run_build", "bash":
		return true
	}
	return false
}

// codexToolTitle returns the Codex-style verb + label for a tool call.
// Exec-style tools get "Ran", local code tools get descriptive verbs the way
// Codex's exploring/exec cells do, and everything else falls back to "Called".
func codexToolTitle(name, args string) (verb, label string) {
	label = codexToolLabel(name, args)
	switch name {
	case "run_command", "run_tests", "run_build":
		return "Ran", label
	case "bash":
		return "You ran", label
	case "search":
		return "Searched for", label
	case "read":
		return "Read", label
	case "list_dir":
		return "Listed", label
	case "edit_file":
		return "Edited", label
	case "write_file":
		return "Wrote", label
	case "apply_patch":
		return "Applied", label
	case "git_status", "git_diff", "git_log":
		return "Ran", label
	case "verify":
		return "Ran", label
	case "ask_user":
		return "Asked you", label
	default:
		return "Called", label
	}
}

// codexToolLabel renders the inline label of a tool call:
//
//	run_command → the command itself (Codex exec style)
//	search      → `search({"q":"x"})` (name + compact args)
//	mcp__git__status → `mcp__git__status({})`
func codexToolLabel(name, args string) string {
	switch name {
	case "run_command", "run_tests", "run_build":
		var cmd string
		if args != "" {
			var m map[string]any
			if err := json.Unmarshal([]byte(args), &m); err == nil {
				if c, ok := m["command"].(string); ok && c != "" {
					cmd = c
				}
			}
		}
		if cmd == "" {
			cmd = name
		}
		return cmd
	case "bash":
		return args
	case "search":
		return argStringFromJSON(args, "query", "codebase")
	case "read":
		return argStringFromJSON(args, "path", "file")
	case "list_dir":
		return argStringFromJSON(args, "path", "dir")
	case "edit_file", "write_file":
		return argStringFromJSON(args, "path", name)
	case "apply_patch":
		return "patch"
	case "git_status", "git_diff", "git_log":
		return strings.ReplaceAll(name, "_", " ")
	case "verify":
		return "verify"
	case "ask_user":
		return "question"
	default:
		compact := compactJSON(args)
		if len(compact) > 120 {
			compact = truncate(compact, 117) + "…"
		}
		if compact == "" || compact == "{}" {
			return name
		}
		return name + compact
	}
}

// argStringFromJSON reads a string field from a JSON tool-arguments payload.
func argStringFromJSON(args, key, def string) string {
	if args == "" {
		return def
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return def
	}
	if s, ok := m[key].(string); ok && s != "" {
		return s
	}
	return def
}

// compactJSON re-serializes a JSON string without whitespace. Returns "" on
// parse failure or empty input.
func compactJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	out, err := json.Marshal(v)
	if err != nil {
		return s
	}
	return string(out)
}

// codexExecHeader renders the first line of a tool cell:
//
//   - Running cargo build          (live, animated bullet)
//   - Ran go test ./...            (success, green bullet)
//   - Ran cargo build              (failure, red bullet)
//   - Called search({"q":"x"})
func codexExecHeader(live, ok bool, name, args string) string {
	verb, label := codexToolTitle(name, args)
	if live {
		verb = "Running"
	}
	var bullet string
	switch {
	case live:
		bullet = activityBullet(time.Now())
	case ok:
		bullet = styleExitOK.Render(codexBullet)
	default:
		bullet = styleExitErr.Render(codexBullet)
	}
	return bullet + " " +
		styleVerb.Render(verb) + " " +
		styleCmdName.Render(label)
}

// codexUserShellHeader renders the user `!` shell cell header, with a
// success/failure-colored bullet like committed exec cells:
//
//   - You ran ls
func codexUserShellHeader(cmd string, ok bool) string {
	bullet := styleBullet
	if ok {
		bullet = styleExitOK
	} else {
		bullet = styleExitErr
	}
	return bullet.Render(codexBullet) + " " +
		styleVerb.Render("You ran") + " " +
		styleCmdName.Render(cmd)
}

// codexCommandContinuation renders wrapped/multiline command lines with the
// Codex "│" gutter (at most two rows, then an ellipsis):
//
//	│ this_is_a_very_long_si
//	│ ngle_token_that_will_w
//	│ … +2 lines
func codexCommandContinuation(command string, width int) string {
	parts := strings.Split(command, "\n")
	var lines []string
	for i, part := range parts {
		if i == 0 {
			wrapped := wrapWords(part, max(8, width-4))
			if len(wrapped) > 1 {
				lines = append(lines, wrapped[1:]...)
			}
			continue
		}
		lines = append(lines, wrapWords(part, max(8, width-4))...)
	}
	if len(lines) == 0 {
		return ""
	}
	omitted := 0
	if len(lines) > 2 {
		omitted = len(lines) - 2
		lines = lines[:2]
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("  ")
		b.WriteString(stylePrefix.Render(codexBranch + " "))
		b.WriteString(styleOutput.Render(l))
		b.WriteString("\n")
	}
	if omitted > 0 {
		b.WriteString("  ")
		b.WriteString(stylePrefix.Render(codexBranch + " "))
		b.WriteString(styleDim.Render("… +" + itoa(omitted) + " lines"))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// codexOutputBlock renders command/tool output the way Codex exec cells do:
// the first line carries a "└" prefix and every following line a 4-column
// gutter, with head/tail truncation and the transcript hint:
//
//	└ error: first line on
//	  stderr
//	  error: second line on
//	  stderr
func codexOutputBlock(output string, maxLines, width int) string {
	output = strings.TrimRight(output, "\n")
	if maxLines <= 0 {
		maxLines = toolMaxLines
	}
	if output == "" {
		return "  " + stylePrefix.Render(codexCorner+" ") + styleOutput.Render("(no output)")
	}
	var all []string
	for _, line := range strings.Split(output, "\n") {
		wrapped := wrapWords(line, max(8, width-4))
		if len(wrapped) == 0 {
			all = append(all, "")
		} else {
			all = append(all, wrapped...)
		}
	}
	omitted := 0
	var shown []string
	if len(all) > maxLines {
		head := max(1, maxLines-3)
		tail := 2
		if head+tail > len(all) {
			head = len(all) - tail
		}
		shown = append(shown, all[:head]...)
		omitted = len(all) - head - tail
		shown = append(shown, all[len(all)-tail:]...)
	} else {
		shown = all
	}
	var b strings.Builder
	for i, l := range shown {
		if i == 0 {
			b.WriteString("  ")
			b.WriteString(stylePrefix.Render(codexCorner + " "))
		} else {
			b.WriteString("    ")
		}
		b.WriteString(styleOutput.Render(l))
		b.WriteString("\n")
	}
	if omitted > 0 {
		b.WriteString("    ")
		b.WriteString(styleDim.Render(fmt.Sprintf("… +%d lines (%s)", omitted, codexHint)))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// codexExecCell renders a committed or live shell/tool call as a Codex exec
// history cell:
//
//   - Running cargo build
//     │ extra command line
//     └ Compiling foo...
func codexExecCell(width int, live, ok bool, name, args, output string, maxOutputLines int) string {
	header := codexExecHeader(live, ok, name, args)
	cmd := codexToolLabel(name, args)
	cont := codexCommandContinuation(cmd, width)
	body := codexOutputBlock(output, maxOutputLines, width)
	if cont == "" {
		return header + "\n" + body
	}
	return header + "\n" + cont + "\n" + body
}

// codexUserShellCell renders the user `!` shell result:
//
//   - You ran ls
//     └ file1
//     file2
func codexUserShellCell(cmd, output string, ok bool, width int) string {
	header := codexUserShellHeader(cmd, ok)
	body := codexOutputBlock(output, shellMaxLines, width)
	return header + "\n" + body
}

// codexTranscriptCell renders the transcript-overlay / export form of a
// shell command:
//
//	$ go test ./...
//	ok  github.com/x 3.4s
//	✓ • 3.2s
func codexTranscriptCell(command, output string, success bool, exitCode string, dur time.Duration) string {
	var b strings.Builder
	b.WriteString(stylePrompt.Render(codexPrompt) + " " + styleCmdName.Render(command))
	output = strings.TrimRight(output, "\n")
	if output != "" {
		b.WriteString("\n")
		b.WriteString(styleOutput.Render(output))
	}
	b.WriteString("\n")
	b.WriteString(codexExitStatus(success, exitCode, dur))
	return b.String()
}

// codexExitStatus renders the final status row of a transcript cell:
//
//	✓ • 1.2s       (success)
//	✗ (1) • 0ms    (failure with exit code)
func codexExitStatus(success bool, exitCode string, dur time.Duration) string {
	if success {
		return styleExitOK.Render(codexOK) + styleDim.Render(" • "+fmtDurCompact(dur))
	}
	code := strings.TrimSpace(exitCode)
	if code == "" {
		code = "1"
	}
	return styleExitErr.Render(codexErr+" ("+code+")") + styleDim.Render(" • "+fmtDurCompact(dur))
}

// codexSeparator renders a Codex FinalMessageSeparator-style divider:
//
//	──── Worked for 42s ────
//	────────────────────────
//
// label may be empty for a plain rule. The separator fills the given width.
func codexSeparator(label string, width int) string {
	if width < 12 {
		width = 12
	}
	if label == "" {
		return styleSep.Render(strings.Repeat(codexSepChar, width))
	}
	full := codexSepChar + " " + label + " " + codexSepChar
	labelW := lipgloss.Width(full)
	if labelW >= width {
		return styleSep.Render(full)
	}
	return styleSep.Render(full + strings.Repeat(codexSepChar, width-labelW))
}

// fmtDurCompact renders a duration Codex-style: "0ms", "1.2s", "42s",
// "1m 23s", "1h 2m".
func fmtDurCompact(d time.Duration) string {
	if d <= 0 {
		return "0ms"
	}
	ms := d.Milliseconds()
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	secs := d.Round(time.Second)
	if secs < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(secs / time.Minute)
	s := int(secs % time.Minute)
	if m >= 60 {
		h := m / 60
		m = m % 60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if s == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dm %ds", m, s)
}

// wrapWords wraps a single logical line to at most width display columns,
// breaking at whitespace first and then on wide tokens, mirroring Codex's
// no-hyphenation adaptive wrapping for exec cells. Internal runs of spaces
// and tabs are preserved so command/test output keeps its alignment.
func wrapWords(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	for _, raw := range strings.Split(s, "\n") {
		if raw == "" {
			out = append(out, "")
			continue
		}
		var cur strings.Builder
		curW := 0
		flush := func() {
			if s := strings.TrimRight(cur.String(), " \t"); s != "" {
				out = append(out, s)
			}
			cur.Reset()
			curW = 0
		}
		i := 0
		for i < len(raw) {
			if raw[i] == ' ' || raw[i] == '\t' {
				j := i
				for j < len(raw) && (raw[j] == ' ' || raw[j] == '\t') {
					j++
				}
				ws := raw[i:j]
				wsW := lipgloss.Width(ws)
				if curW > 0 && curW+wsW > width {
					// Whitespace is a break opportunity: start a fresh row
					// and drop the run of spaces at the fold.
					flush()
				} else if curW == 0 {
					if wsW <= width {
						cur.WriteString(ws)
						curW = wsW
					}
				} else {
					cur.WriteString(ws)
					curW += wsW
				}
				i = j
				continue
			}
			j := i
			for j < len(raw) && raw[j] != ' ' && raw[j] != '\t' {
				j++
			}
			w := raw[i:j]
			wl := lipgloss.Width(w)
			if wl > width {
				if curW > 0 {
					flush()
				}
				runes := []rune(w)
				for len(runes) > 0 {
					take := 0
					tw := 0
					for take < len(runes) {
						rw := lipgloss.Width(string(runes[take]))
						if tw+rw > width {
							break
						}
						tw += rw
						take++
					}
					if take == 0 {
						take = 1
					}
					out = append(out, string(runes[:take]))
					runes = runes[take:]
				}
			} else {
				if curW > 0 && curW+wl > width {
					flush()
				}
				cur.WriteString(w)
				curW += wl
			}
			i = j
		}
		flush()
	}
	return out
}
