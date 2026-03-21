# TUI — Bubbletea Model, Modes, Keys, Styling

## Model Struct

```go
type model struct {
    cfg       Config
    providers map[string]provider.Provider  // keyed by provider name
    panes     []paneState                   // ordered by layout declaration
    paneIndex map[string]int                // providerName → panes index (O(1) lookup)
    active    int                           // index of focused pane
    termW     int                           // terminal width (initial: 80)
    termH     int                           // terminal height (initial: 24)
    mode      uiMode
    prevMode  uiMode                        // mode to restore when closing help overlay
    loginErr  error                         // last interactive login error

    // Log overlay state
    logLines       []wblog.Line
    logScroll      int
    logAutoScroll  bool
    logSearchMode  string          // "", "pending", "s", "f", "F"
    logInput       textinput.Model
    logQuery       string
    logMatches     []int
    logMatchCursor int
}
```

---

## Per-Pane State (`paneState`)

Each pane carries its own search/filter state independently of all other panes.

```go
type paneState struct {
    providerName  string
    items         []provider.Item  // currently shown items (may be stale)
    err           error
    loading       bool
    stale         bool             // showing cached data, fetch in-flight
    cursor        int              // index into visibleItems()
    spinner       spinner.Model

    // Per-pane search/filter (mirrors log overlay model)
    searchMode    string           // "", "pending", "s", "f", "F"
    searchQuery   string           // committed query
    searchMatches []int            // item indices matching searchQuery
    searchCursor  int              // index into searchMatches for n/N
    searchInput   textinput.Model
}
```

`loading` and `stale` are mutually exclusive in normal operation. `loading` is only true before the very first successful fetch.

---

## UI Modes

```go
const (
    modeNormal uiMode = iota  // default
    modeHelp                  // full-screen help overlay; any key → prevMode
    modeLogin                 // login prompt: g/a/esc
    modeLog                   // log overlay; Ctrl+L/q/esc closes
)
```

### Mode transitions

```
Normal ──?──────→ Help    ──any key──→ prevMode  (? is global: works from any mode)
Normal ──L──────→ Login   ──g/a─────→ Normal (launches interactive subprocess)
                           ──Esc────→ Normal
Normal ──Ctrl+L─→ Log     ──Ctrl+L/q/Esc → Normal
Log    ──?──────→ Help    ──any key──→ Log   (prevMode=Log restored)
```

Note: `modeSearch` was removed. Search is now **per-pane** and operates within `modeNormal` (see below).

---

## Keyboard Shortcuts

### Global (works from any mode)

| Key | Action |
|-----|--------|
| `?` | Toggle help overlay (saves current mode; any key restores it) |

### Normal mode — navigation

| Key | Action |
|-----|--------|
| `q` / `Ctrl+C` | Quit |
| `Tab` | Next pane (wraps) |
| `Shift+Tab` | Previous pane (wraps) |
| `j` / `↓` | Cursor down 1 |
| `k` / `↑` | Cursor up 1 |
| `J` | Cursor down 10 |
| `K` | Cursor up 10 |
| `R` | Refresh all panes |
| `L` | Login mode |
| `Ctrl+L` | Open log overlay |

### Normal mode — per-pane search/filter

Search and filter are scoped to the **currently focused pane**. Each pane holds its own independent state.

| Key | Action |
|-----|--------|
| `/` | Enter pending mode — wait for `s`, `f`, or `F` |
| `/s` then `Enter` | Commit search query; highlight matches, `n`/`N` navigate |
| `/f` then `Enter` | Commit filter; non-matching items **hidden** |
| `/F` then `Enter` | Commit filter; non-matching items **dimmed** |
| `n` | (search mode) jump cursor to next match |
| `N` | (search mode) jump cursor to previous match |
| `Esc` | Clear current pane's active search/filter |

While the text input is focused (`/s`, `/f`, `/F`):

| Key | Action |
|-----|--------|
| Any character | Appended to query |
| `Enter` | Commit query |
| `Esc` | Cancel, clear query |

**Search fields:** Title + Subtitle + Meta (case-insensitive contains match).  
**Cursor clamping:** `j`/`↓` clamped at `len(visibleItems)-1`; `k`/`↑` at 0. `J` uses `min(cursor+10, len-1)`; `K` uses `max(cursor-10, 0)`.

### Login mode

| Key | Action |
|-----|--------|
| `g` | `gh auth login` (interactive subprocess) |
| `a` | `acli jira auth login` (interactive subprocess) |
| `Esc` / any other | Cancel, return to normal |

### Help mode

| Key | Action |
|-----|--------|
| Any key | Return to `prevMode` |

### Log overlay

| Key | Action |
|-----|--------|
| `Ctrl+L` / `q` / `Esc` | Close overlay |
| `j` / `↓` | Scroll down |
| `k` / `↑` | Scroll up |
| `g` | Jump to top |
| `G` | Jump to bottom (re-enables auto-scroll) |
| `/s` then `Enter` | Search; `n`/`N` cycle matches |
| `/f` then `Enter` | Filter (hide non-matching lines) |
| `/F` then `Enter` | Filter (dim non-matching lines) |
| `n` / `N` | Next / previous search match |
| `w` | Quick-filter to `war` (warnings) — toggle |
| `e` | Quick-filter to `err` (errors) — toggle |
| `i` | Quick-filter to `ifo` (info) — toggle |
| `Esc` | Clear current search/filter |

---

## Per-Pane Search Behaviour

- `visibleItems(paneIdx)` returns:
  - In `/f` mode with a committed query: only items where `itemMatches(item, query)` is true.
  - All other modes: all `p.items` (highlighting/dimming handled at render time).
- `paneMatches(items, q)` returns a slice of item indices matching `q` — used to build `searchMatches` for `/s` and `/F` mode.
- `itemMatches(item, q)` — case-insensitive contains on `Title`, `Subtitle`, and `Meta`.
- Switching panes does **not** clear another pane's search state.
- `R` (refresh) does **not** clear search state on any pane.

---

## Login Flow

Uses `tea.ExecProcess` — suspends the TUI, restores the terminal to cooked mode, hands stdin/stdout/stderr to the subprocess, then resumes. Required because `gh auth login` and `acli jira auth login` are interactive terminal programs.

If the subprocess exits with an error, `m.loginErr` is set and displayed in the footer until the next login attempt.

---

## Footer Visibility

The footer row is only shown when there is something worth showing. `footerVisible` is computed once per `View()` call:

```go
ap := m.panes[m.active]
footerVisible := m.mode == modeLogin || m.loginErr != nil ||
    ap.searchMode != "" || ap.searchInput.Focused()
```

Footer content priority:
1. `modeLogin` → login prompt
2. Active pane's `searchInput.Focused()` → `/mode` + text input + hint
3. Active pane's `searchMode == "pending"` → chord hint (`/s`, `/f`, `/F`)
4. Active pane's search/filter with committed query → match count + `n`/`N` hint or filter summary
5. `loginErr != nil` → error text

In default mode with no active search and no login error, the footer is **absent** — panes use the full terminal height.

---

## `renderPane` — Content String Construction

`renderPane(idx, contentHeight int) string` builds the string passed into a pane box.

1. **Header line:** `PROVNAME` in `titleStyle`. If a search/filter is active on this pane, a badge is appended:
   - `/s` mode: `[search: "query"  N match(es)]`
   - `/f` mode: `[filter: "query"  N]`
   - `/F` mode: `[dim: "query"]`
   - `pending`: `[/s search  /f filter  /F dim]`
2. **Loading state:** spinner + "Loading…"
3. **Error state:** `errorStyle("Error: ...")`
4. **Items:** `visibleItems(idx)` — filtered in `/f` mode, all items otherwise.
5. **Scroll window:** if `len(items) > itemRows`, one row reserved for scroll indicator.
6. **Each item row:** `"%-40s %-20s %s"` — Title(40), Subtitle(20), Meta(15), preceded by `"▶ "` or `"  "`.
   - Current cursor + active pane: `selectedStyle`
   - Current search match (`/s` mode): `selectedStyle`
   - Other match (`/s` or `/F` mode): `matchStyle`
   - Non-matching in `/F` mode: `logDimStyle`
   - `item.Highlighted`: `highlightedStyle`
   - Default: `normalStyle`
7. **Scroll indicator:** `↑↓ N/Total (pct%)` if content overflowed.

---

## Log Overlay

Rendered by `renderLogOverlay`. An 80%×80% centered box (min 40×10). Auto-scrolls to the bottom unless the user has scrolled up or activated a search. A background `logTick` message fires every 250ms to refresh the line snapshot from the in-memory ring buffer.

---

## Styling Reference

All styles are package-level `lipgloss.Style` vars in `main.go`. None are recomputed per frame (except active pane border color which reads from config).

| Variable | Colors | Usage |
|----------|--------|-------|
| `titleStyle` | fg `"212"` bold | Pane header |
| `selectedStyle` | fg `"229"`, bg `"57"` bold | Selected item; current search match |
| `highlightedStyle` | fg `"196"` | `item.Highlighted=true` (unread, high-priority) |
| `normalStyle` | fg `"252"` | Normal items; log lines |
| `matchStyle` | fg `"226"`, bg `"58"` bold | Search-matching items; pane header badge |
| `logDimStyle` | fg `"240"` | Non-matching lines/items in `/F` dim-filter mode |
| `metaStyle` | fg `"243"` | Meta field; scroll indicator; footer hints |
| `subtitleStyle` | fg `"110"` | Subtitle field |
| `errorStyle` | fg `"196"` bold | Errors inline + login error in footer |
| `footerStyle` | fg `"240"` | Footer hint text |
| `overlayBorderStyle` | border `"212"` rounded, padding(1,3) | Help + log overlay box border |
| `overlayTitleStyle` | fg `"212"` bold | Overlay title |
| `overlayKeyStyle` | fg `"229"` bold | Key names in help; "Login:" label |
| `overlayDescStyle` | fg `"252"` | Descriptions in help |
| `searchBarStyle` | fg `"212"` bold | The `/mode` prompt in footer |
| `searchNoMatchStyle` | fg `"196"` | "no matches" in footer; "No matches." in pane |

**Active pane border:** `lipgloss.Color(cfg.Theme.ActivePaneColor)` (default `"212"`).  
**Inactive pane border:** `lipgloss.Color("240")` — hard-coded, not configurable.  
**Spinner:** `spinner.Dot`, `lipgloss.Color("205")`.

---

## Help Overlay

Rendered by `renderHelpOverlay`. Two-column layout: **Global** shortcuts on the left, **Log Pane** shortcuts on the right. Columns are height-padded to match and joined with `lipgloss.JoinHorizontal`. Placed with `lipgloss.Place(termW, termH, Center, Center, box)`. Closing the overlay restores `prevMode` — so pressing `?` from the log pane and then any key returns to the log pane.

---

## Window Resize

On `tea.WindowSizeMsg`, `m.termW` and `m.termH` are updated. The next `View()` call automatically recomputes all layout dimensions via the two-pass render. No state needs to be reset on resize.
