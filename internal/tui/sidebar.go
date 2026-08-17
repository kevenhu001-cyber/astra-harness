package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kevenhu001-cyber/astra-harness/internal/core"
	"github.com/kevenhu001-cyber/astra-harness/internal/engine"
)

// sidebarMode picks what the sidebar shows.
type sidebarMode int

const (
	sidebarSessions sidebarMode = iota
	sidebarFiles
	sidebarGoals
	sidebarActivity
)

type sidebarItem struct {
	label  string
	detail string
	icon   string
	id     string
	mode   sidebarMode
}

type sidebar struct {
	visible bool
	mode   sidebarMode
	cursor int
	files  []string
	width  int
	height int

	// Tab labels for the mode switcher shown at the top of the sidebar.
	tabs []string

	// sessions filtered to root
	root    string
	engine  *engine.Engine
	loading bool

	lastRefresh time.Time

	// screenLeft / screenTop describe where the sidebar is laid out within
	// the terminal (set by app.layout). The app routes mouse clicks into the
	// sidebar when MouseMsg.X >= screenLeft.
	screenLeft int
	screenTop  int
}

func newSidebar(e *engine.Engine) *sidebar {
	return &sidebar{
		root:  e.Root,
		engine: e,
		mode:  sidebarSessions,
		width: 26,
		tabs:  []string{"Sessions", "Files", "Knowledge", "Activity"},
	}
}

func (s *sidebar) SetSize(w, h int) {
	s.width = w
	s.height = h
}

func (s *sidebar) Toggle() { s.visible = !s.visible }

func (s *sidebar) NextMode() {
	s.mode = (s.mode + 1) % 4
	s.cursor = 0
}

func (s *sidebar) items() []sidebarItem {
	switch s.mode {
	case sidebarFiles:
		return s.fileItems()
	case sidebarGoals:
		return s.goalItems()
	case sidebarActivity:
		return s.activityItems()
	}
	return s.sessionItems()
}

func (s *sidebar) sessionItems() []sidebarItem {
	if s.engine == nil {
		return nil
	}
	sessions, _ := s.engine.Store.ListSessions()
	cur := s.engine.SessionID()
	out := []sidebarItem{
		{label: "(new session)", detail: "start a fresh conversation", icon: "+", mode: sidebarSessions, id: "__new__"},
	}
	for _, sess := range sessions {
		label := sess.ID
		if len(label) > 14 {
			label = label[:8] + "…" + label[len(label)-4:]
		}
		marker := " "
		if sess.ID == cur {
			marker = "●"
		}
		out = append(out, sidebarItem{
			label:  fmt.Sprintf("%s %s", marker, label),
			detail: fmt.Sprintf("%s · %d msgs · %s", sess.Model, len(sess.Messages), sess.UpdatedAt.Format("01-02 15:04")),
			icon:   "✦",
			id:     sess.ID,
			mode:   sidebarSessions,
		})
	}
	return out
}

func (s *sidebar) fileItems() []sidebarItem {
	if s.files == nil || time.Since(s.lastRefresh) > 30*time.Second {
		s.files = scanProjectFiles(s.root)
		s.lastRefresh = time.Now()
	}
	out := make([]sidebarItem, 0, len(s.files))
	for i, p := range s.files {
		if i > 200 {
			break
		}
		info, err := os.Stat(filepath.Join(s.root, p))
		size := ""
		if err == nil {
			size = humanSize(info.Size())
		}
		out = append(out, sidebarItem{
			label:  p,
			detail: size,
			icon:   iconForFile(p),
			id:     p,
			mode:   sidebarFiles,
		})
	}
	return out
}

func (s *sidebar) goalItems() []sidebarItem {
	if s.engine == nil {
		return nil
	}
	st := s.engine.Store.State
	out := []sidebarItem{}
	if g := s.engine.Store.ActiveGoal(); g != nil {
		out = append(out, sidebarItem{
			label:  truncate(g.Description, 28),
			detail: fmt.Sprintf("active · %.0f%%", g.Progress*100),
			icon:   "◇",
			id:     g.ID,
			mode:   sidebarGoals,
		})
	}
	for _, c := range st.Claims {
		out = append(out, sidebarItem{
			label:  fmt.Sprintf("[%s] %s %s", c.Status, truncate(c.Subject, 14), truncate(c.Predicate, 10)),
			detail: fmt.Sprintf("conf %.0f%% · ev %d", c.Confidence*100, len(c.EvidenceIDs)),
			icon:   "✓",
			id:     c.ID,
			mode:   sidebarGoals,
		})
	}
	unks := core.RankUnknowns(st.Unknowns)
	for i, u := range unks {
		if i > 12 {
			break
		}
		out = append(out, sidebarItem{
			label:  truncate(u.Description, 28),
			detail: fmt.Sprintf("p=%.2f · imp %.0f%%", u.Priority, u.Impact*100),
			icon:   "?",
			id:     u.ID,
			mode:   sidebarGoals,
		})
	}
	if len(out) == 0 {
		out = append(out, sidebarItem{label: "(no knowledge yet)", detail: "run /index to build"})
	}
	return out
}

func (s *sidebar) activityItems() []sidebarItem {
	if s.engine == nil {
		return nil
	}
	var out []sidebarItem
	ev := s.engine.Store.EvidenceRecent(15)
	for _, x := range ev {
		out = append(out, sidebarItem{
			label:  truncate(x.Source, 28),
			detail: fmt.Sprintf("%s · %.0f%%", x.Status, x.Confidence*100),
			icon:   "▣",
			id:     x.ID,
			mode:   sidebarActivity,
		})
	}
	if len(out) == 0 {
		out = append(out, sidebarItem{label: "(no evidence yet)", detail: ""})
	}
	return out
}

// Update handles messages and returns side-effect commands.
func (s *sidebar) Update(msg tea.Msg) tea.Cmd {
	if !s.visible {
		return nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	items := s.items()
	switch key.String() {
	case "up", "k":
		if len(items) > 0 {
			s.cursor = (s.cursor - 1 + len(items)) % len(items)
		}
	case "down", "j":
		if len(items) > 0 {
			s.cursor = (s.cursor + 1) % len(items)
		}
	case "tab", "m":
		s.NextMode()
	case "1", "2", "3", "4":
		idx := int(key.String()[0] - '1')
		if idx < len(s.tabs) {
			s.mode = sidebarMode(idx)
			s.cursor = 0
		}
	}
	return nil
}

// Selected returns the cursor-selected item (or nil).
func (s *sidebar) Selected() *sidebarItem {
	items := s.items()
	if len(items) == 0 {
		return nil
	}
	if s.cursor >= len(items) {
		s.cursor = len(items) - 1
	}
	return &items[s.cursor]
}

func (s *sidebar) View() string {
	if !s.visible {
		return ""
	}
	w := s.width
	if w < 18 {
		w = 18
	}
	if w > 38 {
		w = 38
	}
	hint := "↑↓ navigate · tab switch · enter open"
	var b strings.Builder

	// Tab strip — active tab is rendered with the accent key style, others dim.
	// We wrap to two rows if the combined length exceeds the sidebar width.
	tabs := s.tabs
	var tabsLine strings.Builder
	var rowLen int
	for i, t := range tabs {
		rendered := styleKey.Render(t)
		if i != int(s.mode) {
			rendered = styleDim.Render(t)
		}
		seg := " " + rendered + " "
		if rowLen+lipgloss.Width(seg) > w-2 {
			tabsLine.WriteString("\n")
			rowLen = 0
			seg = rendered + " "
		}
		tabsLine.WriteString(seg)
		rowLen += lipgloss.Width(seg)
	}
	b.WriteString(tabsLine.String())
	b.WriteString("\n")
	b.WriteString(styleFaint.Render(strings.Repeat("─", w-2)))
	b.WriteString("\n")

	items := s.items()
	maxLines := s.height - 6
	if maxLines < 4 {
		maxLines = 4
	}
	if maxLines > 20 {
		maxLines = 20
	}
	start := 0
	if s.cursor >= maxLines {
		start = s.cursor - maxLines + 1
	}
	end := start + maxLines
	if end > len(items) {
		end = len(items)
	}
	for i := start; i < end; i++ {
		it := items[i]
		label := fmt.Sprintf("%s %s", it.icon, it.label)
		if len(label) > w-9 {
			label = label[:w-10] + "…"
		}
		ordinal := fmt.Sprintf("%d.", i-start+1)
		var prefix string
		if i == s.cursor {
			prefix = styleKey.Render("›") + " " + styleKey.Render(ordinal) + " "
		} else {
			prefix = "  " + styleDim.Render(ordinal) + " "
		}
		detail := truncate(it.detail, w-8)
		var labelStr string
		if i == s.cursor {
			labelStr = styleTitle.Render(label)
		} else {
			labelStr = styleBody.Render(label)
		}
		b.WriteString(prefix + labelStr + "\n")
		if detail != "" {
			if i == s.cursor {
				b.WriteString(styleDim.Render("    " + detail))
			} else {
				b.WriteString(styleDim.Render("    " + detail))
			}
			b.WriteString("\n")
		}
	}
	if len(items) == 0 {
		b.WriteString(styleDim.Render("  (empty)"))
		b.WriteString("\n")
	}
	if len(items) > end {
		b.WriteString(styleDim.Render(fmt.Sprintf("  …+%d more", len(items)-end)))
		b.WriteString("\n")
	}
	b.WriteString(styleFaint.Render(strings.Repeat("─", w-2)))
	b.WriteString("\n")
	b.WriteString(styleDim.Render(hint))
	return lipgloss.NewStyle().Width(w).Height(s.height-2).Render(strings.TrimRight(b.String(), "\n"))
}

// HitAt maps a terminal (x,y) screen coordinate to a sidebar item and tab
// when the click lands inside the sidebar. Returns the item (or nil) and
// the new mode if the click landed on a tab. The app uses this to route
// MouseMsg events into the sidebar.
func (s *sidebar) HitAt(x, y int) (*sidebarItem, *sidebarMode) {
	if !s.visible || s.screenLeft == 0 {
		return nil, nil
	}
	if x < s.screenLeft || x >= s.screenLeft+s.width {
		return nil, nil
	}
	if y < s.screenTop {
		// Click is on the tab strip — switch tabs by tab number.
		rel := y - s.screenTop
		if rel == 0 {
			for i := range s.tabs {
				mode := sidebarMode(i)
				return nil, &mode
			}
		}
		return nil, nil
	}
	// Click is on the item area. Each item is two rows (label + detail), so
	// divide the relative y by 2 to get the item index.
	items := s.items()
	if len(items) == 0 {
		return nil, nil
	}
	relRow := (y - s.screenTop - 1) / 2 // -1 for the tab strip
	idx := relRow
	if idx >= 0 && idx < len(items) {
		s.cursor = idx
		return &items[idx], nil
	}
	return nil, nil
}

func iconForFile(p string) string {
	ext := strings.ToLower(filepath.Ext(p))
	switch ext {
	case ".go":
		return "●"
	case ".rs":
		return "●"
	case ".py":
		return "●"
	case ".ts", ".tsx":
		return "▲"
	case ".js", ".jsx":
		return "▲"
	case ".json", ".yaml", ".yml", ".toml":
		return "≡"
	case ".md", ".txt":
		return "✎"
	case ".html", ".css", ".scss":
		return "❡"
	default:
		return "·"
	}
}

func scanProjectFiles(root string) []string {
	skip := map[string]bool{
		".git": true, ".astra": true, "node_modules": true, "vendor": true,
		"target": true, "dist": true, "build": true, "out": true, ".next": true,
		"__pycache__": true, ".venv": true, "venv": true,
	}
	var out []string
	walk := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if skip[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	}
	_ = filepath.Walk(root, walk)
	return out
}

func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

// hitEnterOnSidebar returns the item the user selected (if any) and the
// action that should run on the main app. Acts on j/k/tab and enter.
func (s *sidebar) Hit() (*sidebarItem, bool) {
	if !s.visible {
		return nil, false
	}
	it := s.Selected()
	if it == nil {
		return nil, false
	}
	return it, true
}
