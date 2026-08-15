package knowledge

import (
	"regexp"
	"strings"
)

// Symbol is a lightweight structural entry extracted without a full parser.
type Symbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Signature string `json:"signature,omitempty"`
	Line      int    `json:"line"`
}

type symbolRule struct {
	re   *regexp.Regexp
	kind string
	idx  int
}

var symbolRules = map[string][]symbolRule{
	".go": {
		{regexp.MustCompile(`^\s*(?:func)\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)`), "func", 1},
		{regexp.MustCompile(`^\s*(?:type)\s+([A-Za-z_]\w*)`), "type", 1},
		{regexp.MustCompile(`^\s*(?:var|const)\s+([A-Za-z_]\w*)`), "var", 1},
	},
	".rs": {
		{regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)`), "fn", 1},
		{regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:struct|enum|trait|mod)\s+([A-Za-z_]\w*)`), "type", 1},
		{regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:const|static)\s+([A-Za-z_]\w*)`), "const", 1},
	},
	".py": {
		{regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)`), "func", 1},
		{regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)`), "class", 1},
	},
	".ts":  tsRule(),
	".tsx": tsRule(),
	".js":  tsRule(),
	".jsx": tsRule(),
	".mjs": tsRule(),
	".cjs": tsRule(),
	".java": {
		{regexp.MustCompile(`^\s*(?:public|protected|private)?\s*(?:static\s+)?(?:final\s+)?(?:class|interface|enum|record)\s+([A-Za-z_]\w*)`), "class", 1},
		{regexp.MustCompile(`^\s*(?:public|protected|private)?\s*(?:static\s+)?(?:final\s+)?[\w<>\[\],.?]+\s+([A-Za-z_]\w*)\s*\([^;{]*\)\s*\{`), "method", 1},
	},
	".kt":  ktRule(),
	".kts": ktRule(),
	".c":   cRule(),
	".h":   cRule(),
	".cpp": cRule(),
	".hpp": cRule(),
	".cc":  cRule(),
	".cs":  cRule(),
	".php": {
		{regexp.MustCompile(`^\s*(?:public|protected|private)?\s*function\s+([A-Za-z_]\w*)`), "func", 1},
		{regexp.MustCompile(`^\s*(?:abstract\s+|final\s+)?class\s+([A-Za-z_]\w*)`), "class", 1},
	},
	".rb": {
		{regexp.MustCompile(`^\s*(?:def)\s+([A-Za-z_]\w*[!?]?)`), "func", 1},
		{regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)`), "class", 1},
		{regexp.MustCompile(`^\s*module\s+([A-Za-z_]\w*)`), "module", 1},
	},
}

func tsRule() []symbolRule {
	return []symbolRule{
		{regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?(?:function|class|interface|type|enum)\s+([A-Za-z_$][\w$]*)`), "decl", 1},
		{regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)`), "var", 1},
	}
}

func ktRule() []symbolRule {
	return []symbolRule{
		{regexp.MustCompile(`^\s*(?:public|private|protected|internal)?\s*(?:suspend\s+)?fun\s+([A-Za-z_]\w*)`), "fun", 1},
		{regexp.MustCompile(`^\s*(?:public|private|protected|internal)?\s*(?:data\s+|sealed\s+|enum\s+)?(?:class|interface|object)\s+([A-Za-z_]\w*)`), "class", 1},
	}
}

func cRule() []symbolRule {
	return []symbolRule{
		{regexp.MustCompile(`^\s*(?:typedef\s+)?(?:struct|class|enum|union|interface)\s+([A-Za-z_]\w*)`), "type", 1},
		{regexp.MustCompile(`^\s*(?:public|protected|private|internal|static|virtual|override|async)\s+.*?\b([A-Za-z_]\w*)\s*\([^;{}]*\)\s*(?:const\s*)?\{`), "method", 1},
	}
}

// ExtractSymbols returns structural symbols for a source file.
func ExtractSymbols(path, content string) []Symbol {
	ext := extOf(path)
	rules, ok := symbolRules[ext]
	if !ok {
		return nil
	}
	var out []Symbol
	for i, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimRight(line, "\r")
		for _, rule := range rules {
			if m := rule.re.FindStringSubmatch(trimmed); m != nil {
				name := m[rule.idx]
				sig := strings.TrimSpace(trimmed)
				if len(sig) > 120 {
					sig = sig[:117] + "..."
				}
				out = append(out, Symbol{Name: name, Kind: rule.kind, Signature: sig, Line: i + 1})
				break
			}
		}
	}
	return out
}

func extOf(path string) string {
	lower := strings.ToLower(path)
	i := strings.LastIndexByte(lower, '.')
	if i < 0 {
		return ""
	}
	return lower[i:]
}
