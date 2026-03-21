# workbench — Agent Documentation Index

**workbench** is a terminal-based developer dashboard (TUI) written in Go. It aggregates GitHub notifications/PRs, email, and Jira tickets into a single configurable pane layout, all visible at a glance in one terminal window.

---

## How to Use This Documentation

This folder follows **Progressive Disclosure** principles — show what exists and its retrieval cost first, let the agent decide what to fetch based on relevance and need. Start here, then read only the detail files relevant to your task.

> **Maintaining these docs:** When you add, remove, or significantly change code in this repo, update the relevant file(s) below. Keep each file focused on its layer. If a detail file grows beyond ~200 lines, split it. Always update this index when adding new files.

| File | What it covers | Read when… |
|------|---------------|------------|
| **This file** | Repo overview, file map, key facts | Always — start here |
| [`architecture.md`](architecture.md) | Package map, data flow, startup sequence, the two-pass render loop | Changing the overall structure, adding a package, debugging the render loop |
| [`tui.md`](tui.md) | Bubbletea model, all UI modes, per-pane search, log overlay, keyboard shortcuts, styling | Changing keybindings, modes, search/filter, layout, or visual styling |
| [`providers.md`](providers.md) | The `Provider` interface contract, and deep dives on each plugin (applemail, github, jira) | Changing or adding a plugin |
| [`layout-engine.md`](layout-engine.md) | Weight-based layout math, lipgloss sizing quirks, the two-pass dims system | Debugging layout/sizing bugs, changing pane geometry |
| [`config.md`](config.md) | All config fields, defaults, file location, loading behaviour | Changing configuration options |
| [`plans/`](plans/) | Design plans for future or in-progress features | Starting a significant new feature |

---

## Repo at a Glance

```
workbench/
├── cmd/workbench/main.go        ← entry point, bubbletea model, all TUI logic
├── internal/
│   ├── plugin/plugin.go         ← Item, FetchRequest, FetchResponse, Provider interface
│   ├── plugin/run.go            ← RunPlugin helper for plugin binaries
│   ├── plugin/subprocess.go     ← SubprocessProvider (spawns binary, stdin/stdout JSON)
│   ├── cache/cache.go           ← atomic JSON cache (~/.cache/workbench/)
│   ├── layout/layout.go         ← weight-based pane layout engine
│   └── log/log.go               ← singleton structured logger + in-memory ring
├── plugins/
│   ├── applemail/main.go        ← Apple Mail plugin (macOS, via AppleScript)
│   ├── github/main.go           ← GitHub plugin (API or gh CLI fallback)
│   └── jira/main.go             ← Jira plugin (shells out to acli)
├── config.example.toml          ← annotated example config
├── script/rebuild-and-reinstall ← dev build script (builds + installs workbench + plugins)
└── agent_docs/                  ← this documentation tree
```

**Module:** `github.com/jluntpcty/workbench`  
**Go version:** 1.25.0  
**Key deps:** bubbletea v1.3.10, lipgloss v1.1.0, go-github v71, BurntSushi/toml v1.6.0

---

## Key Facts (no need to read further for these)

- **Config file:** `~/.config/workbench/config.toml` — resolved via `$XDG_CONFIG_HOME` → `$HOME/.config` (NOT `os.UserConfigDir()`, which returns the wrong path on macOS)
- **Cache files:** `~/.cache/workbench/<provider>.json` (one per provider; no TTL)
- **Log file:** `~/.local/share/workbench/workbench.log`
- **No CLI flags.** All configuration is through the TOML file.
- **macOS-only** in practice (relies on `gh`, `acli`, and optionally Apple Mail via AppleScript)
- **Alt-screen TUI** — takes over the full terminal, restores on exit (`tea.WithAltScreen()`)
- **Plugin discovery:** workbench scans `~/.config/workbench/plugins/` at startup; each executable file becomes a provider keyed by its filename
- **Plugin protocol:** one-shot subprocess — workbench spawns `<plugin> fetch`, writes JSON FetchRequest to stdin, reads JSON FetchResponse from stdout
- **Plugin config:** `[plugins.<name>]` in config.toml is passed as `FetchRequest.Config map[string]any`; plugins read whatever keys they need
- **Three bundled plugins:** `applemail`, `github`, `jira` — in `plugins/*/main.go`
- **No test files exist** as of initial writing
- **`Enter` key does nothing** on selected items — no open-in-browser action yet
- **`?` is global** — opens the help overlay from any mode, then restores the previous mode on close

---

## The Plugin Contract (one-liner)

```go
type Provider interface {
    Name() string
    Fetch(ctx context.Context) ([]Item, error)
}
```

Plugin binaries call `plugin.RunPlugin(func(cfg map[string]any) ([]plugin.Item, error) {...})` from their `main()`. The SDK handles all stdin/stdout JSON protocol mechanics.

`Name()` is the binary's filename — it must match the `provider` string in `config.toml` layout panes and the key in `[plugins.<name>]`.

---

## Search / Filter (per-pane)

Each pane has its own independent search/filter state. From normal mode, press `/` then:
- `s` → search: all items shown, matching items highlighted, `n`/`N` navigate
- `f` → filter: non-matching items hidden
- `F` → dim-filter: non-matching items dimmed

`Esc` clears the active pane's search. State persists when switching panes. The log overlay has its own identical search system plus `w`/`e`/`i` quick-filters.

---

## Plans

| Plan | Status | Summary |
|------|--------|---------|
| [`plans/email-integration-plan.md`](plans/email-integration-plan.md) | Implemented (now `applemail` plugin) | Native Apple Mail / AppleScript email provider |
