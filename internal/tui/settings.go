package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kevenhu001-cyber/astra-harness/internal/engine"
)

// providerSettings is the opencode-style provider configuration surface. It is
// a small state machine with three views that mirror opencode's
// connect-provider flow:
//
//   - pvPicker: a provider list (opencode's DialogProvider) — one row per
//     configured provider plus a trailing "+ Custom provider" row.
//   - pvPrompt: a single-line prompt (opencode's DialogPrompt) used to collect
//     the API key for a known provider, or the provider id → API key → base
//     URL → model sequence when connecting a custom provider.
//   - pvEditor: an advanced Edit form (ID · Name · Type · Base URL · API Key ·
//     repeatable Models rows) reachable with `e` from the picker.
//
// The whole surface is keyboard- and mouse-driven.
type providerSettings struct {
	eng    *engine.Engine
	view   providerView
	notice string
	err    string
	rects  []settingsRect

	pickSel int // selected row in the picker

	pr providerPrompt // active prompt (pvPrompt)
	ed providerEditor // active editor (pvEditor)
}

type providerView int

const (
	pvPicker providerView = iota
	pvPrompt
	pvEditor
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

// providerEditor edits a single provider (existing or new). It works on an
// in-memory copy (ed.prov) and only persists when the user saves.
type providerEditor struct {
	prov     engine.ProviderConfig
	isNew    bool
	isCustom bool // user-authored provider: ID + Type + Models are editable
	active   bool // this provider is the engine's active provider

	fields   []fieldDef
	fieldIdx int
	zone     provZone

	models    []string // editable model list
	defModel  string   // model marked as default for this provider
	modelSel  int
	editing   bool // editing a model row's text
	modelEdit textinput.Model

	btnSel int
}

type fieldDef struct {
	kind fieldKind
	ti   textinput.Model
}

type fieldKind int

const (
	fkID fieldKind = iota
	fkName
	fkType
	fkBaseURL
	fkAPIKey
)

type provZone int

const (
	zoneFields provZone = iota
	zoneModels
	zoneButtons
)

// promptKind identifies which single-line prompt (opencode DialogPrompt) is
// currently shown in pvPrompt.
type promptKind int

const (
	promptAPIKey promptKind = iota
	promptProviderID
	promptBaseURL
	promptModel
)

// providerPrompt is a single opencode-style prompt: one focused text input
// with a title, hint and a single action. Known providers go straight to the
// API key prompt; the custom flow walks Provider id → API key → Base URL →
// Model before the provider is created.
type providerPrompt struct {
	kind     promptKind
	prov     engine.ProviderConfig // provider being connected
	isCustom bool                  // true during the custom-provider flow
	ti       textinput.Model
}

// closeActionOpenModels is the sentinel closing message returned when a
// provider was connected and the app should open the model picker next
// (opencode: ApiMethod → DialogModel).
const closeActionOpenModels = "⚙open-models"

// customProviderIDRe mirrors opencode's CUSTOM_PROVIDER_ID: ids start with a
// lowercase letter or number and only use lowercase letters, numbers, hyphens
// and underscores.
var customProviderIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-_]*$`)

func validProviderID(id string) bool {
	return customProviderIDRe.MatchString(id)
}

func firstModelOf(p engine.ProviderConfig) string {
	if len(p.Models) > 0 {
		return p.Models[0]
	}
	return ""
}

// providerButtons returns the action row for the editor. A new custom provider
// only offers Create / Cancel; an existing provider offers Save & Use / Save /
// Delete / Cancel.
func providerButtons(isNew bool) []settingsButton {
	if isNew {
		return []settingsButton{
			{label: "Create", action: "create"},
			{label: "Cancel", action: "cancel"},
		}
	}
	return []settingsButton{
		{label: "Save & Use", action: "save-use"},
		{label: "Save", action: "save"},
		{label: "Delete", action: "delete"},
		{label: "Cancel", action: "cancel"},
	}
}

func newProviderSettings(a *app) *providerSettings {
	s := &providerSettings{eng: a.engine, view: pvPicker}
	for i, p := range a.engine.Config.Providers {
		if p.ID == a.engine.ProviderID() {
			s.pickSel = i
			break
		}
	}
	return s
}

func newProviderEditor(a *app, prov engine.ProviderConfig, isNew bool) providerEditor {
	ed := providerEditor{
		prov:     prov,
		isNew:    isNew,
		isCustom: isNew || strings.HasPrefix(prov.ID, "custom"),
		active:   prov.ID == a.engine.ProviderID(),
	}
	if ed.isCustom {
		ed.fields = append(ed.fields, mkField(fkID, prov.ID))
	}
	ed.fields = append(ed.fields, mkField(fkName, prov.Name))
	if ed.isCustom {
		ed.fields = append(ed.fields, mkField(fkType, prov.Type))
	}
	ed.fields = append(ed.fields, mkField(fkBaseURL, prov.BaseURL))
	ed.fields = append(ed.fields, mkField(fkAPIKey, ""))
	for i := range ed.fields {
		if ed.fields[i].kind == fkAPIKey {
			ed.fields[i].ti.EchoMode = textinput.EchoPassword
			ed.fields[i].ti.EchoCharacter = '•'
			ed.fields[i].ti.Placeholder = keyHint(prov)
		}
	}
	ed.models = append(ed.models, prov.Models...)
	if len(ed.models) == 0 {
		ed.models = append(ed.models, "")
	}
	ed.defModel = defaultModelFor(a, prov)
	ed.modelEdit = textinput.New()
	ed.modelEdit.Prompt = ""
	ed.modelEdit.Width = 40
	return ed
}

func mkField(kind fieldKind, val string) fieldDef {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetValue(val)
	return fieldDef{kind: kind, ti: ti}
}

// keyHint explains how the API key is currently provided.
func keyHint(p engine.ProviderConfig) string {
	if p.APIKey != "" {
		return "already set — type to replace"
	}
	if p.APIKeyEnv != "" {
		return "uses env " + p.APIKeyEnv + " — or type a key"
	}
	return "type an API key (saved to .astra/config.json)"
}

// defaultModelFor picks the default model for the edited provider: the global
// default when it belongs to this provider, otherwise the first model.
func defaultModelFor(a *app, p engine.ProviderConfig) string {
	for _, m := range p.Models {
		if m == a.engine.Config.DefaultModel {
			return m
		}
	}
	if len(p.Models) > 0 {
		return p.Models[0]
	}
	return ""
}

func providerConfigured(p engine.ProviderConfig) bool {
	return p.APIKey != "" || p.APIKeyEnv != ""
}

func (ed *providerEditor) fieldVal(kind fieldKind) string {
	for _, f := range ed.fields {
		if f.kind == kind {
			return strings.TrimSpace(f.ti.Value())
		}
	}
	return ""
}

func (ed *providerEditor) focusField() {
	for i := range ed.fields {
		if i == ed.fieldIdx {
			ed.fields[i].ti.Focus()
		} else {
			ed.fields[i].ti.Blur()
		}
	}
}

func (ed *providerEditor) blurFields() {
	for i := range ed.fields {
		ed.fields[i].ti.Blur()
	}
}

func (ed *providerEditor) cycleType() {
	if ed.prov.Type == "anthropic" {
		ed.prov.Type = "openai-compatible"
	} else {
		ed.prov.Type = "anthropic"
	}
	for i := range ed.fields {
		if ed.fields[i].kind == fkType {
			ed.fields[i].ti.SetValue(ed.prov.Type)
		}
	}
}

// update routes one message through the surface. Returns (done, closeMsg, cmd).
func (s *providerSettings) update(msg tea.Msg, a *app) (bool, string, tea.Cmd) {
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

func (s *providerSettings) handleKey(msg tea.KeyMsg, a *app) (bool, string, tea.Cmd) {
	k := msg.String()
	if s.view == pvPicker {
		return s.handlePickerKey(k, a)
	}
	if s.view == pvPrompt {
		return s.handlePromptKey(msg, a)
	}
	return s.handleEditorKey(msg, a)
}

// handlePickerKey drives the Provider Picker list. Enter / space connects a
// provider (opencode: select → API key prompt), `e` opens the advanced editor,
// and `a` starts the custom-provider flow.
func (s *providerSettings) handlePickerKey(k string, a *app) (bool, string, tea.Cmd) {
	count := len(s.eng.Config.Providers) + 1 // +1 for the custom row
	switch k {
	case "esc":
		return true, "", nil
	case "down", "j":
		s.pickSel = (s.pickSel + 1) % count
	case "up", "k":
		s.pickSel = (s.pickSel - 1 + count) % count
	case "enter", " ", "l":
		if s.pickSel < len(s.eng.Config.Providers) {
			return false, "", s.startConnect(a, s.eng.Config.Providers[s.pickSel])
		}
		return false, "", s.startCustom(a)
	case "a":
		return false, "", s.startCustom(a)
	case "e":
		if s.pickSel < len(s.eng.Config.Providers) {
			s.openEditor(a, s.eng.Config.Providers[s.pickSel], false)
		} else {
			s.openCustom(a)
		}
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		idx := int(k[0] - '1')
		if idx < len(s.eng.Config.Providers) {
			s.pickSel = idx
			return false, "", s.startConnect(a, s.eng.Config.Providers[idx])
		}
	}
	return false, "", nil
}

func (s *providerSettings) openEditor(a *app, prov engine.ProviderConfig, isNew bool) {
	s.ed = newProviderEditor(a, prov, isNew)
	s.view = pvEditor
	s.ed.zone = zoneFields
	s.ed.fieldIdx = 0
	s.ed.focusField()
	s.err = ""
	s.notice = ""
}

func (s *providerSettings) openCustom(a *app) {
	prov := engine.ProviderConfig{
		ID:      "",
		Type:    "openai-compatible",
		Name:    "",
		BaseURL: "",
		Models:  []string{""},
	}
	s.openEditor(a, prov, true)
}

func (s *providerSettings) backToPicker(a *app) {
	s.view = pvPicker
	s.ed = providerEditor{}
	s.pr = providerPrompt{}
	s.err = ""
}

// handleEditorKey drives the Connect / Edit / Custom form.
func (s *providerSettings) handleEditorKey(m tea.KeyMsg, a *app) (bool, string, tea.Cmd) {
	k := m.String()
	ed := &s.ed
	switch ed.zone {
	case zoneFields:
		switch k {
		case "esc":
			s.backToPicker(a)
			return false, "", nil
		case "tab", "down", "enter":
			if ed.curField().kind == fkType && k == "enter" {
				ed.cycleType()
				return false, "", nil
			}
			ed.fieldIdx++
			if ed.fieldIdx >= len(ed.fields) {
				ed.zone = zoneModels
				ed.modelSel = clamp(ed.modelSel, 0, len(ed.models)-1)
				ed.blurFields()
			} else {
				ed.focusField()
				return false, "", ed.fields[ed.fieldIdx].ti.Focus()
			}
		case "shift+tab", "up":
			ed.fieldIdx--
			if ed.fieldIdx < 0 {
				ed.zone = zoneButtons
				ed.btnSel = len(providerButtons(ed.isNew)) - 1
				ed.blurFields()
			} else {
				ed.focusField()
				return false, "", ed.fields[ed.fieldIdx].ti.Focus()
			}
		case "left", "right":
			if ed.curField().kind == fkType {
				ed.cycleType()
				return false, "", nil
			}
		default:
			if ed.curField().kind != fkType {
				ed.fields[ed.fieldIdx].ti, _ = ed.fields[ed.fieldIdx].ti.Update(m)
			}
		}
	case zoneModels:
		if ed.editing {
			switch k {
			case "esc":
				ed.editing = false
				ed.modelEdit.Blur()
			case "enter":
				ed.commitModelEdit()
				ed.editing = false
				ed.modelEdit.Blur()
			default:
				ed.modelEdit, _ = ed.modelEdit.Update(m)
			}
			return false, "", nil
		}
		switch k {
		case "esc":
			s.backToPicker(a)
			return false, "", nil
		case "up", "k":
			ed.modelSel = (ed.modelSel - 1 + len(ed.models)) % len(ed.models)
		case "down", "j", "tab":
			ed.modelSel = (ed.modelSel + 1) % len(ed.models)
		case "enter", " ":
			ed.defModel = ed.models[ed.modelSel]
		case "e":
			ed.modelEdit.SetValue(ed.models[ed.modelSel])
			ed.modelEdit.CursorEnd()
			ed.modelEdit.Focus()
			ed.editing = true
		case "a":
			ed.models = append(ed.models, "")
			ed.modelSel = len(ed.models) - 1
			ed.modelEdit.SetValue("")
			ed.modelEdit.CursorEnd()
			ed.modelEdit.Focus()
			ed.editing = true
		case "d":
			if len(ed.models) > 1 {
				ed.models = append(ed.models[:ed.modelSel], ed.models[ed.modelSel+1:]...)
				ed.modelSel = clamp(ed.modelSel, 0, len(ed.models)-1)
				if ed.defModel != "" {
					found := false
					for _, m := range ed.models {
						if m == ed.defModel {
							found = true
							break
						}
					}
					if !found {
						ed.defModel = ed.models[0]
					}
				}
			}
		case "shift+tab":
			ed.zone = zoneFields
			ed.fieldIdx = len(ed.fields) - 1
			ed.focusField()
			return false, "", ed.fields[ed.fieldIdx].ti.Focus()
		}
	case zoneButtons:
		btns := providerButtons(ed.isNew)
		switch k {
		case "esc":
			s.backToPicker(a)
			return false, "", nil
		case "left", "shift+tab":
			ed.btnSel = (ed.btnSel - 1 + len(btns)) % len(btns)
		case "right", "tab":
			ed.btnSel = (ed.btnSel + 1) % len(btns)
		case "enter", " ":
			return s.runButton(btns[ed.btnSel].action, a)
		}
	}
	return false, "", nil
}

// openPrompt shows a single-line prompt (opencode DialogPrompt) and focuses
// its input. The input is focused and cursor-placed so typed text is visible
// immediately.
func (s *providerSettings) openPrompt(a *app, kind promptKind, prov engine.ProviderConfig, isCustom bool, placeholder string, masked bool, value string) tea.Cmd {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = placeholder
	ti.Width = 40
	if masked {
		ti.EchoMode = textinput.EchoPassword
		ti.EchoCharacter = '•'
	}
	ti.SetValue(value)
	ti.CursorEnd()
	cmd := ti.Focus()
	s.pr = providerPrompt{kind: kind, prov: prov, isCustom: isCustom, ti: ti}
	s.view = pvPrompt
	s.err = ""
	s.notice = ""
	return cmd
}

// startConnect begins the opencode connect flow for a known provider: prompt
// for the API key, then hand off to the model picker.
func (s *providerSettings) startConnect(a *app, p engine.ProviderConfig) tea.Cmd {
	s.err = ""
	return s.openPrompt(a, promptAPIKey, p, false, "API key", true, "")
}

// startCustom begins the opencode custom-provider flow: Provider id → API key
// → Base URL → Model.
func (s *providerSettings) startCustom(a *app) tea.Cmd {
	prov := engine.ProviderConfig{ID: "", Type: "openai-compatible"}
	s.err = ""
	return s.openPrompt(a, promptProviderID, prov, true, "Provider id", false, "")
}

// handlePromptKey drives the single-line connect prompts.
func (s *providerSettings) handlePromptKey(m tea.KeyMsg, a *app) (bool, string, tea.Cmd) {
	k := m.String()
	switch k {
	case "esc":
		s.pr = providerPrompt{}
		s.backToPicker(a)
		return false, "", nil
	case "enter":
		return s.submitPrompt(a)
	default:
		s.pr.ti, _ = s.pr.ti.Update(m)
		return false, "", nil
	}
}

// submitPrompt validates the current prompt and advances the flow.
func (s *providerSettings) submitPrompt(a *app) (bool, string, tea.Cmd) {
	value := strings.TrimSpace(s.pr.ti.Value())
	switch s.pr.kind {
	case promptProviderID:
		// opencode rejects invalid ids with the same message and re-prompts.
		if !validProviderID(value) {
			s.err = "Provider ids must start with a lowercase letter or number and only use lowercase letters, numbers, hyphens, and underscores"
			return false, "", nil
		}
		prov := s.pr.prov
		prov.ID = value
		prov.Name = value
		return false, "", s.openPrompt(a, promptAPIKey, prov, true, "API key", true, "")
	case promptAPIKey:
		if value == "" {
			s.err = "API key is required"
			return false, "", nil
		}
		prov := s.pr.prov
		prov.APIKey = value
		if s.pr.isCustom {
			// Custom providers need a base URL (opencode leaves this to
			// opencode.json; Astra collects it inline so the provider works).
			return false, "", s.openPrompt(a, promptBaseURL, prov, true, "https://api.openai.com/v1", false, prov.BaseURL)
		}
		if err := a.engine.UpdateProvider(prov.ID, "", value, ""); err != nil {
			s.err = err.Error()
			return false, "", nil
		}
		// opencode: ApiMethod → DialogModel.
		return true, closeActionOpenModels + "|provider " + prov.ID + " api key saved — pick a model below", nil
	case promptBaseURL:
		prov := s.pr.prov
		if value != "" {
			prov.BaseURL = value
		}
		if prov.BaseURL == "" {
			prov.BaseURL = "https://api.openai.com/v1"
		}
		return false, "", s.openPrompt(a, promptModel, prov, true, "model id", false, firstModelOf(prov))
	case promptModel:
		if value == "" {
			s.err = "model id is required"
			return false, "", nil
		}
		prov := s.pr.prov
		cfg := engine.ProviderConfig{
			ID: prov.ID, Type: "openai-compatible", Name: prov.Name,
			BaseURL: prov.BaseURL, APIKey: prov.APIKey, Models: []string{value},
		}
		if err := a.engine.UpsertProvider(cfg); err != nil {
			s.err = err.Error()
			return false, "", nil
		}
		if err := a.engine.SwitchModel(prov.ID, value); err != nil {
			s.err = "saved, but could not activate: " + err.Error()
			return false, "", nil
		}
		return true, "provider " + prov.ID + " connected · now active (" + value + ")", nil
	}
	return false, "", nil
}

// curField returns the field currently focused in the editor.
func (ed *providerEditor) curField() fieldDef { return ed.fields[ed.fieldIdx] }

func (ed *providerEditor) commitModelEdit() {
	v := strings.TrimSpace(ed.modelEdit.Value())
	ed.models[ed.modelSel] = v
	if ed.defModel == "" && v != "" {
		ed.defModel = v
	}
}

func (s *providerSettings) runButton(action string, a *app) (bool, string, tea.Cmd) {
	switch action {
	case "save-use":
		return s.save(a, true)
	case "save":
		return s.save(a, false)
	case "create":
		return s.save(a, true)
	case "delete":
		id := s.ed.prov.ID
		if err := a.engine.DeleteProvider(id); err != nil {
			s.err = err.Error()
			return false, "", nil
		}
		s.notice = "provider " + id + " deleted"
		s.backToPicker(a)
		return false, "", nil
	case "cancel":
		s.backToPicker(a)
		return false, "", nil
	}
	return false, "", nil
}

// save persists the edited provider (and optionally activates it). Empty
// fields keep their current values so a masked key field is safe to leave
// untouched.
func (s *providerSettings) save(a *app, use bool) (bool, string, tea.Cmd) {
	ed := &s.ed
	id := ed.prov.ID
	if ed.isNew {
		id = ed.fieldVal(fkID)
	}
	if id == "" {
		s.err = "provider id is required"
		return false, "", nil
	}
	name := ed.fieldVal(fkName)
	if name == "" {
		name = id
	}
	baseURL := ed.fieldVal(fkBaseURL)
	apiKey := ed.fieldVal(fkAPIKey)
	models := nonempty(ed.models)
	def := ed.defModel
	if def == "" && len(models) > 0 {
		def = models[0]
	}

	cfg := engine.ProviderConfig{
		ID:        id,
		Type:      ed.prov.Type,
		Name:      name,
		BaseURL:   baseURL,
		APIKey:    apiKey, // empty => engine keeps the stored key
		APIKeyEnv: ed.prov.APIKeyEnv,
		Models:    models,
	}
	if err := a.engine.UpsertProvider(cfg); err != nil {
		s.err = err.Error()
		return false, "", nil
	}
	if use {
		if err := a.engine.SwitchModel(id, def); err != nil {
			s.err = "saved, but could not activate: " + err.Error()
			return false, "", nil
		}
	}
	msg := "provider " + id + " saved"
	if apiKey != "" {
		msg += " · api key saved"
	}
	if baseURL != "" {
		msg += " · url=" + baseURL
	}
	if use {
		msg += " · now active (" + def + ")"
	}
	return true, msg, nil
}

func nonempty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// activate routes a click/select action.
func (s *providerSettings) activate(action string, a *app) (bool, string, tea.Cmd) {
	switch {
	case strings.HasPrefix(action, "pick:"):
		if action == "pick:custom" {
			return false, "", s.startCustom(a)
		}
		var idx int
		fmt.Sscanf(action, "pick:%d", &idx)
		if idx >= 0 && idx < len(s.eng.Config.Providers) {
			s.pickSel = idx
			return false, "", s.startConnect(a, s.eng.Config.Providers[idx])
		}
	case action == "prompt":
		return false, "", s.pr.ti.Focus()
	case strings.HasPrefix(action, "field:"):
		var idx int
		fmt.Sscanf(action, "field:%d", &idx)
		if idx >= 0 && idx < len(s.ed.fields) {
			s.ed.zone = zoneFields
			s.ed.fieldIdx = idx
			s.ed.focusField()
			return false, "", s.ed.fields[idx].ti.Focus()
		}
	case strings.HasPrefix(action, "model:"):
		var idx int
		fmt.Sscanf(action, "model:%d", &idx)
		if idx >= 0 && idx < len(s.ed.models) {
			s.ed.zone = zoneModels
			s.ed.modelSel = idx
		}
	case strings.HasPrefix(action, "btn:"):
		return s.runButton(strings.TrimPrefix(action, "btn:"), a)
	}
	return false, "", nil
}

// View renders the centered card (picker or editor) and refreshes click rects.
func (s *providerSettings) View(width, height int) string {
	pal := activePalette()
	boxW := width - 8
	if boxW > 96 {
		boxW = 96
	}
	if boxW < 50 {
		boxW = 50
	}
	contentW := boxW - 6

	var lines []string
	var rects []settingsRect

	if s.view == pvPicker {
		lines, rects = s.viewPicker(contentW, pal)
	} else if s.view == pvPrompt {
		lines, rects = s.viewPrompt(contentW, pal)
	} else {
		lines, rects = s.viewEditor(contentW, pal)
	}

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
	s.rects = s.rects[:0]
	for _, r := range rects {
		r.x = x0 + 3 + r.x
		r.y = y0 + 2 + r.y
		s.rects = append(s.rects, r)
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// viewPicker renders the Provider Picker list (opencode DialogProvider): one
// row per configured provider plus a trailing custom-provider row.
func (s *providerSettings) viewPicker(contentW int, pal themePalette) ([]string, []settingsRect) {
	var lines []string
	var rects []settingsRect
	lines = append(lines, styleTitle.Render("◆ Connect a provider")+"  "+styleDim.Render("pick a provider to connect · e edit · a add custom"))
	lines = append(lines, "")

	row := len(lines)
	count := len(s.eng.Config.Providers)
	for i, p := range s.eng.Config.Providers {
		selected := i == s.pickSel
		active := p.ID == s.eng.ProviderID()
		conf := providerConfigured(p)
		lines, rects = renderProviderRow(lines, rects, i, row, contentW, selected, active, conf, p.Name, p.ID, pal)
		row = len(lines)
	}
	selected := s.pickSel == count
	lines, rects = renderCustomRow(lines, rects, row, contentW, selected, pal)
	lines = append(lines, "")
	lines = append(lines, styleDim.Render("↑↓ choose · ⏎ connect · e advanced · a add custom · 1-9 jump · esc close"))
	return lines, rects
}

func renderProviderRow(lines []string, rects []settingsRect, idx, row, width int, selected, active, conf bool, name, id string, pal themePalette) ([]string, []settingsRect) {
	if name == "" {
		name = id
	}
	statusDot := "○"
	statusText := "not configured"
	if active {
		statusDot = "●"
		statusText = "active"
	} else if conf {
		statusDot = "●"
		statusText = "configured"
	}
	var line string
	if selected {
		line = "› " + styleTitle.Render(name) + "  " + styleDim.Render("("+id+")") + "  " + statusLine(statusDot, statusText, active, pal)
		line = lipgloss.NewStyle().Background(pal.Bg2).Width(width).Render(line)
	} else {
		line = "  " + styleBody.Render(name) + "  " + styleDim.Render("("+id+")") + "  " + statusLine(statusDot, statusText, active, pal)
		line = lipgloss.NewStyle().Width(width).Render(line)
	}
	rects = append(rects, settingsRect{x: 0, y: row, w: width, h: 1, action: fmt.Sprintf("pick:%d", idx)})
	lines = append(lines, line)
	return lines, rects
}

func statusLine(dot, text string, active bool, pal themePalette) string {
	c := pal.WhiteDim
	if active {
		c = pal.Accent
	} else if text == "configured" {
		c = pal.Green
	}
	return lipgloss.NewStyle().Foreground(c).Render(dot + " " + text)
}

func renderCustomRow(lines []string, rects []settingsRect, row, width int, selected bool, pal themePalette) ([]string, []settingsRect) {
	var line string
	if selected {
		line = lipgloss.NewStyle().Background(pal.Bg2).Width(width).Render("› " + styleTitle.Render("+ Custom provider"))
	} else {
		line = lipgloss.NewStyle().Width(width).Render("  " + styleBody.Render("+ Custom provider"))
	}
	rects = append(rects, settingsRect{x: 0, y: row, w: width, h: 1, action: "pick:custom"})
	lines = append(lines, line)
	return lines, rects
}

// viewPrompt renders the opencode-style single-line connect prompt.
func (s *providerSettings) viewPrompt(contentW int, pal themePalette) ([]string, []settingsRect) {
	pr := &s.pr
	var lines []string
	var rects []settingsRect

	title := pr.prov.Name
	if title == "" {
		title = pr.prov.ID
	}
	if pr.kind == promptProviderID {
		title = "Other" // opencode labels the custom flow "Other"
	}
	lines = append(lines, styleTitle.Render("◆ "+title)+"  "+styleDim.Render(pr.prov.ID))
	lines = append(lines, "")

	labels := map[promptKind]string{
		promptProviderID: "Provider ID",
		promptAPIKey:     "API key",
		promptBaseURL:    "Base URL",
		promptModel:      "Model",
	}
	hints := map[promptKind]string{
		promptProviderID: "lowercase letters, numbers, hyphens and underscores",
		promptAPIKey:     keyHint(pr.prov),
		promptBaseURL:    "leave empty to use the default endpoint",
		promptModel:      "the model id this provider will use",
	}
	inputW := contentW - 16
	if inputW < 16 {
		inputW = 16
	}
	pr.ti.Width = inputW
	box := lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, true, false, true).BorderForeground(pal.Accent).Padding(0, 1)
	lines = append(lines, styleDim.Render("  "+padRight(labels[pr.kind], 11))+" "+box.Render(pr.ti.View()))
	rects = append(rects, settingsRect{x: 13, y: len(lines) - 1, w: contentW - 15, h: 1, action: "prompt"})
	lines = append(lines, styleDim.Render("  "+hints[pr.kind]))
	lines = append(lines, "")
	if s.err != "" {
		lines = append(lines, styleError.Render("✗ "+s.err))
	}
	lines = append(lines, styleDim.Render("enter save · esc back"))
	return lines, rects
}

// viewEditor renders the Connect / Edit / Custom form.
func (s *providerSettings) viewEditor(contentW int, pal themePalette) ([]string, []settingsRect) {
	ed := &s.ed
	var lines []string
	var rects []settingsRect

	title := "◆ Connect Provider"
	if ed.isNew {
		title = "◆ New Provider"
	} else if ed.isCustom {
		title = "◆ Edit Provider"
	}
	lines = append(lines, styleTitle.Render(title)+"  "+styleDim.Render(ed.prov.ID))
	lines = append(lines, "")

	// Identity / connection fields.
	fieldLabels := map[fieldKind]string{
		fkID: "Provider ID", fkName: "Name", fkType: "Type",
		fkBaseURL: "Base URL", fkAPIKey: "API Key",
	}
	inputW := contentW - 16
	if inputW < 16 {
		inputW = 16
	}
	for i := range ed.fields {
		ed.fields[i].ti.Width = inputW
	}
	ed.modelEdit.Width = inputW
	for i, f := range ed.fields {
		focused := ed.zone == zoneFields && ed.fieldIdx == i
		label := fieldLabels[f.kind]
		prefix := "  "
		if focused {
			prefix = "› "
		}
		lab := styleDim.Render(prefix + padRight(label, 11))
		box := lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, true, false, true).Padding(0, 1)
		if focused {
			box = box.BorderForeground(pal.Accent)
		} else {
			box = box.BorderForeground(pal.GrayLo)
		}
		value := f.ti.View()
		if f.kind == fkType {
			value = value + styleDim.Render("  ◀▶ toggle")
		}
		lines = append(lines, lab+" "+box.Render(value))
		rects = append(rects, settingsRect{x: 13, y: len(lines) - 1, w: contentW - 15, h: 1, action: fmt.Sprintf("field:%d", i)})
	}
	lines = append(lines, "")

	// Models editor.
	lines = append(lines, styleDim.Render("Models"))
	for i, m := range ed.models {
		sel := ed.zone == zoneModels && ed.modelSel == i
		defMark := "  "
		if m == ed.defModel {
			defMark = "◆ "
		}
		prefix := "  "
		if sel {
			prefix = "› "
		}
		display := m
		if ed.editing && sel {
			display = ed.modelEdit.View()
		}
		if display == "" {
			display = styleDim.Render("(empty — type a model id)")
		}
		lab := styleDim.Render(prefix + defMark)
		box := lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, true, false, true).Padding(0, 1)
		if sel {
			box = box.BorderForeground(pal.Accent)
			if ed.editing {
				box = box.BorderForeground(pal.Green)
			}
		} else {
			box = box.BorderForeground(pal.GrayLo)
		}
		lines = append(lines, lab+" "+box.Render(display))
		rects = append(rects, settingsRect{x: 5, y: len(lines) - 1, w: contentW - 7, h: 1, action: fmt.Sprintf("model:%d", i)})
	}
	lines = append(lines, styleDim.Render("  enter set default · e edit · a add · d delete"))
	lines = append(lines, "")

	// Action buttons.
	btns := providerButtons(ed.isNew)
	btnLine := ""
	btnX := 0
	row := len(lines)
	for i, b := range btns {
		chip := " " + b.label + " "
		var st lipgloss.Style
		if ed.zone == zoneButtons && ed.btnSel == i {
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

	if s.err != "" {
		lines = append(lines, styleError.Render("✗ "+s.err))
	} else if s.notice != "" {
		lines = append(lines, styleOk.Render("• "+s.notice))
	} else {
		lines = append(lines, styleDim.Render("changes saved to .astra/config.json · esc back to providers"))
	}
	return lines, rects
}
