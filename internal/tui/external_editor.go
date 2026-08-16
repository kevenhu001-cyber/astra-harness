package tui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// openExternalEditor writes draft to a temp file, opens the user's editor
// (VISUAL, then EDITOR, then code/vi/notepad), and returns the edited text.
func openExternalEditor(draft string) (string, error) {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		switch runtime.GOOS {
		case "windows":
			editor = "notepad"
		default:
			editor = "vi"
		}
	}
	f, err := os.CreateTemp("", "astra-draft-*.md")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err := f.WriteString(draft); err != nil {
		f.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	defer os.Remove(name)

	parts := strings.Fields(editor)
	if len(parts) == 0 {
		return "", fmt.Errorf("empty editor command")
	}
	cmd := exec.Command(parts[0], append(parts[1:], name)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
