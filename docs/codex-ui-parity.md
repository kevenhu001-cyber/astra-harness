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
| `external_editor.go` + `Ctrl+G` | `external_editor.rs` | temp-file external editing via `VISUAL`/`EDITOR` |
| `clipboard.go` + `Ctrl+V` | `clipboard_paste.rs`, `chat_composer.rs` | clipboard image bytes (Windows/macOS/Linux) and image-path paste, `[Image #N]` rows |
| `app.go Backtrack` + `overlayBacktrack` | `app_backtrack.rs`, `pager_overlay.rs` | Esc opens transcript, select user message, truncate and re-edit (practical, no branch) |
| `statusLineSegments` + `/statusline` | `status_line_setup.rs`, `status_line_style.rs` | configurable footer items with Codex defaults and ids |
| `keymapCapture` + `/keymap` | `keymap.rs`, `keymap_setup` | capture-style remap of core actions, persisted in config |
| `overlayModels` filter | `model_popups.rs` | searchable model picker |
| `RunWithOptions` resume picker | `resume_picker.rs` | startup resume picker when saved sessions exist |

## Not yet ported

- Full Codex branch-style backtrack (currently truncate-and-edit)
- `/keymap` full action coverage (core actions only for now)
- `/statusline` every Codex item (Astra-computable subset is done)
- Skills/multi-agent/collaboration/enterprise screens render as “not available” shells
- Resume picker visual fidelity (list exists; dense/archive views not yet)

These are tracked as the next parity milestones.
