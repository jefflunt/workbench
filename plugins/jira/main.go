// Command jira is a workbench plugin that fetches work items from Jira by
// shelling out to the Atlassian CLI (acli).
//
// Configuration (under [plugins.jira] in config.toml):
//
//	assigned_jql  string  JQL for tickets assigned to the current user.
//	watching_jql  string  JQL for tickets the current user is watching.
//	limit         int     Maximum results per query (default 50).
//
// Protocol: reads a plugin.FetchRequest JSON from stdin, writes a
// plugin.FetchResponse JSON to stdout, exits 0 on success.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/jluntpcty/workbench/internal/plugin"
)

// workItem is the JSON shape produced by acli for a single Jira work item.
type workItem struct {
	Key    string `json:"key"`
	Fields struct {
		Summary string `json:"summary"`
		Status  struct {
			Name string `json:"name"`
		} `json:"status"`
		Priority struct {
			Name string `json:"name"`
		} `json:"priority"`
	} `json:"fields"`
}

var needsFollowUp = map[string]bool{
	"In Review": true,
	"Blocked":   true,
}

func main() {
	plugin.RunPlugin(fetch, expand, nil)
}

func expand(cfg map[string]any, item plugin.Item) ([]plugin.Item, error) {
	return nil, nil
}

func fetch(cfg map[string]any, query string) ([]plugin.Item, error) {
	assignedJQL, _ := cfg["assigned_jql"].(string)
	watchingJQL, _ := cfg["watching_jql"].(string)
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

	// Apply defaults if config keys are missing.
	if assignedJQL == "" {
		assignedJQL = "assignee = currentUser() ORDER BY updated DESC"
	}
	if watchingJQL == "" {
		watchingJQL = "watcher = currentUser() AND status in ('In Review', 'Blocked')"
	}

	ctx := context.Background()
	baseURL := detectBaseURL(ctx)
	var items []plugin.Item

	if assignedJQL != "" {
		got, err := runQuery(ctx, assignedJQL, limit, baseURL)
		if err != nil {
			return nil, err
		}
		items = append(items, got...)
	}

	if watchingJQL != "" {
		got, err := runQuery(ctx, watchingJQL, limit, baseURL)
		if err != nil {
			// Non-fatal: return what we already have.
			return items, nil
		}
		// Deduplicate by ticket key (Subtitle).
		seen := make(map[string]bool, len(items))
		for _, it := range items {
			seen[it.Subtitle] = true
		}
		for _, it := range got {
			if !seen[it.Subtitle] {
				items = append(items, it)
			}
		}
	}

	return items, nil
}

func runQuery(ctx context.Context, jql string, limit int, baseURL string) ([]plugin.Item, error) {
	cmd := exec.CommandContext(ctx,
		"acli", "jira", "workitem", "search",
		"--jql", jql,
		"--json",
		"--limit", strconv.Itoa(limit),
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("jira: acli failed: %w – stderr: %s", err, stderr.String())
	}

	var workItems []workItem
	if err := json.Unmarshal(stdout.Bytes(), &workItems); err != nil {
		return nil, fmt.Errorf("jira: failed to parse acli JSON output: %w", err)
	}

	items := make([]plugin.Item, 0, len(workItems))
	for _, wi := range workItems {
		status := wi.Fields.Status.Name
		priority := wi.Fields.Priority.Name
		meta := status
		if priority != "" {
			meta = priority + " · " + status
		}
		url := ""
		if baseURL != "" {
			url = "https://" + baseURL + "/browse/" + wi.Key
		}
		items = append(items, plugin.Item{
			Title:       wi.Fields.Summary,
			Subtitle:    wi.Key,
			Meta:        meta,
			URL:         url,
			Highlighted: needsFollowUp[status],
		})
	}
	return items, nil
}

// detectBaseURL runs "acli jira auth status" and extracts the Site hostname.
// Returns empty string on any failure (open action will be silently skipped).
func detectBaseURL(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, "acli", "jira", "auth", "status")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Site:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Site:"))
		}
	}
	return ""
}
