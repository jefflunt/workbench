// Command applemail is a workbench plugin that fetches email from macOS
// Mail.app via AppleScript, or from an external CLI command as a fallback.
//
// Configuration (under [plugins.applemail] in config.toml):
//
//	account_name  string  Mail.app account description (e.g. "Exchange").
//	              When set, reads email via AppleScript.
//	limit         int     Maximum number of messages to fetch (default 50).
//
// Protocol: reads a plugin.FetchRequest JSON from stdin, writes a
// plugin.FetchResponse JSON to stdout, exits 0 on success.
package main

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/jluntpcty/workbench/internal/plugin"
)

const (
	fieldSep  = "\x1e" // ASCII 30 — separates fields within a record
	recordSep = "\x1f" // ASCII 31 — separates records from each other
)

var senderRe = regexp.MustCompile(`^(.+?)\s*<(.+?)>\s*$`)

func main() {
	plugin.RunPlugin(fetch, nil)
}

func fetch(cfg map[string]any, query string) ([]plugin.Item, error) {
	accountName, _ := cfg["account_name"].(string)
	limit := 50
	if v, ok := cfg["limit"]; ok {
		switch n := v.(type) {
		case int:
			limit = n
		case int64:
			limit = int(n)
		case float64:
			limit = int(n)
		}
	}
	if limit <= 0 {
		limit = 50
	}

	if accountName == "" {
		return nil, fmt.Errorf("applemail: account_name is required in [plugins.applemail]")
	}

	script := buildListScript(accountName, limit)
	raw, err := runAppleScript(script)
	if err != nil {
		return nil, fmt.Errorf("applemail: osascript error: %w", err)
	}
	return parseMessages(raw), nil
}

// runAppleScript executes the given AppleScript via osascript.
func runAppleScript(script string) (string, error) {
	cmd := exec.Command("osascript", "-e", script) //nolint:gosec
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// buildListScript returns an AppleScript that fetches the top-N inbox messages
// from the named account.
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
    set output to output & (message id of m) & (ASCII character 30) & (subject of m) & (ASCII character 30) & (sender of m) & (ASCII character 30) & ((date received of m) as «class isot» as string) & (ASCII character 30) & (read status of m) & (ASCII character 30) & (flagged status of m) & (ASCII character 31)
    set msgCount to msgCount + 1
  end repeat
  return output
end tell`, safe, top)
}

// parseMessages splits the raw osascript output into plugin.Items.
func parseMessages(raw string) []plugin.Item {
	if raw == "" {
		return nil
	}
	records := strings.Split(raw, recordSep)
	items := make([]plugin.Item, 0, len(records))
	for _, rec := range records {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		// fields: [id, subject, sender, dateStr, readStr, flaggedStr]
		fields := strings.SplitN(rec, fieldSep, 7)
		if len(fields) < 6 {
			continue
		}
		subject := strings.TrimSpace(fields[1])
		if subject == "" {
			subject = "(no subject)"
		}
		sender := parseSender(fields[2])
		meta := formatMeta(fields[3])
		isRead := strings.TrimSpace(fields[4]) == "true"
		isFlagged := strings.TrimSpace(fields[5]) == "true"

		items = append(items, plugin.Item{
			Title:       subject,
			Subtitle:    sender,
			Meta:        meta,
			URL:         "message://%3C" + strings.TrimSpace(fields[0]) + "%3E",
			Highlighted: !isRead || isFlagged,
		})
	}
	return items
}

// parseSender extracts the display name from a sender string.
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

// formatMeta converts an AppleScript ISO date string to a compact display string.
//
//   - Same-day messages:    "14:35"
//   - This year, not today: "Mar 20"
//   - Other years:          "03/20/26"
func formatMeta(dateStr string) string {
	dateStr = strings.TrimSpace(dateStr)
	t, err := time.ParseInLocation("2006-01-02T15:04:05", dateStr, time.Local)
	if err != nil {
		return dateStr
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

// escapeAS escapes a string for safe embedding in an AppleScript double-quoted
// string literal.
func escapeAS(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
