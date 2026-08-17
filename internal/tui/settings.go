package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kevenhu001-cyber/astra-harness/internal/engine"
)

// modelSettings is the interactive provider-configuration form: a selectable
// provider chip row, three labeled text fields (base URL, API key, model ID),
// and an action button row. It is keyboard- and mouse-driven so the user
// never has to type long /set-* commands.
type modelSettings struct {
	eng       *engine.Engine
	providers []engine.ProviderConfig
	provSel   int
	fields    []textinput.Model // 0: base URL, 1: API key, 2: model ID
	fieldSel  int
	zone      settingsZone
	btnSel    int
	origCount int // provider count when the form opened (for cancel rollback)
	notice    string
	err       string
	rects     []settingsRect
}

type settingsZone int

const (
	settingsZoneProviders settingsZone = iota
	settingsZoneFields
	settingsZoneButtons
)

// settingsRect is a clickable screen region recorded during View so a later
// MouseMsg can be routed to it.
type settingsRect struct {
	x, y, w, h int
	action     string
}

type settingsButton struct {
	label  string
	action string
}

var settingsButtons = []settingsButton{
	{label: "Save & Use", action: "save-use"},
	{label: "Save", action: "save"},
	{label: "Reset", action: "reset"},
	{label: "Cancel", action: "cancel"},
}

func newModelSettings(a *app) *modelSettings {
	s := &modelSettings{
		eng:       a.engine,
		providers: a.engine.Config.Providers,
		origCount: len(a.engine.Config.Providers),
	}
	for i, p := range s.providers {
		if p.ID == a.engine.ProviderID() {
			s.provSel = i
			break
		}
	}
	for i := 0; i < 3; i++ {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 0
		if i == 1 {
			ti.EchoMode = textinput.EchoPassword
			ti.EchoCharacter = '•'
		}
		s.fields = append(s.fields, ti)
	}
	s.loadFields()
	// With a single provider, jump straight into the first field so the
	// user can type immediately; otherwise let them pick a provider first.
	if len(s.providers) == 1 {
		s.zone = settingsZoneFields
		s.fieldSel = 0
		s.focusField()
	}
	return s
}

// keyHint explains how the API key is currently provided.
func (s *modelSettings) keyHint(p engine.ProviderConfig) string {
	if p.APIKey != "" {
		return "already set — type to replace"
	}
	if p.APIKeyEnv != "" {
		return "uses env " + p.APIKeyEnv + " — or type a key"
	}
	return "type an API key (saved to .astra/config.json)"
}

// loadFields fills the text fields from the selected provider.
func (s *modelSettings) loadFields() {
	if len(s.providers) == 0 {
		return
	}
	p := s.providers[s.provSel]
	s.fields[0].SetValue(p.BaseURL)
	s.fields[1].SetValue("")
	s.fields[1].Placeholder = s.keyHint(p)
	model := ""
	for _, m := range p.Models {
		if m == s.eng.Config.DefaultModel {
			model = m
			break
		}
	}
	if model == "" && len(p.Models) > 0 {
		model = p.Models[0]
	}
	s.fields[2].SetValue(model)
}

// focusField focuses the current text field (and blurs the others).
func (s *modelSettings) focusField() {
	for i := range s.fields {
		if i == s.fieldSel {
			s.fields[i].Focus()
		} else {
			s.fields[i].Blur()
		}
	}
}

// blurFields blurs every text field (used when leaving the fields zone).
func (s *modelSettings) blurFields() {
	for i := range s.fields {
		s.fields[i].Blur()
	}
}

// update routes one message through the form. Returns (done, closeMsg, cmd):
// done closes the form (closeMsg is shown as a system message), cmd is a
// tea command to run (cursor blink while a field is focused).
func (s *modelSettings) update(msg tea.Msg, a *app) (bool, string, tea.Cmd) {
	switch m := msg.(type) {
	case tea.MouseMsg:
		if m.Type == tea.MouseLeft {
			for _, r := range s.rects {
				if m.X >= r.x && m.X < r.x+r.w && m.Y >= r.y && m.Y < r.y+r.h {
					return s.activate(r.action, a)
				}
			}
		}
		return false, "", nil
	case tea.KeyMsg:
		return s.handleKey(m, a)
	}
	return false, "", nil
}

func (s *modelSettings) handleKey(msg tea.KeyMsg, a *app) (bool, string, tea.Cmd) {
	k := msg.String()
	switch s.zone {
	case settingsZoneProviders:
		switch k {
		case "esc":
			return s.cancel(a)
		case "up", "k":
			if len(s.providers) > 1 {
				s.provSel = (s.provSel - 1 + len(s.providers)) % len(s.providers)
				s.loadFields()
			}
		case "down", "j":
			if len(s.providers) > 1 {
				s.provSel = (s.provSel + 1) % len(s.providers)
				s.loadFields()
			}
		case "enter", "tab", "right":
			if len(s.providers) > 0 {
				s.zone = settingsZoneFields
				s.fieldSel = 0
				s.focusField()
				return false, "", s.fields[0].Focus()
			}
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			idx := int(k[0] - '1')
			if idx < len(s.providers) {
				s.provSel = idx
				s.loadFields()
			}
		case "a":
			s.addProvider()
		}
	case settingsZoneFields:
		switch k {
		case "esc":
			s.zone = settingsZoneProviders
			s.blurFields()
		case "tab", "enter", "down":
			s.fieldSel++
			if s.fieldSel >= len(s.fields) {
				s.zone = settingsZoneButtons
				s.btnSel = 0
				s.blurFields()
			} else {
				s.focusField()
				return false, "", s.fields[s.fieldSel].Focus()
			}
		case "shift+tab", "up":
			if s.fieldSel > 0 {
				s.fieldSel--
				s.focusField()
				return false, "", s.fields[s.fieldSel].Focus()
			}
			s.zone = settingsZoneProviders
			s.blurFields()
		default:
			s.fields[s.fieldSel], _ = s.fields[s.fieldSel].Update(msg)
		}
	case settingsZoneButtons:
		switch k {
		case "esc":
			s.zone = settingsZoneFields
			s.fieldSel = len(s.fields) - 1
			s.focusField()
		case "left":
			s.btnSel = (s.btnSel - 1 + len(settingsButtons)) % len(settingsButtons)
		case "right", "tab":
			s.btnSel = (s.btnSel + 1) % len(settingsButtons)
		case "enter", " ":
			return s.activate("btn:"+settingsButtons[s.btnSel].action, a)
		}
	}
	return false, "", nil
}

// activate runs a click/select action (provider chip, field row, or button).
func (s *modelSettings) activate(action string, a *app) (bool, string, tea.Cmd) {
	switch {
	case strings.HasPrefix(action, "prov:"):
		var idx int
		fmt.Sscanf(action, "prov:%d", &idx)
		if idx >= 0 && idx < len(s.providers) {
			s.provSel = idx
			s.loadFields()
		}
	case strings.HasPrefix(action, "field:"):
		var idx int
		fmt.Sscanf(action, "field:%d", &idx)
		if idx >= 0 && idx < len(s.fields) {
			s.zone = settingsZoneFields
			s.fieldSel = idx
			s.focusField()
			return false, "", s.fields[idx].Focus()
		}
	case action == "add":
		s.addProvider()
	case strings.HasPrefix(action, "btn:"):
		return s.runButton(strings.TrimPrefix(action, "btn:"), a)
	}
	return false, "", nil
}

func (s *modelSettings) runButton(name string, a *app) (bool, string, tea.Cmd) {
	switch name {
	case "save-use":
		return s.save(a, true)
	case "save":
		return s.save(a, false)
	case "reset":
		s.loadFields()
		s.err = ""
		s.notice = "changes discarded — fields reloaded"
	case "cancel":
		return s.cancel(a)
	}
	return false, "", nil
}

// save persists the edited provider (and optionally activates it). Empty
// fields keep their current values so a masked key field is safe to leave
// untouched.
func (s *modelSettings) save(a *app, use bool) (bool, string, tea.Cmd) {
	if len(s.providers) == 0 {
		s.err = "no provider selected — add one first"
		return false, "", nil
	}
	p := s.providers[s.provSel]
	url := strings.TrimSpace(s.fields[0].Value())
	key := strings.TrimSpace(s.fields[1].Value())
	model := strings.TrimSpace(s.fields[2].Value())
	if err := a.engine.UpdateProvider(p.ID, url, key, model); err != nil {
		s.err = err.Error()
		return false, "", nil
	}
	if use && model != "" {
		if err := a.engine.SwitchModel(p.ID, model); err != nil {
			s.err = "saved, but could not switch: " + err.Error()
			return false, "", nil
		}
	}
	msg := "provider " + p.ID + " updated"
	if url != "" {
		msg += " · url=" + url
	}
	if key != "" {
		msg += " · api key saved"
	}
	if model != "" {
		msg += " · model=" + model
	}
	if use && model != "" {
		msg += " (now active)"
	}
	return true, msg, nil
}

// cancel closes the form and rolls back any provider added this session
// (added providers are only in memory until saved).
func (s *modelSettings) cancel(a *app) (bool, string, tea.Cmd) {
	if len(s.eng.Config.Providers) > s.origCount {
		s.eng.Config.Providers = s.eng.Config.Providers[:s.origCount]
	}
	return true, "", nil
}

// addProvider appends a fresh openai-compatible provider (in memory; it is
// persisted when the user hits Save) and jumps into its fields.
func (s *modelSettings) addProvider() {
	cfg := s.eng.Config
	n := 1
	id := ""
	for {
		id = fmt.Sprintf("custom%d", n)
		exists := false
		for _, p := range cfg.Providers {
			if p.ID == id {
				exists = true
				break
			}
		}
		if !exists {
			break
		}
		n++
	}
	cfg.Providers = append(cfg.Providers, engine.ProviderConfig{
		ID:   id,
		Type: "openai-compatible",
		Name: "Custom " + fmt.Sprintf("%d", n),
	})
	s.providers = cfg.Providers
	s.provSel = len(cfg.Providers) - 1
	s.loadFields()
	s.zone = settingsZoneFields
	s.fieldSel = 0
	s.focusField()
	s.err = ""
	s.notice = "new provider " + id + " added — fill in URL / key / model, then Save"
}

// View renders the centered settings card and refreshes the clickable rects.
func (s *modelSettings) View(width, height int) string {
	pal := activePalette()
	boxW := width - 8
	if boxW > 100 {
		boxW = 100
	}
	if boxW < 50 {
		boxW = 50
	}
	contentW := boxW - 6
	inputW := contentW - 20
	if inputW < 20 {
		inputW = 20
	}
	for i := range s.fields {
		s.fields[i].Width = inputW
	}

	var lines []string
	var rects []settingsRect

	lines = append(lines, styleTitle.Render("◆ Model Settings")+"  "+styleDim.Render("base URL · API key · model ID"))
	lines = append(lines, "")

	// Provider chips (clickable, keyboard selectable).
	lines = append(lines, styleDim.Render("Providers"))
	chipLine := ""
	chipX := 0
	row := len(lines)
	for i, p := range s.providers {
		label := p.ID
		if p.ID == s.eng.ProviderID() {
			label = "● " + label
		}
		chip := " " + label + " "
		var st lipgloss.Style
		if i == s.provSel {
			st = lipgloss.NewStyle().Background(pal.Accent).Foreground(pal.Bg0).Bold(true)
		} else {
			st = lipgloss.NewStyle().Foreground(pal.WhiteDim)
		}
		rendered := st.Render(chip)
		w := lipgloss.Width(rendered)
		if chipX > 0 && chipX+w+2 > contentW {
			lines = append(lines, chipLine)
			row = len(lines)
			chipLine = ""
			chipX = 0
		}
		rects = append(rects, settingsRect{x: chipX, y: row, w: w, h: 1, action: fmt.Sprintf("prov:%d", i)})
		chipLine += rendered + "  "
		chipX += w + 2
	}
	addChip := " + add provider "
	rendered := lipgloss.NewStyle().Foreground(pal.Accent).Render(addChip)
	w := lipgloss.Width(rendered)
	if chipX > 0 && chipX+w+2 > contentW {
		lines = append(lines, chipLine)
		row = len(lines)
		chipLine = ""
		chipX = 0
	}
	rects = append(rects, settingsRect{x: chipX, y: row, w: w, h: 1, action: "add"})
	chipLine += rendered
	lines = append(lines, chipLine)
	lines = append(lines, "")

	// Labeled text fields.
	labels := []string{"Base URL", "API Key", "Model ID"}
	for i, name := range labels {
		focused := s.zone == settingsZoneFields && s.fieldSel == i
		var label string
		if focused {
			label = styleKey.Render("› " + padRight(name, 8))
		} else {
			label = styleDim.Render("  " + padRight(name, 8))
		}
		// Single-line input box: left/right border only, so each field stays
		// one row tall and the click rect stays aligned.
		box := lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, true, false, true).Padding(0, 1)
		if focused {
			box = box.BorderForeground(pal.Accent)
		} else {
			box = box.BorderForeground(pal.GrayLo)
		}
		lines = append(lines, label+" "+box.Render(s.fields[i].View()))
		rects = append(rects, settingsRect{x: 10, y: len(lines) - 1, w: contentW - 12, h: 1, action: fmt.Sprintf("field:%d", i)})
	}
	lines = append(lines, "")

	// Action buttons.
	btnLine := ""
	btnX := 0
	row = len(lines)
	for i, b := range settingsButtons {
		chip := " " + b.label + " "
		var st lipgloss.Style
		if s.zone == settingsZoneButtons && s.btnSel == i {
			// Selected button: solid orange block (matches the provider chip).
			st = lipgloss.NewStyle().Background(pal.Accent).Foreground(pal.Bg0).Bold(true).Padding(0, 1)
			chip = "›" + chip
		} else {
			st = lipgloss.NewStyle().Foreground(pal.WhiteDim).Padding(0, 1)
		}
		rendered := st.Render(chip)
		w := lipgloss.Width(rendered)
		if btnX > 0 && btnX+w+2 > contentW {
			lines = append(lines, btnLine)
			row = len(lines)
			btnLine = ""
			btnX = 0
		}
		rects = append(rects, settingsRect{x: btnX, y: row, w: w, h: 1, action: "btn:" + b.action})
		btnLine += rendered + "  "
		btnX += w + 2
	}
	lines = append(lines, btnLine)
	lines = append(lines, "")

	// Status line.
	if s.err != "" {
		lines = append(lines, styleError.Render("✗ "+s.err))
	} else if s.notice != "" {
		lines = append(lines, styleOk.Render("• "+s.notice))
	} else {
		lines = append(lines, styleDim.Render("URL, key and model are saved to .astra/config.json · empty fields keep current values"))
	}
	lines = append(lines, styleDim.Render("↑↓ choose · tab / ⏎ next · esc back · mouse click · Save & Use activates"))

	content := strings.Join(lines, "\n")
	box := styleOverlay.Width(boxW).Render(content)
	boxH := lipgloss.Height(box)
	x0 := (width - boxW) / 2
	y0 := (height - boxH) / 2
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	// Convert content-relative rects to screen coords: border 1 + padding 2
	// on the left, border 1 + padding 1 on the top.
	s.rects = s.rects[:0]
	for _, r := range rects {
		r.x = x0 + 3 + r.x
		r.y = y0 + 2 + r.y
		s.rects = append(s.rects, r)
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
