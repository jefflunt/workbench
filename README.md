# workbench

A TUI-based centralized dashboard for developers, built in Go.  
Displays GitHub notifications & pull requests, Jira tickets, and email in a single terminal window.

```
╭──────────────────────────────╮╭─────────────────────────────────────────────╮
│ APPLEMAIL                    ││ GITHUB                                      │
│▶ Sprint planning notes  …    ││  Fix flaky test  myorg/api  review req · PR │
│  Weekly sync recap      …    ││  Add dark mode   myorg/ui   author · PR     │
│                              ││↑↓ 1/12 (0%)                                 │
╰──────────────────────────────╯╰─────────────────────────────────────────────╯
╭─────────────────────────────────────────────────────────────────────────────╮
│ JIRA                                                                        │
│  Implement OAuth flow   PROJ-42   High · In Review                          │
│  Fix login redirect     PROJ-38   Medium · Blocked                          │
╰─────────────────────────────────────────────────────────────────────────────╯
tab/shift+tab: switch pane  j/k: navigate  R: refresh  L: login  ?: help  q: quit
```

## Requirements

| Tool | Purpose | Required? |
|------|---------|-----------|
| [Go 1.25+](https://go.dev/dl/) | Build the binary | Yes |
| [gh CLI](https://cli.github.com/) | GitHub auth fallback (when no token) | If no GitHub token |
| [acli](https://developer.atlassian.com/cloud/acli) | Jira data via `acli jira workitem search` | For jira plugin |
| Apple Mail | Email via AppleScript (macOS) | For applemail plugin |

## Installation

### Build from source

```sh
git clone https://github.com/jluntpcty/workbench.git
cd workbench
go build -o workbench ./cmd/workbench
```

Move the binary somewhere on your `$PATH`:

```sh
mv workbench ~/.local/bin/
```

### Run directly

```sh
go run ./cmd/workbench
```

### Install as a Go binary

```sh
go install ./cmd/workbench
```

### Rebuild and reinstall (development)

```sh
script/rebuild-and-reinstall
```

Runs tests, builds, and installs in one step.

## Configuration

workbench reads its configuration from:

```
~/.config/workbench/config.toml
```

Copy the example config to get started:

```sh
mkdir -p ~/.config/workbench
cp config.example.toml ~/.config/workbench/config.toml
```

Then edit it to match your setup. If no config file is found, sensible defaults are used.

### Plugin configuration

Each plugin is configured under a `[plugins.<name>]` section in `config.toml`. The name matches the plugin binary filename (e.g. `applemail`, `github`, `jira`) and the `provider` value in the layout.

### Full config reference

```toml
[plugins.applemail]
# Mail.app account description (Mail.app → Settings → Accounts → Description).
account_name = "Exchange"
# Max messages to fetch (default 50).
limit = 50

[plugins.github]
# Personal access token with `repo` and `read:org` scopes.
# Leave empty to use the `gh` CLI (must be authenticated via `gh auth login`).
token = ""

[plugins.jira]
# JQL query for tickets assigned to the current user.
assigned_jql = "assignee = currentUser() ORDER BY updated DESC"

# JQL query for tickets the current user is watching that need follow-up.
watching_jql = "watcher = currentUser() AND status in ('In Review', 'Blocked')"

# Maximum results returned per query (default: 50).
limit = 50

[theme]
# Border color of the active (focused) pane.
# Accepts ANSI 256-color codes (e.g. "212") or hex colors (e.g. "#ff87d7").
active_pane_color = "212"

[log]
# Maximum log lines kept in memory (default: 1000000).
max_lines = 1000000

[layout]

  # Each [[layout.row]] defines a horizontal band.
  # `weight` controls what fraction of the terminal height this row occupies.
  # Within a row, each [[layout.row.pane]] gets a fraction of the width.
  # Fractions are computed as: weight / sum(all weights in that axis).

  [[layout.row]]
  weight = 1   # 50% of vertical space (1 out of 1+1)

    [[layout.row.pane]]
    provider = "applemail"
    weight   = 2   # 40% horizontal (2 out of 2+3)

    [[layout.row.pane]]
    provider = "github"
    weight   = 3   # 60% horizontal (3 out of 2+3)

  [[layout.row]]
  weight = 1   # 50% of vertical space

    [[layout.row.pane]]
    provider = "jira"
    weight   = 1   # 100% horizontal (only pane in this row)
```

#### Layout mechanics

- Rows are stacked vertically; panes within a row are placed side by side.
- Heights and widths are computed at runtime from `tea.WindowSizeMsg` so the
  layout adapts automatically when the terminal is resized.
- `provider` must match the filename of a plugin binary in `~/.config/workbench/plugins/`.
- Pane content scrolls when items exceed the available height; a `↑↓ n/total (pct%)` indicator appears at the bottom of the pane.

### Authentication

**GitHub** — either set `token` in `config.toml` or authenticate with the `gh` CLI:

```sh
gh auth login
```

**Jira** — authenticate once with acli; workbench never manages Jira credentials:

```sh
acli jira auth login
```

Install acli via Homebrew:

```sh
brew tap atlassian/homebrew-acli
brew install acli
```

**Apple Mail** — set `account_name` to the Description shown in Mail.app → Settings → Accounts. macOS will prompt for Automation permission on first run.

You can also trigger authentication flows from inside workbench with `L` (see keyboard shortcuts below).

### Caching

workbench caches each pane's last-fetched data to:

```
~/.cache/workbench/<provider>.json
```

On startup, cached data is displayed immediately while fresh data loads in the background. A spinner next to the pane title indicates a background refresh is in progress. The cache is updated automatically after every successful fetch.

## Keyboard shortcuts

| Key | Action |
|-----|--------|
| `?` | Toggle help overlay (works from any mode) |
| `q` / `Ctrl+C` | Quit |
| `Tab` | Move focus to the next pane |
| `Shift+Tab` | Move focus to the previous pane |
| `j` / `↓` | Move cursor down in the focused pane |
| `k` / `↑` | Move cursor up in the focused pane |
| `J` | Move cursor down 10 items |
| `K` | Move cursor up 10 items |
| `R` | Refresh all panes concurrently |
| `L` then `g` | Login: GitHub (`gh auth login`) |
| `L` then `a` | Login: Atlassian / Jira (`acli jira auth login`) |
| `Ctrl+L` | Open log pane |
| `Space` | Play / Pause (music) |
| `h` | Previous track (music) |
| `l` | Next track (music) |
| `s` | Shuffle (music) |
| `a` | Add to Queue (music) |
| `n` | Now Playing / Queue (music) |

### Search and filter (per pane)

Press `/` to start — then type the second key:

| Chord | Effect |
|-------|--------|
| `/s` | Search: all items shown, matches highlighted. `n`/`N` jump to next/previous match. |
| `/f` | Filter: non-matching items hidden. |
| `/F` | Dim-filter: non-matching items dimmed. |

Each pane holds its own independent search/filter state. Switching panes does not clear the search on other panes. Press `Esc` to clear the active pane's search.

### Log pane

| Key | Action |
|-----|--------|
| `Ctrl+L` / `q` / `Esc` | Close log pane |
| `j` / `k` | Scroll down / up |
| `g` / `G` | Top / bottom |
| `/s`, `/f`, `/F` | Search / filter (same as pane search) |
| `n` / `N` | Next / previous match |
| `w` | Quick-filter: warnings |
| `e` | Quick-filter: errors |
| `i` | Quick-filter: info |

## Project structure

```
workbench/
├── cmd/
│   └── workbench/
│       └── main.go          # bubbletea model, plugin discovery, entry point
├── internal/
│   ├── plugin/
│   │   ├── plugin.go        # Plugin protocol types (Item, FetchRequest, FetchResponse, Provider)
│   │   ├── run.go           # RunPlugin helper for plugin binaries
│   │   └── subprocess.go    # SubprocessProvider: spawns a plugin binary, speaks the protocol
│   ├── cache/
│   │   └── cache.go         # On-disk JSON cache (~/.cache/workbench/)
│   ├── layout/
│   │   └── layout.go        # Weight-based pane layout engine
│   └── log/
│       └── log.go           # Singleton logger, in-memory ring buffer, file persistence
├── plugins/
│   ├── applemail/
│   │   └── main.go          # Apple Mail plugin (macOS, AppleScript)
│   ├── github/
│   │   └── main.go          # GitHub plugin (notifications + PRs via API or gh CLI)
│   └── jira/
│       └── main.go          # Jira plugin (shells out to acli)
├── script/
│   └── rebuild-and-reinstall  # Test, build workbench + all plugins, install everything
├── config.example.toml
├── go.mod
└── go.sum
```

### Plugin discovery

At startup, workbench scans `~/.config/workbench/plugins/` for executable files. Each file becomes a pane provider; its filename is the provider name used in `[[layout.row.pane]] provider = "..."` and in `[plugins.<name>]` config.

The `script/rebuild-and-reinstall` script builds all plugins in `plugins/*/` and installs them to `~/.config/workbench/plugins/` automatically.

## Contributing

```sh
# Verify the build
go build ./...

# Run the linter
go vet ./...

# Run tests
go test ./...
```
