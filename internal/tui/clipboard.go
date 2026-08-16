package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true,
	".gif": true, ".webp": true, ".bmp": true,
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// looksLikeImagePath reports whether the pasted text is a single local image
// file path (Codex-compatible path attachment).
func looksLikeImagePath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, "\r\n") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(s))
	return imageExts[ext] && fileExists(s)
}

// pasteClipboardImage saves the clipboard image (if any) to a temp file and
// returns its path.
func pasteClipboardImage() (string, error) {
	switch runtime.GOOS {
	case "windows":
		return pasteWindowsImage()
	case "darwin":
		return pasteMacImage()
	default:
		return pasteLinuxImage()
	}
}

func pasteWindowsImage() (string, error) {
	f, err := os.CreateTemp("", "astra-clip-*.png")
	if err != nil {
		return "", err
	}
	name := f.Name()
	f.Close()
	escaped := strings.ReplaceAll(name, "'", "''")
	script := "Add-Type -AssemblyName System.Windows.Forms; " +
		"$img=[System.Windows.Forms.Clipboard]::GetImage(); " +
		"if($img -eq $null){exit 2}; " +
		fmt.Sprintf("$img.Save('%s');", escaped)
	if out, err := exec.Command("powershell", "-NoProfile", "-Command", script).CombinedOutput(); err != nil {
		_ = os.Remove(name)
		if len(out) > 0 {
			return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
		}
		return "", err
	}
	if !fileExists(name) {
		return "", fmt.Errorf("clipboard contains no image")
	}
	return name, nil
}

func pasteMacImage() (string, error) {
	f, err := os.CreateTemp("", "astra-clip-*.png")
	if err != nil {
		return "", err
	}
	name := f.Name()
	f.Close()
	if out, err := exec.Command("pngpaste", name).CombinedOutput(); err != nil {
		_ = os.Remove(name)
		if len(out) > 0 {
			return "", fmt.Errorf("pngpaste: %s", strings.TrimSpace(string(out)))
		}
		return "", fmt.Errorf("pngpaste not available; install it or paste an image path: %w", err)
	}
	if !fileExists(name) {
		return "", fmt.Errorf("clipboard contains no image")
	}
	return name, nil
}

func pasteLinuxImage() (string, error) {
	f, err := os.CreateTemp("", "astra-clip-*.png")
	if err != nil {
		return "", err
	}
	name := f.Name()
	f.Close()
	if out, err := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o").Output(); err == nil && len(out) > 0 {
		if werr := os.WriteFile(name, out, 0o600); werr == nil {
			return name, nil
		}
	}
	if out, err := exec.Command("wl-paste", "--type", "image/png", "--no-newline").Output(); err == nil && len(out) > 0 {
		if werr := os.WriteFile(name, out, 0o600); werr == nil {
			return name, nil
		}
	}
	_ = os.Remove(name)
	return "", fmt.Errorf("no clipboard image found (need xclip or wl-paste)")
}

// readClipboardText returns the current clipboard text.
func readClipboardText() (string, error) {
	switch runtime.GOOS {
	case "windows":
		out, err := exec.Command("powershell", "-NoProfile", "-Command", "Get-Clipboard -Raw").Output()
		return strings.TrimSpace(string(out)), err
	case "darwin":
		out, err := exec.Command("pbpaste").Output()
		return strings.TrimSpace(string(out)), err
	default:
		if out, err := exec.Command("xclip", "-selection", "clipboard", "-o").Output(); err == nil {
			return strings.TrimSpace(string(out)), nil
		}
		out, err := exec.Command("wl-paste", "--no-newline").Output()
		return strings.TrimSpace(string(out)), err
	}
}
