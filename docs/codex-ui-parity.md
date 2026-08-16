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
| `BranchBacktrackToUserMessage` | `app_backtrack.rs` | full branch-style backtrack: new session, original preserved |
| `/statusline` interactive | `status_line_setup.rs` | enter-to-toggle picker over full item list |
| `/keymap` extended | `keymap.rs` | scroll/page/clear/new/palette/copy/paste/help/permission actions remappable |
| `overlaySessions` filter | `resume_picker.rs` | filterable session list + start-new entry |
| `codex.go` exec cells | `exec_cell/render.rs` | `• Running/Ran/Called` headers, `│` command continuations, `└` output blocks with `… +N lines (ctrl + t to view transcript)` |
| `codex.go` separators | `history_cell/separators.rs` | dim turn rules and `─ Worked for 42s ─` labels (>= 1 minute) |
| `renderUser` / `renderAssistant` | `history_cell/messages.rs` | user messages: terminal-tinted background, bold-dim `› ` prefix, 2-col continuation gutter; assistant messages: `• ` prefix on the first markdown line with `  ` continuations, no boxes |
| `markdown.go` style | `markdown_render.rs` | h1 bold+underlined, h2 bold, h3 bold+italic, h4-6 italic, inline code cyan, links cyan+underlined, blockquotes green `> `, ordered markers light blue, borderless highlighted code blocks |
| transcript overlay / export | `ExecCell::transcript_lines` | `$ cmd` + raw output + `✓/✗ (code) • duration` transcript form |

## Conversation display

The main chat viewport now mirrors Codex's history cells:

- **User messages** carry a subtle background tint (white 12% over the
  terminal background, matching `user_message_style`), a bold-dim `› ` prefix
  on the first line, and a 2-column continuation gutter. No decorative border.
- **Assistant messages** render as box-free rich markdown with a dim `• `
  bullet on the first line and `  ` continuation prefixes on every subsequent
  line, including blank rows and code blocks.
- **Tool calls** render as exec cells: `• Running cargo build` while live,
  then `• Ran go test ./...` / `• Called search(...)` / `• You ran ls` with
  green/red bullets by result, `│` command continuations, `└` output blocks
  capped at 5 rows (50 for user shell), and a `… +N lines (ctrl + t to view
  transcript)` hint when output is truncated.
- **Turn separators** emit a plain dim rule after turns that performed tool
  work, labeled `─ Worked for 2m 05s ─` when the turn ran for at least a
  minute.
- The **transcript overlay** and markdown export use Codex's `$ cmd` form with
  a `✓ • 1.2s` / `✗ (2) • 50ms` status row.

## Not yet ported

- `/keymap` full action coverage (remaining actions are viewer-only)
- `/statusline` enterprise/cloud items (five-hour/weekly limits, credits, workspace headline)
- Skills/multi-agent/collaboration/enterprise screens render as “not available” shells
- Resume picker dense/archive views and sort/filter tabs
- Approval overlay visual fidelity beyond existing permission modal

These are tracked as the next parity milestones.
