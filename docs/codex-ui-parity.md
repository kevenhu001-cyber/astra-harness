# Codex UI parity

Astra's TUI is being ported 1:1 from the open-source Codex TUI
(`/tmp/codex-ref/codex-rs/tui`, commit `a95a6fe`), while keeping the
black/white/orange color scheme the user asked for.

## Source mapping

| Astra (Go) | Codex source | What is mirrored |
| --- | --- | --- |
| `composer.go` placeholder/prompt | `bottom_pane/chat_composer.rs`, `bottom_pane/textarea.rs` | `Ask Astra to do anything`, `> ` prompt |
| `app.go renderStatusBar` | `bottom_pane/footer.rs` | `? for shortcuts`, transient quit/Esc hints, right-side model/branch/usage context |
| `overlay.go overlayShortcuts` | `bottom_pane/footer.rs` (`SHORTCUTS`) | exact shortcut rows: `/`, `!`, `ctrl+j`, `tab`, `@`, `ctrl+v`, `ctrl+g`, `esc esc`, `ctrl+r`, `ctrl+c`, `ctrl+t`, `alt+,/alt+.` |
| `app.go handleKey` | `keymap.rs`, `bottom_pane/chat_composer.rs` | Ctrl+C double-press quit, Ctrl+D quit, Esc-Esc edit previous, Ctrl+T transcript, Ctrl+R history search |
| `theme.go "codex"` | `style.rs` + user requirement | black/white/orange palette, orange accent only |
| `overlayProviderConfig` + `/set-*` | `config.md` provider model | configure Provider, URL, API key, Model ID in-app and persist to `.astra/config.json` |

## Not yet ported

- External editor (`ctrl+g`) and image paste (`ctrl+v`)
- Full Esc backtrack transcript editing (currently maps to `/undo`)
- `/statusline` configurable footer row
- Model popup preset list fidelity
- Keymap customization (`/keymap`)

These are tracked as the next parity milestones.
