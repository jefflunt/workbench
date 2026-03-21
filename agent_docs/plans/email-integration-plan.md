# Plan: Native Apple Mail Email Provider

## Overview

Replace the current generic CLI-shim email provider (`internal/email/email.go`) with a native Go implementation that reads directly from macOS Mail.app via AppleScript — the same mechanism used by the `focus-for-outlook` TypeScript project at `~/code/focus-for-outlook`.

The result is a zero-dependency email provider: no external command to configure, no JSON format to wrangle, no separate daemon. The user only needs to name their Mail.app account in `config.toml`.

---

## Source Reference: focus-for-outlook

The TypeScript project at `~/code/focus-for-outlook` already solves this problem completely. The translation to Go is straightforward because the approach is pure subprocess invocation (`osascript -e <script>`) with string parsing — no Node-specific APIs are involved.

### Key files to translate

| TypeScript file | What to port |
|----------------|-------------|
| `src/services/mail-app-bridge.ts` — `MailAppBridge` | The entire AppleScript layer: `listNewMessages`, `parseMessageList`, `parseSenderString`, `normalizeDate`, `formatAppleScriptDate`, `escapeAS` |
| `src/services/mail-app-bridge.ts` — `runAppleScript` | Thin `osascript` wrapper → `exec.CommandContext` |
| `src/db/messages.ts` — `MessageRow` | The data shape for display purposes |
| `src/cli/inbox.ts` — `formatDate`, `renderMessageRow` | Date formatting for the Meta field |

The daemon, SQLite, rules engine, watchlist, and calendar bridge from `focus-for-outlook` are **out of scope** for this plan. We only need the read path.

---

## How Apple Mail AppleScript Works

### The osascript mechanism

`runAppleScript(script)` in TypeScript does:
```typescript
execFile('osascript', ['-e', script], { timeout: 30000, maxBuffer: 10MB })
```

In Go this is:
```go
cmd := exec.CommandContext(ctx, "osascript", "-e", script)
out, err := cmd.Output()
```

macOS requires **Automation permission** for the calling process (Terminal, iTerm2, etc.) to control Mail.app. The first invocation triggers a system permission dialog. Once granted, subsequent calls succeed silently.

### Record/field encoding

AppleScript cannot return structured data directly over stdout. `focus-for-outlook` uses a clever two-character encoding:

- **ASCII 30** (Record Separator, `\x1e`) — separates fields within a message record
- **ASCII 31** (Unit Separator, `\x1f`) — separates message records from each other

This avoids any delimiter collision with email content (subjects, sender names, etc. never contain control characters).

### The list-messages AppleScript

```applescript
tell application "Mail"
  set acct to account "<accountName>"
  set inboxRef to mailbox "Inbox" of acct
  set msgList to messages of inboxRef
  set output to ""
  set msgCount to 0
  repeat with m in msgList
    if msgCount ≥ <top> then exit repeat
    set msgId to id of m
    set msgSubject to subject of m
    set msgSender to sender of m
    set msgDate to date received of m
    set msgRead to read status of m
    set msgFlagged to flagged status of m
    set msgHeaders to all headers of m
    set output to output
      & msgId          & (ASCII character 30)
      & msgSubject     & (ASCII character 30)
      & msgSender      & (ASCII character 30)
      & (msgDate as «class isot» as string) & (ASCII character 30)
      & msgRead        & (ASCII character 30)
      & msgFlagged     & (ASCII character 30)
      & msgHeaders     & (ASCII character 31)
    set msgCount to msgCount + 1
  end repeat
  return output
end tell
```

`«class isot»` coerces an AppleScript date to ISO 8601 string (`YYYY-MM-DDTHH:MM:SS`, no timezone marker).

### Field layout per record

```
fields[0]  id          Mail.app internal numeric message ID
fields[1]  subject     email subject line
fields[2]  sender      "Name <email@example.com>" or "email@example.com"
fields[3]  dateStr     "YYYY-MM-DDTHH:MM:SS" (local time, no Z)
fields[4]  readStr     "true" or "false"
fields[5]  flaggedStr  "true" or "false"
fields[6]  headersRaw  full RFC 822 headers as a single string
```

### Date handling

AppleScript's `«class isot»` returns local time without a timezone suffix. `focus-for-outlook` appends `"Z"` for simplicity (treats it as UTC). For a display-only use case this is acceptable, but a more correct approach is to use `time.Local` when parsing.

### Sender parsing

Senders come in two forms:
- `"Alice Smith <alice@example.com>"` → name=`"Alice Smith"`, address=`"alice@example.com"`
- `"alice@example.com"` → name=`""`, address=`"alice@example.com"`

Regex: `^(.+?)\s*<(.+?)>$`

### String escaping for AppleScript

Only two characters need escaping inside AppleScript double-quoted strings:
- `\` → `\\`
- `"` → `\"`

---

## Proposed Go Implementation

### New package: `internal/applemail`

Create `internal/applemail/applemail.go`.

```
internal/applemail/
└── applemail.go
```

The existing `internal/email/email.go` (CLI shim) is **kept** — it remains the fallback when `[email] command` is set. The new provider is a separate type, registered under the name `"email"` only when `[email] account_name` is set.

### Config changes

Add a new optional section to `Config` in `main.go`:

```go
type EmailConfig struct {
    Command     string `toml:"command"`      // existing: CLI shim path
    AccountName string `toml:"account_name"` // new: Mail.app account name
}
```

Provider selection logic in `main()`:

```go
var emailProvider provider.Provider
switch {
case cfg.Email.AccountName != "":
    emailProvider = applemail.New(cfg.Email.AccountName, cfg.Email.Limit)
case cfg.Email.Command != "":
    emailProvider = email.New(cfg.Email.Command)
default:
    emailProvider = email.New("") // will error gracefully in the pane
}
```

Add `limit` to `EmailConfig` with a default of 50 (matches GitHub/Jira conventions).

### `config.toml` — new fields

```toml
[email]
# Option A: use Apple Mail directly (macOS only)
account_name = "Exchange"   # Mail.app account name (Settings → Internet Accounts)
limit        = 50

# Option B: use an external command (existing behaviour, still works)
# command = "mymail --output json"
```

If both are set, `account_name` takes precedence.

### `internal/applemail/applemail.go` — full design

```go
package applemail

import (
    "context"
    "fmt"
    "os/exec"
    "regexp"
    "strings"
    "time"

    "github.com/jluntpcty/workbench/internal/provider"
)

const (
    fieldSep  = "\x1e" // ASCII 30 — between fields within a record
    recordSep = "\x1f" // ASCII 31 — between records
)

type Provider struct {
    AccountName string
    Limit       int
}

func New(accountName string, limit int) *Provider {
    if limit <= 0 {
        limit = 50
    }
    return &Provider{AccountName: accountName, Limit: limit}
}

func (p *Provider) Name() string { return "email" }

func (p *Provider) Fetch(ctx context.Context) ([]provider.Item, error) {
    script := buildListScript(p.AccountName, p.Limit)
    raw, err := runAppleScript(ctx, script)
    if err != nil {
        return nil, fmt.Errorf("applemail: osascript error: %w", err)
    }
    return parseMessages(raw), nil
}

func runAppleScript(ctx context.Context, script string) (string, error) {
    cmd := exec.CommandContext(ctx, "osascript", "-e", script)
    out, err := cmd.Output()
    if err != nil {
        return "", err
    }
    return strings.TrimSpace(string(out)), nil
}

func buildListScript(accountName string, top int) string {
    safe := escapeAS(accountName)
    return fmt.Sprintf(`tell application "Mail"
  set acct to account "%s"
  set inboxRef to mailbox "Inbox" of acct
  set msgList to messages of inboxRef
  set output to ""
  set msgCount to 0
  repeat with m in msgList
    if msgCount >= %d then exit repeat
    set output to output & (id of m) & (ASCII character 30) & (subject of m) & (ASCII character 30) & (sender of m) & (ASCII character 30) & ((date received of m) as «class isot» as string) & (ASCII character 30) & (read status of m) & (ASCII character 30) & (flagged status of m) & (ASCII character 31)
    set msgCount to msgCount + 1
  end repeat
  return output
end tell`, safe, top)
}
```

**Note:** headers are intentionally omitted from the list script (unlike `focus-for-outlook`). Headers are large and workbench only needs subject/sender/date/read/flagged for display. This keeps the AppleScript fast.

#### Parsing

```go
var senderRe = regexp.MustCompile(`^(.+?)\s*<(.+?)>\s*$`)

func parseMessages(raw string) []provider.Item {
    if raw == "" {
        return nil
    }
    records := strings.Split(raw, recordSep)
    items := make([]provider.Item, 0, len(records))
    for _, rec := range records {
        rec = strings.TrimSpace(rec)
        if rec == "" {
            continue
        }
        fields := strings.SplitN(rec, fieldSep, 7)
        if len(fields) < 6 {
            continue
        }
        // fields: [id, subject, sender, dateStr, readStr, flaggedStr]
        subject  := fields[1]
        if subject == "" { subject = "(no subject)" }
        sender   := parseSender(fields[2])
        meta     := formatMeta(fields[3])
        isRead   := strings.TrimSpace(fields[4]) == "true"
        isFlagged := strings.TrimSpace(fields[5]) == "true"

        items = append(items, provider.Item{
            Title:       subject,
            Subtitle:    sender,
            Meta:        meta,
            Highlighted: !isRead || isFlagged,
        })
    }
    return items
}

func parseSender(raw string) string {
    raw = strings.TrimSpace(raw)
    if m := senderRe.FindStringSubmatch(raw); m != nil {
        if name := strings.TrimSpace(m[1]); name != "" {
            return name
        }
        return m[2]
    }
    return raw
}

func formatMeta(dateStr string) string {
    dateStr = strings.TrimSpace(dateStr)
    // AppleScript «class isot» returns "YYYY-MM-DDTHH:MM:SS" (local time, no Z)
    t, err := time.ParseInLocation("2006-01-02T15:04:05", dateStr, time.Local)
    if err != nil {
        return dateStr // fallback: show raw string
    }
    now := time.Now()
    today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
    msgDay := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
    switch {
    case msgDay.Equal(today):
        return t.Format("15:04")
    case t.Year() == now.Year():
        return t.Format("Jan _2")
    default:
        return t.Format("01/02/06")
    }
}

func escapeAS(s string) string {
    s = strings.ReplaceAll(s, `\`, `\\`)
    s = strings.ReplaceAll(s, `"`, `\"`)
    return s
}
```

#### Highlighted logic

`focus-for-outlook` highlights unread messages. Flagged messages are also visually important. The proposed rule:

```
Highlighted = !isRead || isFlagged
```

This mirrors how `focus-for-outlook`'s `inbox.ts` treats unread (bold, cyan indicator) and flagged (yellow flag indicator) messages.

---

## macOS Permissions

Mail.app automation requires **Automation permission** via macOS System Settings → Privacy & Security → Automation. The first time workbench runs `osascript -e 'tell application "Mail"...'`, macOS presents a one-time dialog asking the user to grant Terminal (or iTerm2, or whatever terminal emulator they use) permission to control Mail.

There is no way to pre-grant this or suppress the dialog. If the user denies, `osascript` exits with a non-zero status and an error message like `"Not authorized to send Apple events to Mail."` — which surfaces cleanly as an error in the EMAIL pane.

**No entitlements, no Info.plist, no sandboxing issues** — this is a standard terminal app using standard automation APIs.

---

## Performance Characteristics

The AppleScript call is synchronous and **blocks for the duration of the fetch**. Typical timings from `focus-for-outlook` in practice:

| Inbox size | Approximate time |
|-----------|-----------------|
| 50 messages (top=50) | 1–3 seconds |
| 100 messages | 2–5 seconds |
| 200+ messages | 5–15 seconds |

These are acceptable for a background refresh (30s+ polling intervals). The workbench 30-second fetch timeout is sufficient.

Mail.app must be running. If it is not, AppleScript will launch it automatically — which adds a one-time startup cost of several seconds. Subsequent calls are fast.

The list script intentionally omits `all headers of m` (present in `focus-for-outlook` but not needed for display). Fetching headers multiplies the AppleScript execution time by ~3–5×.

---

## Open Questions for Review

1. **Account name discovery:** Should workbench offer a `focus list-accounts` style helper (a config-time CLI command that runs `tell application "Mail" to return name of every account`) to help users find their `account_name` value? Or is documenting it in `config.example.toml` sufficient?

2. **`since` date filter:** `focus-for-outlook`'s `listNewMessages` accepts a `since` date and adds an AppleScript `if (date received of m) > sinceDate` filter, enabling incremental fetches. Workbench's `Fetch` signature has no `since` parameter. For now the plan fetches the top-N always (matching the GitHub/Jira providers). A future optimisation could add a last-seen timestamp to the cache and use it as a filter.

3. **Highlighted: flagged vs unread only:** Should `isFlagged` alone be enough to highlight, or should flagged messages only highlight when also unread? Currently proposed: either unread OR flagged → highlighted. Adjust based on preference.

4. **Fallback behaviour when Mail.app is closed:** Currently would surface an error in the pane. An alternative is to return the cached items silently and only show the error after N consecutive failures. Out of scope for v1.

5. **`URL` field:** `focus-for-outlook` uses `message://%3C<Message-ID>%3E` URLs for deep-linking into Mail.app. Populating the `URL` field in `provider.Item` would be free to add (needs `all headers of m` to get the `Message-ID` header) but workbench has no `Enter`-to-open action yet. Defer until that feature exists.

---

## Implementation Steps

1. **Add `account_name` and `limit` to `EmailConfig`** in `main.go` and `defaultConfig()`.
2. **Create `internal/applemail/applemail.go`** with `Provider`, `Fetch`, `buildListScript`, `runAppleScript`, `parseMessages`, `parseSender`, `formatMeta`, `escapeAS`.
3. **Update provider selection in `main()`** to prefer `applemail` when `account_name` is set.
4. **Update `config.example.toml`** with the new `account_name` and `limit` fields, including a note about finding the account name in Mail.app settings.
5. **Manual testing:** Set `account_name = "Exchange"` (or equivalent), run workbench, verify EMAIL pane shows inbox contents, unread messages highlighted.
6. **(Optional, later)** Add `Enter` key support to open the selected message in Mail.app via `message://` URL scheme: `open "message://%3C<msgId>%3E"`.
