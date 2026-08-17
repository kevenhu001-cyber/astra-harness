package tui

import (
	"strconv"
	"strings"
)

// renderDiff formats text as a unified diff with Astra styling.
//
// If the input is already in diff form (--- / +++ headers with +/- lines) it
// passes through; otherwise it is treated as a generic two-column diff.
func renderDiff(body, filename string) string {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return ""
	}
	lines := strings.Split(body, "\n")
	// Detect diff-like input.
	hasDiff := false
	for _, l := range lines {
		if strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---") || strings.HasPrefix(l, "@@") ||
			strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") {
			hasDiff = true
			break
		}
	}
	if hasDiff {
		return renderUnifiedDiff(lines)
	}
	return renderPlainBody(body)
}

func renderUnifiedDiff(lines []string) string {
	var b strings.Builder
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "@@"):
			b.WriteString(styleDiffHunk.Render(line))
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			b.WriteString(styleDiffFileHdr.Render(line))
		case strings.HasPrefix(line, "+"):
			b.WriteString(styleDiffAdd.Render(line))
		case strings.HasPrefix(line, "-"):
			b.WriteString(styleDiffDel.Render(line))
		default:
			b.WriteString(styleDiffCtxLine.Render(line))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func renderPlainBody(body string) string {
	var b strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "+") && len(line) > 0 {
			b.WriteString(styleDiffAdd.Render(line))
		} else if strings.HasPrefix(line, "-") && len(line) > 0 {
			b.WriteString(styleDiffDel.Render(line))
		} else {
			b.WriteString(styleDiffCtxLine.Render(line))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// detectDiff attempts to extract a single-file unified diff from a tool output.
func detectDiff(output string) (filename string, diff string, ok bool) {
	idx := strings.Index(output, "--- ")
	if idx < 0 {
		return "", "", false
	}
	body := output[idx:]
	nl := strings.Index(body, "\n")
	if nl < 0 {
		return "", "", false
	}
	header := strings.TrimPrefix(body[:nl], "--- ")
	end := strings.Index(body, "\n+++ ")
	if end < 0 {
		return header, body, true
	}
	rest := body[end+1:]
	nl = strings.Index(rest, "\n")
	if nl < 0 {
		return header, rest, true
	}
	return header, rest[:nl] + "\n" + body[idx:idx+end], true
}

// renderFilePreview renders a code snippet with chroma + line numbers.
func renderFilePreview(src, filename string) string {
	return renderNumberedCode(src, filename, 1)
}

// renderReadPreview turns the line-numbered output of the read tool back into
// a syntax-highlighted code block. maxLines limits only the rendered card;
// passing zero keeps every line returned by the engine in the scrollable
// viewport.
func renderReadPreview(output, filename string, maxLines int) string {
	output = strings.TrimRight(output, "\n")
	if output == "" {
		return ""
	}
	var source strings.Builder
	startLine := 1
	shown := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "...[truncated]") {
			break
		}
		sep := strings.Index(line, " │ ")
		code := line
		if sep >= 0 {
			if n, err := strconv.Atoi(strings.TrimSpace(line[:sep])); err == nil {
				if shown == 0 {
					startLine = n
				}
			}
			code = line[sep+len(" │ "):]
		}
		if maxLines > 0 && shown >= maxLines {
			break
		}
		if shown > 0 {
			source.WriteByte('\n')
		}
		source.WriteString(code)
		shown++
	}
	if shown == 0 {
		return ""
	}
	return renderNumberedCode(source.String(), filename, startLine)
}

func renderNumberedCode(src, filename string, startLine int) string {
	lang := detectLang(filename, "")
	highlighted := highlightCode(src, lang, filename)
	lines := strings.Split(strings.TrimRight(highlighted, "\n"), "\n")
	var b strings.Builder
	for i, l := range lines {
		lineNo := startLine + i
		ln := styleCodeLineNum.Render(padLeft(lineNo, 4) + " │ ")
		b.WriteString(ln)
		b.WriteString(l)
		b.WriteString("\n")
	}
	return styleCodeBlock.Render(b.String())
}
