package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kevenhu001-cyber/astra-harness/internal/auth"
)

// loginState enumerates the visible phases of the device flow overlay.
type loginState int

const (
	loginStatePending  loginState = iota // waiting for browser approval
	loginStateSuccess                    // approved, displaying success
	loginStateError                      // error from the auth server
	loginStateExpired                    // device code expired (10min)
)

// loginOverlay is the dedicated TUI panel shown while a device flow is
// in progress. It surfaces the verification URI, the user code, and a
// live countdown so the operator never has to scroll the chat log to
// find them. Keyboard: o = open browser, c = copy URI, r = retry, esc
// = cancel.
type loginOverlay struct {
	state      loginState
	flow       *auth.DeviceFlow
	deadline   time.Time
	email      string // populated on success
	credSaved  bool   // whether the new credential was already saved
	errText    string
	existed    bool // true when the user was already signed in (no flow started)
	prevEmail  string
	closeAfter time.Time // success state auto-closes at this time
}

// loginTickMsg is fired by tea.Tick to refresh the countdown.
type loginTickMsg time.Time

// loginAutoCloseMsg hides the success overlay after 2s.
type loginAutoCloseMsg struct{}

// newLoginOverlayPending builds the overlay for a fresh device flow.
func newLoginOverlayPending(flow *auth.DeviceFlow) *loginOverlay {
	return &loginOverlay{
		state:    loginStatePending,
		flow:     flow,
		deadline: time.Now().Add(time.Duration(flow.ExpiresIn) * time.Second),
	}
}

// newLoginOverlayAlreadySignedIn is shown when /login runs while the
// user already has a credential. Re-auth runs after pressing 'r'.
func newLoginOverlayAlreadySignedIn(email string) *loginOverlay {
	return &loginOverlay{
		state:     loginStatePending,
		existed:   true,
		prevEmail: email,
	}
}

// markSuccess is called by the app when loginDoneMsg carries a credential.
func (o *loginOverlay) markSuccess(email string) {
	o.state = loginStateSuccess
	o.email = email
	o.credSaved = true
	o.errText = ""
	o.closeAfter = time.Now().Add(2 * time.Second)
}

// markError records an error and lets the user retry or cancel.
func (o *loginOverlay) markError(msg string) {
	o.state = loginStateError
	o.errText = msg
}

// markExpired flips the state to expired and surfaces a retry hint.
func (o *loginOverlay) markExpired() {
	o.state = loginStateExpired
	o.errText = "Authorization expired. Press r to request a new code."
}

// open reissues the verification URI in the system browser.
func (o *loginOverlay) open() {
	if o.flow != nil {
		_ = auth.OpenBrowser(o.flow.VerificationURI)
	}
}

// copyURI prints the verification URI to the chat so the user can copy
// it from the terminal even when no GUI browser is available. We use
// the chat feed rather than a system clipboard call so the dependency
// footprint stays minimal.
func (o *loginOverlay) copyURI() string {
	if o.flow == nil {
		return ""
	}
	return o.flow.VerificationURI
}

// update handles key events while the overlay is visible. Returns the
// action to take: "open", "copy", "retry", "cancel", "" (unhandled).
func (o *loginOverlay) update(msg tea.KeyMsg) string {
	switch o.state {
	case loginStateSuccess:
		if msg.String() == "esc" || msg.String() == "enter" {
			return "cancel"
		}
		return ""
	case loginStateError, loginStateExpired, loginStatePending:
		switch msg.String() {
		case "esc", "ctrl+c":
			return "cancel"
		case "o":
			o.open()
			return "open"
		case "c":
			return "copy"
		case "r":
			return "retry"
		}
	}
	return ""
}

// view renders the overlay body. The app centers it in the viewport.
func (o *loginOverlay) view() string {
	switch o.state {
	case loginStateSuccess:
		return o.viewSuccess()
	case loginStateError:
		return o.viewError()
	case loginStateExpired:
		return o.viewExpired()
	}
	return o.viewPending()
}

func (o *loginOverlay) viewPending() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render(" Sign in to Astra "))
	b.WriteString("\n\n")
	if o.existed {
		b.WriteString(styleBody.Render("Already signed in as "))
		b.WriteString(styleEmph.Render(o.prevEmail))
		b.WriteString(styleBody.Render("."))
		b.WriteString("\n\n")
		b.WriteString(styleDim.Render("Press "))
		b.WriteString(styleKey.Render("r"))
		b.WriteString(styleDim.Render(" to re-authenticate (clears the local credential)."))
		b.WriteString("\n")
		b.WriteString(styleDim.Render("Press "))
		b.WriteString(styleKey.Render("esc"))
		b.WriteString(styleDim.Render(" to close."))
		return b.String()
	}
	if o.flow == nil {
		b.WriteString(styleDim.Render("Requesting device code..."))
		return b.String()
	}
	remaining := time.Until(o.deadline)
	if remaining < 0 {
		remaining = 0
	}
	mins := int(remaining.Minutes())
	secs := int(remaining.Seconds()) % 60
	b.WriteString(styleDim.Render("Open this URL in your browser:"))
	b.WriteString("\n")
	b.WriteString(styleValue.Render("  " + o.flow.VerificationURI))
	b.WriteString("\n\n")
	b.WriteString(styleDim.Render("Code:"))
	b.WriteString("  ")
	b.WriteString(lipgloss.NewStyle().Foreground(activePalette().Magenta).Bold(true).Render(o.flow.UserCode))
	b.WriteString("\n\n")
	b.WriteString(styleDim.Render(fmt.Sprintf("Expires in  %02d:%02d", mins, secs)))
	b.WriteString("    ")
	b.WriteString(styleKey.Render("[o]"))
	b.WriteString(styleDim.Render(" open  "))
	b.WriteString(styleKey.Render("[c]"))
	b.WriteString(styleDim.Render(" copy  "))
	b.WriteString(styleKey.Render("[r]"))
	b.WriteString(styleDim.Render(" retry  "))
	b.WriteString(styleKey.Render("[esc]"))
	b.WriteString(styleDim.Render(" cancel"))
	return b.String()
}

func (o *loginOverlay) viewSuccess() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render(" Signed in "))
	b.WriteString("\n\n")
	b.WriteString(styleOk.Render("  ok "))
	b.WriteString("  ")
	b.WriteString(styleEmph.Render(o.email))
	b.WriteString("\n\n")
	b.WriteString(styleDim.Render("This panel closes in a moment. The status bar now shows your email."))
	return b.String()
}

func (o *loginOverlay) viewError() string {
	var b strings.Builder
	b.WriteString(styleError.Render(" Sign-in failed "))
	b.WriteString("\n\n")
	b.WriteString(styleBody.Render(o.errText))
	b.WriteString("\n\n")
	b.WriteString(styleDim.Render("Press "))
	b.WriteString(styleKey.Render("r"))
	b.WriteString(styleDim.Render(" to retry, "))
	b.WriteString(styleKey.Render("esc"))
	b.WriteString(styleDim.Render(" to cancel."))
	return b.String()
}

func (o *loginOverlay) viewExpired() string {
	var b strings.Builder
	b.WriteString(styleWarn.Render(" Authorization expired "))
	b.WriteString("\n\n")
	b.WriteString(styleBody.Render("The 10-minute device code window elapsed before approval."))
	b.WriteString("\n\n")
	b.WriteString(styleDim.Render("Press "))
	b.WriteString(styleKey.Render("r"))
	b.WriteString(styleDim.Render(" to request a new code, "))
	b.WriteString(styleKey.Render("esc"))
	b.WriteString(styleDim.Render(" to cancel."))
	return b.String()
}

// renderLoginOverlay centers the login panel in the viewport, matching the
// simple-overlay presentation used elsewhere in the TUI.
func renderLoginOverlay(o *loginOverlay, width, height int) string {
	maxW := width - 8
	if maxW > 120 {
		maxW = 120
	}
	box := styleOverlay.Width(maxW).Render(o.view())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// loginOverlayTick is the tea.Cmd that emits loginTickMsg each second.
func loginOverlayTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return loginTickMsg(t) })
}

// loginAutoClose is the tea.Cmd that hides the success overlay after 2s.
func loginAutoClose() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return loginAutoCloseMsg{} })
}
