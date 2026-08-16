package tui

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// readFileLimit reads at most `limit` bytes from a file.
func readFileLimit(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// truncate shortens a string to n runes and appends an ellipsis when truncation
// occurred. The rune-safe variant avoids slicing mid-codepoint on UTF-8.
func truncate(s string, n int) string {
	if n < 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// containsFold is a case-insensitive substring check used by the cost helper.
func containsFold(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// fuzzyMatch returns true if every rune of query (lower-cased) appears in
// target in order (case-insensitive). An empty query matches everything.
func fuzzyMatch(query, target string) bool {
	if query == "" {
		return true
	}
	t := strings.ToLower(target)
	q := strings.ToLower(query)
	qi := 0
	for ti := 0; ti < len(t) && qi < len(q); ti++ {
		if t[ti] == q[qi] {
			qi++
		}
	}
	return qi == len(q)
}

// padRight right-pads s with spaces up to width w using a single allocation.
func padRight(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

// padLeft zero-pads n to width w.
func padLeft(n, w int) string { return fmt.Sprintf("%*d", w, n) }

