package tui

import (
	"regexp"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
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
		glamour.WithStandardStyle("dark"),
	)
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
		key := "__ASTRA_CODE_" + itoa(counter) + "__"
		boxed := styleCodeBlock.Render(highlighted)
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
		glamour.WithStandardStyle("dark"),
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
