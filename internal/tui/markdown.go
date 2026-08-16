package tui

import (
	"regexp"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
)

var mdRenderer *glamour.TermRenderer
var mdWidth int
var mdOnce sync.Mutex

var (
	mdFenceRe   = regexp.MustCompile("(?s)```([a-zA-Z0-9_+\\-.]*)\\n(.*?)```")
	mdPreprocRe = regexp.MustCompile("(?m)^```")
)

const mdCacheMax = 64

// mdCache memoizes the rendered markdown output keyed by (src, width).
// Glamour + chroma are expensive enough that re-rendering every frame is
// the dominant cost during streaming; even an unbounded-by-render cache
// makes the TUI responsive on long replies.
var mdCache sync.Map // map[string]string keyed by src+":"+width

func init() {
	mdRenderer, _ = glamour.NewTermRenderer(
		glamour.WithWordWrap(96),
		glamour.WithStyles(codexMarkdownStyle()),
	)
}

// codexMarkdownStyle returns the glamour style configuration that mirrors the
// markdown rendering in codex-rs/tui (markdown_render.rs):
//
//	h1  bold underlined        h2  bold                h3  bold italic
//	h4-6 italic                code cyan               link cyan underlined
//	blockquote green with "> " ordered markers light blue
//	no decorative code-block box, no "#" heading prefixes
func codexMarkdownStyle() ansi.StyleConfig {
	str := func(s string) *string { return &s }
	on := func(v bool) *bool { return &v }
	cyan, green, lightBlue := str("36"), str("32"), str("94")
	italic := ansi.StylePrimitive{Italic: on(true)}
	return ansi.StyleConfig{
		Document: ansi.StyleBlock{},
		Heading: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Bold:        on(true),
				BlockSuffix: "\n",
			},
		},
		H1: ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{
			Bold: on(true), Underline: on(true),
		}},
		H2: ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Bold: on(true)}},
		H3: ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{
			Bold: on(true), Italic: on(true),
		}},
		H4:            ansi.StyleBlock{StylePrimitive: italic},
		H5:            ansi.StyleBlock{StylePrimitive: italic},
		H6:            ansi.StyleBlock{StylePrimitive: italic},
		Emph:          italic,
		Strong:        ansi.StylePrimitive{Bold: on(true)},
		Strikethrough: ansi.StylePrimitive{CrossedOut: on(true)},
		Code:          ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: cyan}},
		CodeBlock: ansi.StyleCodeBlock{
			StyleBlock: ansi.StyleBlock{},
			Chroma:     &ansi.Chroma{},
		},
		Link:     ansi.StylePrimitive{Color: cyan, Underline: on(true)},
		LinkText: ansi.StylePrimitive{},
		BlockQuote: ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{
			Color:  green,
			Prefix: "> ",
		}},
		Enumeration: ansi.StylePrimitive{Color: lightBlue},
		HorizontalRule: ansi.StylePrimitive{
			Color:  str("240"),
			Format: "\n———\n",
		},
	}
}

// renderMarkdown converts markdown to ANSI text for the viewport. Fenced code
// blocks are extracted and passed through chroma for syntax highlighting.
func renderMarkdown(src string) string {
	if src == "" {
		return ""
	}
	key := src
	if mdWidth > 0 {
		key = "w=" + itoa(mdWidth) + "|" + src
	}
	if v, ok := mdCache.Load(key); ok {
		return v.(string)
	}
	out := renderMarkdownFresh(src)
	memoize(key, out)
	return out
}

func renderMarkdownFresh(src string) string {
	src = mdPreprocRe.ReplaceAllString(src, "\n```")
	placeholders := map[string]string{}
	counter := 0
	src = mdFenceRe.ReplaceAllStringFunc(src, func(match string) string {
		sub := mdFenceRe.FindStringSubmatch(match)
		lang := sub[1]
		body := sub[2]
		highlighted := highlightCode(stripANSI(body), lang, "")
		counter++
		// The placeholder must be free of markdown punctuation: glamour turns
		// "__x__" into emphasis/strong, so underscores would make the later
		// ReplaceAll miss the rendered token.
		key := "ASTRA_CODE_PLACEHOLDER_" + itoa(counter)
		// Codex renders fenced code as plain syntax-highlighted lines — no
		// box, no background band, no padding. The `• `/`  ` gutter added by
		// renderAssistant keeps it aligned with the rest of the message.
		boxed := strings.TrimRight(highlighted, "\n")
		placeholders[key] = boxed
		return "\n" + key + "\n"
	})
	out, err := mdRenderer.Render(src)
	if err != nil {
		out = src
	}
	for k, v := range placeholders {
		out = strings.ReplaceAll(out, k, v)
	}
	return out
}

func memoize(key, value string) {
	count := 0
	mdCache.Range(func(_, _ any) bool { count++; return true })
	if count >= mdCacheMax {
		mdCache.Range(func(k, _ any) bool {
			mdCache.Delete(k)
			return true
		})
	}
	mdCache.Store(key, value)
}

func setMarkdownWidth(w int) {
	if w < 30 {
		w = 30
	}
	mdOnce.Lock()
	if mdWidth == w && mdRenderer != nil {
		mdOnce.Unlock()
		return
	}
	mdWidth = w
	r, err := glamour.NewTermRenderer(
		glamour.WithWordWrap(w-8),
		glamour.WithStyles(codexMarkdownStyle()),
	)
	if err == nil {
		mdRenderer = r
	}
	mdOnce.Unlock()
	mdCache = sync.Map{}
}

// stripANSI removes ANSI CSI sequences so chroma can re-highlight freshly.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inEscape := false
	for _, r := range s {
		switch {
		case inEscape:
			if r == 'm' {
				inEscape = false
			}
		case r == 0x1b:
			inEscape = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// itoa is a tiny strconv alternative to avoid pulling in a stdlib import
// from a hot-path render function. Used only by the markdown cache.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
