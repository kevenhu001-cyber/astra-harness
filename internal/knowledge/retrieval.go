package knowledge

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Result is a ranked retrieval hit.
type Result struct {
	Path    string  `json:"path"`
	Line    int     `json:"line"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// Search uses ripgrep plus the symbol index to find relevant code.
func (ix *Index) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if limit <= 0 {
		limit = 20
	}
	args := []string{"--line-number", "--no-heading", "--max-count", "40", "-i", "--color", "never"}
	for _, g := range excludeGlobs() {
		args = append(args, "-g", g)
	}
	args = append(args, "-e", query, ".")
	cmd := exec.CommandContext(ctx, "rg", args...)
	cmd.Dir = ix.Root
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if _, ok := err.(*exec.ExitError); !ok {
			return nil, fmt.Errorf("rg failed: %w", err)
		}
		if out.Len() == 0 {
			return nil, nil
		}
	}
	terms := queryTerms(query)
	var results []Result
	for _, line := range strings.Split(out.String(), "\n") {
		if line == "" {
			continue
		}
		path, lineno, content, ok := parseRGLine(line)
		if !ok {
			continue
		}
		fullPath := filepath.Join(ix.Root, path)
		score := ix.score(fullPath, content, terms)
		results = append(results, Result{Path: filepath.ToSlash(path), Line: lineno, Content: content, Score: score})
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (ix *Index) score(path, content string, terms []string) float64 {
	score := 0.0
	rel, _ := filepath.Rel(ix.Root, path)
	rel = filepath.ToSlash(rel)
	lowerPath := strings.ToLower(rel)
	lowerContent := strings.ToLower(content)
	base := strings.ToLower(filepath.Base(rel))
	for _, t := range terms {
		if strings.Contains(lowerContent, t) {
			score += 2
		}
		if strings.Contains(base, t) {
			score += 4
		}
	}
	if entry, ok := ix.Files[path]; ok {
		for _, s := range entry.Symbols {
			for _, t := range terms {
				if strings.EqualFold(s.Name, t) {
					score += 8
				}
			}
		}
	}
	depth := float64(strings.Count(rel, "/"))
	score -= depth * 0.1
	return score
}

// FindSymbol locates a named symbol across indexed files.
func (ix *Index) FindSymbol(name string) []SymbolHit {
	var out []SymbolHit
	for path, entry := range ix.Files {
		for _, s := range entry.Symbols {
			if strings.EqualFold(s.Name, name) {
				out = append(out, SymbolHit{Path: path, Symbol: s})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Symbol.Name == out[j].Symbol.Name {
			return out[i].Path < out[j].Path
		}
		return out[i].Symbol.Name < out[j].Symbol.Name
	})
	return out
}

type SymbolHit struct {
	Path   string `json:"path"`
	Symbol Symbol `json:"symbol"`
}

// RelatedFiles returns nearby files plus tests for a set of changed files.
func (ix *Index) RelatedFiles(changed []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, c := range changed {
		add(c)
		dir := filepath.Dir(c)
		for p := range ix.Files {
			if filepath.Dir(p) == dir && !seen[p] {
				add(p)
			}
		}
	}
	// Append tests that reference a changed file name.
	for _, c := range changed {
		base := strings.TrimSuffix(filepath.Base(c), filepath.Ext(c))
		for _, t := range ix.TestFiles() {
			if strings.Contains(strings.ToLower(t), strings.ToLower(base)) {
				add(t)
			}
		}
	}
	return out
}

func queryTerms(q string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return r <= ' ' || r == ',' || r == '.' || r == ':' || r == ';' || r == '(' || r == ')' || r == '"' || r == '\''
	}) {
		f = strings.Trim(f, "`'\"[]{}")
		if len(f) >= 2 {
			out = append(out, f)
		}
	}
	return out
}

func parseRGLine(line string) (path string, lineNo int, content string, ok bool) {
	idx := strings.IndexByte(line, ':')
	if idx <= 0 {
		return "", 0, "", false
	}
	path = line[:idx]
	rest := line[idx+1:]
	idx2 := strings.IndexByte(rest, ':')
	if idx2 <= 0 {
		return "", 0, "", false
	}
	n := 0
	for _, r := range rest[:idx2] {
		if r < '0' || r > '9' {
			return "", 0, "", false
		}
		n = n*10 + int(r-'0')
	}
	content = rest[idx2+1:]
	if len(content) > 240 {
		content = content[:237] + "..."
	}
	return path, n, content, true
}

func excludeGlobs() []string {
	var out []string
	for d := range skipDirs {
		out = append(out, "!"+d+"/**", "!"+d)
	}
	return out
}
