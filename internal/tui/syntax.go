package tui

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// chromaStyle picks a chroma style tuned for our terminal.
func chromaStyle() *chroma.Style {
	if s := chroma.MustNewStyle("astra-dark", chroma.StyleEntries{
		chroma.Comment:           "60 #7f7f9f italic",
		chroma.CommentPreproc:    "60 #7f7f9f italic",
		chroma.Keyword:           "141 #ff79c6 bold",
		chroma.KeywordConstant:   "141 #ff79c6",
		chroma.KeywordDeclaration: "141 #ff79c6",
		chroma.KeywordType:       "141 #bd93f9",
		chroma.Name:              "111 #8be9fd",
		chroma.NameClass:         "111 #8be9fd",
		chroma.NameFunction:      "215 #ffb86c",
		chroma.NameVariable:      "252 #f8f8f2",
		chroma.NameBuiltin:       "215 #ffb86c",
		chroma.LiteralString:     "223 #f1fa8c",
		chroma.LiteralNumber:     "209 #ff9580",
		chroma.Operator:          "141 #ff79c6",
		chroma.Punctuation:       "245 #a0a0b5",
		chroma.Error:             "203 #ff5555",
		chroma.Text:              "252 #f8f8f2",
		chroma.Background:        "bg #15151f",
	}); s != nil {
		return s
	}
	if s := styles.Fallback; s != nil {
		return s
	}
	return styles.Fallback
}

// highlightCode returns a chroma-iterator-formatted code snippet.
func highlightCode(src, lang, filename string) string {
	if strings.TrimSpace(src) == "" {
		return ""
	}
	var lexer chroma.Lexer
	if lang != "" {
		lexer = lexers.Get(lang)
	}
	if lexer == nil && filename != "" {
		lexer = lexers.Match(filename)
	}
	if lexer == nil {
		lexer = lexers.Analyse(src)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)
	style := chromaStyle()
	opts := &chroma.TokeniseOptions{}
	it, err := lexer.Tokenise(opts, src)
	if err != nil {
		return src
	}
	var sb strings.Builder
	for _, t := range it.Tokens() {
		sb.WriteString(colorizeToken(t))
	}
	_ = style // keep style for future formatter use
	return sb.String()
}

// colorizeToken applies an ANSI color matching the chroma token's category.
func colorizeToken(t chroma.Token) string {
	val := t.Value
	if val == "" {
		return val
	}
	if val == "\n" || val == " " || val == "\t" {
		return val
	}
	c := colorForType(t.Type)
	if c == "" {
		return val
	}
	return "\x1b[38;5;" + c + "m" + val + "\x1b[0m"
}

func colorForType(typ chroma.TokenType) string {
	if typ.InCategory(chroma.Comment) {
		return "60"
	}
	if typ.InCategory(chroma.Keyword) {
		return "141"
	}
	if typ.InCategory(chroma.NameClass) || typ.InCategory(chroma.NameFunction) || typ == chroma.NameClass || typ == chroma.NameFunction {
		return "215"
	}
	if typ.InCategory(chroma.Name) || typ == chroma.Name {
		return "111"
	}
	if typ.InCategory(chroma.NameBuiltin) || typ == chroma.NameBuiltin || typ == chroma.NameBuiltinPseudo {
		return "215"
	}
	if typ.InCategory(chroma.LiteralString) || typ == chroma.LiteralString {
		return "223"
	}
	if typ.InCategory(chroma.LiteralNumber) || typ == chroma.LiteralNumber {
		return "209"
	}
	if typ.InCategory(chroma.Operator) || typ == chroma.Operator {
		return "141"
	}
	if typ.InCategory(chroma.Punctuation) || typ == chroma.Punctuation {
		return "245"
	}
	if typ == chroma.Error {
		return "203"
	}
	if typ == chroma.Text || typ.InCategory(chroma.Text) {
		return "252"
	}
	return ""
}

// detectLang guesses a chroma lexer name from a filename or language hint.
func detectLang(name, hint string) string {
	if hint != "" {
		return hint
	}
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".go"):
		return "go"
	case strings.HasSuffix(n, ".rs"):
		return "rust"
	case strings.HasSuffix(n, ".py"):
		return "python"
	case strings.HasSuffix(n, ".ts"):
		return "typescript"
	case strings.HasSuffix(n, ".tsx"):
		return "tsx"
	case strings.HasSuffix(n, ".js"), strings.HasSuffix(n, ".jsx"), strings.HasSuffix(n, ".mjs"):
		return "javascript"
	case strings.HasSuffix(n, ".json"):
		return "json"
	case strings.HasSuffix(n, ".yaml"), strings.HasSuffix(n, ".yml"):
		return "yaml"
	case strings.HasSuffix(n, ".toml"):
		return "toml"
	case strings.HasSuffix(n, ".md"), strings.HasSuffix(n, ".markdown"):
		return "markdown"
	case strings.HasSuffix(n, ".html"), strings.HasSuffix(n, ".htm"):
		return "html"
	case strings.HasSuffix(n, ".css"), strings.HasSuffix(n, ".scss"):
		return "css"
	case strings.HasSuffix(n, ".sh"), strings.HasSuffix(n, ".bash"), strings.HasSuffix(n, ".zsh"):
		return "bash"
	case strings.HasSuffix(n, ".sql"):
		return "sql"
	case strings.HasSuffix(n, ".kt"), strings.HasSuffix(n, ".kts"):
		return "kotlin"
	case strings.HasSuffix(n, ".java"):
		return "java"
	case strings.HasSuffix(n, ".c"), strings.HasSuffix(n, ".h"):
		return "c"
	case strings.HasSuffix(n, ".cpp"), strings.HasSuffix(n, ".hpp"), strings.HasSuffix(n, ".cc"), strings.HasSuffix(n, ".cxx"):
		return "cpp"
	case strings.HasSuffix(n, ".cs"):
		return "csharp"
	case strings.HasSuffix(n, ".rb"):
		return "ruby"
	case strings.HasSuffix(n, ".php"):
		return "php"
	case strings.HasSuffix(n, ".swift"):
		return "swift"
	case strings.HasSuffix(n, ".lua"):
		return "lua"
	case strings.HasSuffix(n, ".proto"):
		return "protobuf"
	case strings.HasSuffix(n, ".xml"):
		return "xml"
	case strings.HasSuffix(n, ".ini"), strings.HasSuffix(n, ".conf"), strings.HasSuffix(n, ".cfg"):
		return "ini"
	case strings.HasSuffix(n, ".diff"), strings.HasSuffix(n, ".patch"):
		return "diff"
	}
	return ""
}
