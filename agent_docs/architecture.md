# Architecture

## Package Map

| Package | Path | Responsibility |
|---------|------|---------------|
| `main` | `cmd/workbench/main.go` | Bubbletea model, config loading, all TUI logic, provider wiring |
| `provider` | `internal/provider/provider.go` | `Provider` interface + `Item` struct — the only shared contract |
| `cache` | `internal/cache/cache.go` | Read/write JSON cache files per provider |
| `applemail` | `internal/applemail/applemail.go` | Native Apple Mail provider via AppleScript |
| `email` | `internal/email/email.go` | Email provider: shells out to a user-configured CLI |
| `github` | `internal/github/github.go` | GitHub provider: API or `gh` CLI fallback |
| `jira` | `internal/jira/jira.go` | Jira provider: shells out to `acli` |
| `layout` | `internal/layout/layout.go` | Weight-to-pixel layout math + lipgloss assembly |
| `log` | `internal/log/log.go` | Singleton structured logger; in-memory ring buffer + file persistence |

No package imports another internal package except `provider` (imported by `applemail`, `email`, `github`, `jira`, `cache`, and `main`) and `log` (imported by all providers and `main`). `layout` has no internal imports.

---

## Startup Sequence

```
main()
  loadConfig()                         → ~/.config/workbench/config.toml or defaults
  wblog.Init("", cfg.Log.MaxLines)     → open ~/.local/share/workbench/workbench.log
  select email provider:
    cfg.Email.AccountName != "" → applemail.New(accountName, limit)
    else                        → email.New(cfg.Email.Command)
  instantiate providers                → github.New, jira.New
  initialModel()
    allProviderNames(cfg)              → ordered names from layout config
    for each name:
      newPaneState(name)               → paneState{loading: true, searchInput: textinput.New()}
      cache.Load(name)                 → if data: items set, loading=false, stale=true
    logInput = textinput.New()
  tea.NewProgram(m, WithAltScreen).Run()
    Init()
      spinner.Tick for each pane
      fetchAll()                       → goroutine per provider, 30s timeout
      logTick()                        → schedules log snapshot refresh
    first WindowSizeMsg → termW/termH set
    fetchAll returns []fetchResultMsg
      Update handles batch:
        each pane: loading=false, stale=false, items set
        cache.Save for each (best-effort, errors ignored)
    View() called each frame
```

---

## Data Flow Per Frame

```
View()
  ┌─ footerVisible? ──────────────────────────────────────────────────────┐
  │  (mode==modeLogin || loginErr || ap.searchMode != "" || input focused) │
  └───────────────────────────────────────────────────────────────────────┘
         ↓
  reservedRows = 0 or 1
         ↓
  layout.Render(rows, {}, ..., reservedRows)   ← PASS 1: dims only
         ↓ returns dims map
  for each pane:
    renderPane(i, dims[name].ContentHeight)    ← produce content strings
         ↓
  layout.Render(rows, views, ..., reservedRows) ← PASS 2: real render
         ↓ returns body string
  if footer: JoinVertical(body, footer)
  if modeHelp: renderHelpOverlay(view)
  if modeLog:  renderLogOverlay(view)
  return view
```

The two-pass design exists because `renderPane` needs `ContentHeight` to compute scroll windowing, but that height only comes from the layout engine — which needs the content strings. Pass 1 breaks the cycle by discarding the rendered output and keeping only the `dims`.

---

## The `fetchAll` Command

All providers are fetched concurrently. The command returns a **single** `[]fetchResultMsg` — all pane results arrive in one `Update` call, so the screen never shows a half-updated state where some panes are stale while others are fresh.

```go
// Simplified pseudocode
func fetchAll(providers map[string]provider.Provider) tea.Cmd {
    return func() tea.Msg {
        ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        ch := make(chan fetchResultMsg, len(providers))
        var wg sync.WaitGroup
        for name, p := range providers {
            wg.Add(1)
            go func(name string, p provider.Provider) {
                defer wg.Done()
                items, err := p.Fetch(ctx)
                ch <- fetchResultMsg{providerName: name, items: items, err: err}
            }(name, p)
        }
        go func() { wg.Wait(); close(ch) }()
        var results []fetchResultMsg
        for r := range ch { results = append(results, r) }
        return results
    }
}
```

---

## Provider Registration

Providers are instantiated and wired in `main()`:

```go
// Email: Apple Mail takes precedence when account_name is configured.
switch {
case cfg.Email.AccountName != "":
    emailProvider = applemail.New(cfg.Email.AccountName, cfg.Email.Limit)
default:
    emailProvider = email.New(cfg.Email.Command)
}

providers := map[string]provider.Provider{
    "email":  emailProvider,
    "github": github.New(cfg.GitHub.Token),
    "jira":   jira.New(cfg.Jira.AssignedJQL, cfg.Jira.WatchingJQL, cfg.Jira.Limit),
}
```

The map is keyed by the same strings used in `config.toml` pane `provider` fields. Adding a new provider requires:
1. Implement `provider.Provider` in a new `internal/<name>` package.
2. Add it to the `providers` map in `main()`.
3. Reference the name in `config.toml` layout panes.

See [`providers.md`](providers.md) for the full contract.

---

## Message Types (bubbletea)

| Type | Direction | Meaning |
|------|-----------|---------|
| `tea.WindowSizeMsg` | runtime → Update | Terminal resized |
| `tea.KeyMsg` | runtime → Update | Key pressed |
| `[]fetchResultMsg` | fetchAll → Update | All provider results arrived |
| `fetchResultMsg{providerName, items, err}` | internal | Single provider result (batched into slice above) |
| `loginDoneMsg{err}` | tea.ExecProcess → Update | Interactive login subprocess exited |
| `spinner.TickMsg` | spinner → Update | Advance spinner animation |
| `logTickMsg` | logTick → Update | Refresh log line snapshot (every 250ms) |

---

## Per-Pane State (`paneState`)

```go
type paneState struct {
    providerName  string
    items         []provider.Item  // currently shown items (may be stale)
    err           error            // last fetch error (nil = ok)
    loading       bool             // true on first load, before any data
    stale         bool             // true when showing cached data, fetch in-flight
    cursor        int              // index into visibleItems()
    spinner       spinner.Model    // spinner.Dot, color "205"

    // Per-pane search/filter state
    searchMode    string           // "", "pending", "s", "f", "F"
    searchQuery   string           // committed query
    searchMatches []int            // item indices matching searchQuery
    searchCursor  int              // index into searchMatches for n/N navigation
    searchInput   textinput.Model  // text input for typing the query
}
```

`loading` and `stale` are mutually exclusive in normal operation. Both can be false (fresh data shown). `loading` is only true before the very first successful fetch.
