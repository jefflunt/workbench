# Providers

## The Contract

```go
// internal/provider/provider.go
type Item struct {
    Title       string
    Subtitle    string
    Meta        string
    URL         string
    Highlighted bool  // renders in highlightedStyle (red)
}

type Provider interface {
    Name() string
    Fetch(ctx context.Context) ([]Item, error)
}
```

**Rules for implementors:**
- `Name()` must exactly match the string used in `config.toml` pane `provider` fields. It is also the cache filename key and the `paneIndex` map key.
- `Fetch` is called concurrently with all other providers, with a 30-second context deadline. Respect cancellation.
- Return `nil, error` on total failure — do not mix partial results with a non-nil error (the caller takes the whole slice or nothing).
- `Highlighted: true` means the item gets red rendering and sorts visually "urgent". Use it for unread email, review-requested PRs, high-priority notifications.

---

## Apple Mail Provider (`internal/applemail`)

**What it does:** Reads messages from macOS Mail.app via AppleScript (`osascript`). macOS-only. Requires Automation permission (System Settings → Privacy & Security → Automation).

```go
type Provider struct {
    AccountName string  // Mail.app account Description field (e.g. "Exchange")
    Limit       int     // max messages to fetch (default 50)
}
func New(accountName string, limit int) *Provider
func (p *Provider) Name() string  // "email"
func (p *Provider) Fetch(ctx context.Context) ([]provider.Item, error)
```

**Fetch behaviour:**
1. Builds an AppleScript that opens `account "<AccountName>"`, gets its `Inbox`, and iterates up to `Limit` messages.
2. Each message record is encoded as ASCII-30-separated fields (id, subject, sender, date, read status, flagged status), records separated by ASCII-31.
3. Parses the output and maps to `provider.Item`:
   - Title = Subject
   - Subtitle = sender display name (extracted from `"Name <addr>"` format)
   - Meta = formatted date (`"14:35"` same day, `"Mar 20"` this year, `"03/20/26"` older)
   - Highlighted = `!isRead || isFlagged`

**`AccountName`** must match the **Description** field in Mail.app → Settings → Accounts (not the email address or display name).

**Auth:** none — delegates entirely to Mail.app's existing account configuration.

**Selection:** used when `cfg.Email.AccountName != ""`. Takes precedence over the CLI email provider.

---

## Email Provider (`internal/email`)

**What it does:** Shells out to a user-configured command, parses its JSON stdout. Used as a fallback when `account_name` is not set.

```go
type Provider struct{ Command string }
func New(command string) *Provider
func (p *Provider) Name() string  // "email"
func (p *Provider) Fetch(ctx context.Context) ([]provider.Item, error)
```

**Fetch behaviour:**
1. If `Command` is empty, returns an error immediately.
2. `strings.Fields(Command)` → `exec.CommandContext(ctx, parts[0], parts[1:]...)`.
3. Parses stdout as a JSON array of:
   ```json
   { "sender": "...", "subject": "...", "timestamp": "...", "read": false, "url": "..." }
   ```
4. Maps to `provider.Item`: Title=Subject, Subtitle=Sender, Meta=Timestamp, URL=URL, Highlighted=`!Read`.

**Auth:** none — fully delegated to the external command.  
**Note:** `#nosec G204` comment acknowledges the user-controlled command exec.

---

## GitHub Provider (`internal/github`)

**What it does:** Fetches unread notifications + open PRs authored by or review-requested from the current user. Two code paths: API (if token set) or `gh` CLI fallback.

```go
type Provider struct{ Token string }
func New(token string) *Provider
func (p *Provider) Name() string  // "github"
func (p *Provider) Fetch(ctx context.Context) ([]provider.Item, error)
```

### API path (`fetchViaAPI`) — when `Token != ""`

Uses `go-github/v71` with `oauth2.StaticTokenSource`. Three calls, all must succeed:
1. `client.Activity.ListNotifications` — up to 50 unread notifications.
2. `client.Search.Issues("is:open is:pr author:@me")` — up to 50 authored open PRs.
3. `client.Search.Issues("is:open is:pr review-requested:@me")` — up to 50 review-requested PRs.

Items appended in that order: notifications first, then authored PRs, then review-requested PRs.

### gh CLI path (`fetchViaGHCLI`) — when `Token == ""`

1. `gh api notifications?all=false&per_page=50 --paginate` — paginated output: multiple JSON arrays `[...][...]` are joined by replacing `][` with `,` before unmarshal.
2. `gh search prs --json title,number,repository,state,url --limit 50 "is:open is:pr author:@me"`.
3. `gh search prs --json title,number,repository,state,url --limit 50 "is:open is:pr review-requested:@me"` — error here is **non-fatal**; returns what was already accumulated.

### Item Mapping

**Notifications:**
- Title = subject title
- Subtitle = repo full name
- Meta = `formatReason(reason) + " · " + formatType(type)`
- URL = repo HTML URL
- Highlighted = `isHighPriority(reason)`

**PRs:**
- Title = PR title
- Subtitle = repo name (extracted from RepositoryURL by scanning backwards for two `/`)
- Meta = `"review requested"` or PR state string
- Highlighted = `reviewRequested` (true for review-requested PRs)

### Helper mappings

`formatReason`: `assign→"assigned"`, `author→"author"`, `comment→"comment"`, `mention→"mentioned"`, `review_requested→"review requested"`, `subscribed→"subscribed"`, `team_mention→"team mention"`, `ci_activity→"CI"`, `state_change→"state changed"`, `approval_requested→"approval requested"`.

`formatType`: `PullRequest→"PR"`, `Issue→"issue"`, `Release→"release"`, `Discussion→"discussion"`, `Commit→"commit"`, `CheckSuite→"CI"`, default→`strings.ToLower`.

`isHighPriority`: true for `review_requested`, `mention`, `team_mention`, `assign`, `approval_requested`.

---

## Jira Provider (`internal/jira`)

**What it does:** Runs up to two `acli` queries (assigned + watching), deduplicates results.

```go
type Provider struct {
    AssignedJQL string
    WatchingJQL string
    Limit       int
}
func New(assignedJQL, watchingJQL string, limit int) *Provider
func (p *Provider) Name() string  // "jira"
func (p *Provider) Fetch(ctx context.Context) ([]provider.Item, error)
```

**Fetch behaviour:**
1. If `AssignedJQL != ""`: `acli jira workitem search --jql "<AssignedJQL>" --json --limit <Limit>`. Error → return immediately.
2. If `WatchingJQL != ""`: same with `WatchingJQL`. Error here is **non-fatal** — returns assigned items.
3. Deduplication by `Subtitle` (the ticket key): a ticket appearing in both queries is shown once.

**Item mapping:**
- Title = `Fields.Summary`
- Subtitle = `Key` (e.g. `"PROJ-42"`)
- Meta = `Fields.Priority.Name + " · " + Fields.Status.Name` (priority omitted if empty)
- URL = `""` (not set)
- Highlighted = `status in {"In Review", "Blocked"}`

**Auth:** fully delegated to `acli jira auth`.

---

## Cache (`internal/cache`)

Wraps provider items in a JSON envelope on disk. Shared by all providers.

```go
func Load(providerName string) ([]provider.Item, error)
func Save(providerName string, items []provider.Item) error
```

**Location:** `$XDG_CACHE_HOME/workbench/<name>.json` → typically `~/.cache/workbench/`.

**Load:** returns `nil, nil` if file missing or JSON corrupt (never an error at startup).

**Save:** atomic — writes to `<path>.tmp` then `os.Rename`. Called in `Update` after a successful fetch, only when `err == nil && len(items) > 0`. Errors are silently ignored by the caller (`_ = cache.Save(...)`).

**No TTL.** Stale data is always served at startup; freshness is tracked by `paneState.stale`.
