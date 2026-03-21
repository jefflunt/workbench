# Configuration

## File Location

```
~/.config/workbench/config.toml
```

Resolved via `$XDG_CONFIG_HOME` if set, otherwise `$HOME/.config`. **`os.UserConfigDir()` is not used** — on macOS that returns `~/Library/Application Support`, which is the wrong path. Falls back to `$HOME/.config` if `$HOME` is unset.

If the file does not exist, the program starts with defaults — not an error. If the file exists but cannot be parsed, the program prints to stderr and exits.

### TOML section ordering

Regular tables (`[email]`, `[log]`, `[theme]`) **must** appear before array-of-tables (`[[layout.row]]`) in the TOML file. BurntSushi/toml silently ignores regular tables placed after array-of-tables. The correct order is:

```
[email] → [github] → [jira] → [log] → [theme] → [layout] → [[layout.row]] ...
```

---

## All Config Fields

### `[email]`

| Field | Type | Default | Effect |
|-------|------|---------|--------|
| `account_name` | string | `""` | Apple Mail account description (e.g. `"Exchange"`). When non-empty, selects the `applemail` provider. Takes precedence over `command`. |
| `limit` | int | `50` | Max messages to fetch (Apple Mail provider only). `≤ 0` → coerced to 50. |
| `command` | string | `""` | Shell command for the generic CLI email provider. Used only when `account_name` is empty. Split on whitespace, then exec'd. Empty → error shown in pane. |

**Provider selection:** `account_name != ""` → `applemail.New(accountName, limit)`. Otherwise → `email.New(command)`.

### `[github]`

| Field | Type | Default | Effect |
|-------|------|---------|--------|
| `token` | string | `""` | Personal access token (`repo` + `read:org` scopes). Empty → falls back to `gh` CLI auth. |

### `[jira]`

| Field | Type | Default | Effect |
|-------|------|---------|--------|
| `assigned_jql` | string | `"assignee = currentUser() ORDER BY updated DESC"` | JQL for assigned tickets. Empty string → query skipped. |
| `watching_jql` | string | `"watcher = currentUser() AND status in ('In Review', 'Blocked')"` | JQL for watched tickets. Empty → skipped. Errors are non-fatal. |
| `limit` | int | `50` | Max results per JQL query. `≤ 0` → coerced to 50 in `jira.New()`. |

### `[log]`

| Field | Type | Default | Effect |
|-------|------|---------|--------|
| `max_lines` | int | `1000000` | Maximum log lines retained in the in-memory ring buffer. Passed to `wblog.Init`. |

### `[theme]`

| Field | Type | Default | Effect |
|-------|------|---------|--------|
| `active_pane_color` | string | `"212"` | Border color of the focused pane. Accepts ANSI 256-color codes (`"212"`) or hex (`"#ff87d7"`). Re-defaulted to `"212"` if the parsed value is empty. |

### `[layout]`

Layout is declared as a list of rows, each containing a list of panes. Rows and panes within a row are weighted relative to each other.

```toml
[[layout.row]]
weight = 1          # fraction of vertical space

  [[layout.row.pane]]
  provider = "email"
  weight   = 2      # fraction of horizontal space within this row

  [[layout.row.pane]]
  provider = "github"
  weight   = 3
```

`provider` must exactly match the `Name()` string returned by a registered provider (`"email"`, `"github"`, `"jira"`).

### Built-in default layout

If no `[layout]` section is present in the config file, this layout is used:

```
Row 0 (weight 1): email (weight 2) + github (weight 3)   → top half, 40/60 split
Row 1 (weight 1): jira  (weight 1)                        → bottom half, full width
```

---

## Loading Behaviour

1. `defaultConfig()` builds the struct with all defaults filled in.
2. `toml.Unmarshal(fileBytes, &cfg)` overlays the file on top — missing keys retain defaults.
3. After parsing, `ActivePaneColor` is re-defaulted to `"212"` if empty (guards against `active_pane_color = ""`).
4. Missing config file → silent success, defaults used.
5. Unreadable or malformed config file → `fmt.Fprintf(os.Stderr, ...)` + `os.Exit(1)`.

---

## Environment Variables

workbench reads two env vars directly:

| Variable | Used for |
|----------|----------|
| `$XDG_CONFIG_HOME` | Config file directory (falls back to `$HOME/.config`) |
| `$HOME` | Fallback when `$XDG_CONFIG_HOME` is unset |

External tools (`gh`, `acli`) may read their own env vars (e.g. `GH_TOKEN`, `GH_HOST`) — those are outside workbench's control.
