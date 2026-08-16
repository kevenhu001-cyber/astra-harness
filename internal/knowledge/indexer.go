package knowledge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const indexVersion = 1

// FileEntry is one indexed file.
type FileEntry struct {
	Path      string    `json:"path"`
	Symbols   []Symbol  `json:"symbols,omitempty"`
	Lines     int       `json:"lines"`
	Size      int64     `json:"size"`
	ModTime   time.Time `json:"mod_time"`
	IsTest    bool      `json:"is_test,omitempty"`
	Extension string    `json:"extension"`
}

// Index is the persisted repository knowledge index.
type Index struct {
	Version   int                   `json:"version"`
	Root      string                `json:"root"`
	Languages map[string]int        `json:"languages"`
	Files     map[string]*FileEntry `json:"files"`
	BuiltAt   time.Time             `json:"built_at"`
}

var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, ".astra": true, "node_modules": true,
	"vendor": true, "dist": true, "build": true, "out": true, "target": true,
	".venv": true, "venv": true, "__pycache__": true, ".idea": true, ".vscode": true,
	"coverage": true, ".next": true, ".nuxt": true, ".cache": true, "Pods": true,
	"DerivedData": true, ".terraform": true, "third_party": true,
}

var maxIndexFileSize int64 = 2 << 20 // 2 MiB

// NewIndex creates an empty index rooted at root.
func NewIndex(root string) *Index {
	return &Index{Version: indexVersion, Root: root, Languages: map[string]int{}, Files: map[string]*FileEntry{}}
}

// Build scans the repository and extracts symbols.
func (ix *Index) Build() error {
	return ix.BuildWithProgress(nil)
}

// BuildWithProgress scans the repository and extracts symbols, reporting
// (done, total) file counts through progress as the scan completes. total is
// the number of files discovered before per-file symbol extraction begins.
func (ix *Index) BuildWithProgress(progress func(done, total int)) error {
	files, err := listFiles(ix.Root)
	if err != nil {
		return err
	}
	ix.Files = make(map[string]*FileEntry, len(files))
	ix.Languages = map[string]int{}

	var mu sync.Mutex
	var wg sync.WaitGroup
	var done atomic.Int64
	sem := make(chan struct{}, 8)
	if progress != nil {
		progress(0, len(files))
	}
	for _, path := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()
			entry, err := indexFile(p)
			if err != nil {
				if progress != nil {
					progress(int(done.Add(1)), len(files))
				}
				return
			}
			mu.Lock()
			ix.Files[p] = entry
			ix.Languages[langName(entry.Extension)]++
			mu.Unlock()
			if progress != nil {
				progress(int(done.Add(1)), len(files))
			}
		}(path)
	}
	wg.Wait()
	if progress != nil {
		progress(len(files), len(files))
	}
	ix.BuiltAt = time.Now().UTC()
	return nil
}

func indexFile(path string) (*FileEntry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() || info.Size() > maxIndexFileSize {
		return nil, errors.New("skip")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, 8192)
	n, _ := f.Read(head)
	if bytes.IndexByte(head[:n], 0) >= 0 {
		return nil, errors.New("binary")
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	buf := make([]byte, info.Size())
	read := 0
	for read < len(buf) {
		m, err := f.Read(buf[read:])
		read += m
		if err != nil {
			break
		}
	}
	content := string(buf)
	ext := extOf(path)
	return &FileEntry{
		Path:      filepath.ToSlash(path),
		Symbols:   ExtractSymbols(path, content),
		Lines:     strings.Count(content, "\n") + 1,
		Size:      info.Size(),
		ModTime:   info.ModTime(),
		IsTest:    isTestFile(path),
		Extension: ext,
	}, nil
}

func listFiles(root string) ([]string, error) {
	g := &Git{Root: root}
	if g.IsRepo() {
		tracked := g.TrackedFiles()
		out := make([]string, 0, len(tracked))
		for _, f := range tracked {
			if !skip(f) {
				out = append(out, filepath.Join(root, filepath.FromSlash(f)))
			}
		}
		sort.Strings(out)
		return out, nil
	}
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !skip(path) {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}

func skip(path string) bool {
	clean := filepath.ToSlash(path)
	for _, seg := range strings.Split(clean, "/") {
		if skipDirs[seg] {
			return true
		}
	}
	ext := extOf(path)
	if ext == "" {
		return true
	}
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".ico", ".svg", ".woff", ".woff2", ".ttf",
		".eot", ".pdf", ".zip", ".gz", ".tar", ".lock", ".sum", ".mod", ".min.js", ".map":
		return true
	}
	return false
}

func isTestFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	name := strings.TrimSuffix(base, filepath.Ext(base))
	if strings.Contains(name, "_test") || strings.Contains(name, ".test") ||
		strings.Contains(name, "test_") || strings.Contains(name, ".spec") ||
		strings.Contains(name, "_spec") || strings.HasPrefix(name, "test") {
		return true
	}
	for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
		switch seg {
		case "test", "tests", "__tests__", "spec", "specs", "testing":
			return true
		}
	}
	return false
}

func langName(ext string) string {
	switch ext {
	case ".go":
		return "Go"
	case ".rs":
		return "Rust"
	case ".py":
		return "Python"
	case ".ts", ".tsx":
		return "TypeScript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "JavaScript"
	case ".java":
		return "Java"
	case ".kt", ".kts":
		return "Kotlin"
	case ".c", ".h":
		return "C"
	case ".cpp", ".hpp", ".cc":
		return "C++"
	case ".cs":
		return "C#"
	case ".php":
		return "PHP"
	case ".rb":
		return "Ruby"
	case ".swift":
		return "Swift"
	case ".md", ".markdown":
		return "Markdown"
	case ".json":
		return "JSON"
	case ".yaml", ".yml":
		return "YAML"
	case ".toml":
		return "TOML"
	case ".html", ".htm":
		return "HTML"
	case ".css":
		return "CSS"
	case ".sh", ".bash":
		return "Shell"
	case ".sql":
		return "SQL"
	case ".dockerfile", "":
		return "Other"
	default:
		return "Other"
	}
}

// Save persists the index to root/.astra/index.json.
func (ix *Index) Save() error {
	dir := filepath.Join(ix.Root, ".astra")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ix, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "index.json"), data, 0o644)
}

// LoadIndex reads the persisted index.
func LoadIndex(root string) (*Index, error) {
	data, err := os.ReadFile(filepath.Join(root, ".astra", "index.json"))
	if err != nil {
		return nil, err
	}
	var ix Index
	if err := json.Unmarshal(data, &ix); err != nil {
		return nil, err
	}
	if ix.Files == nil {
		ix.Files = map[string]*FileEntry{}
	}
	if ix.Languages == nil {
		ix.Languages = map[string]int{}
	}
	return &ix, nil
}

// Touch updates the mtime/size for a changed file without a full re-scan.
func (ix *Index) Touch(path string) {
	full := filepath.Join(ix.Root, path)
	if entry, ok := ix.Files[full]; ok {
		if info, err := os.Stat(full); err == nil {
			entry.ModTime = info.ModTime()
			entry.Size = info.Size()
		}
	} else if !skip(full) {
		if entry, err := indexFile(full); err == nil {
			ix.Files[full] = entry
		}
	}
}

// TestFiles returns indexed paths that look like tests.
func (ix *Index) TestFiles() []string {
	var out []string
	for p, e := range ix.Files {
		if e.IsTest {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// Stats returns a compact summary of the index.
func (ix *Index) Stats() string {
	var b strings.Builder
	fmt.Fprintf(&b, "files=%d languages=%d tests=%d built=%s",
		len(ix.Files), len(ix.Languages), len(ix.TestFiles()), ix.BuiltAt.Format(time.RFC3339))
	return b.String()
}
