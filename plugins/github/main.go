// Command github is a workbench plugin that surfaces the authenticated user's
// GitHub notification inbox and pull requests.
//
// Configuration (under [plugins.github] in config.toml):
//
//	token  string  Personal access token with `repo` and `read:org` scopes.
//	               When empty, falls back to the `gh` CLI (must be authenticated).
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
	"strings"

	gogithub "github.com/google/go-github/v71/github"
	"golang.org/x/oauth2"

	"github.com/jluntpcty/workbench/internal/plugin"
)

func main() {
	plugin.RunPlugin(fetch, expand)
}

func expand(cfg map[string]any, item plugin.Item) ([]plugin.Item, error) {
	return nil, nil
}

func fetch(cfg map[string]any, query string) ([]plugin.Item, error) {
	token, _ := cfg["token"].(string)

	ctx := context.Background()
	if token != "" {
		return fetchViaAPI(ctx, token)
	}
	return fetchViaGHCLI(ctx)
}

// ---------------------------------------------------------------------------
// API path
// ---------------------------------------------------------------------------

func fetchViaAPI(ctx context.Context, token string) ([]plugin.Item, error) {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	client := gogithub.NewClient(tc)

	var items []plugin.Item

	// Notifications
	notifOpts := &gogithub.NotificationListOptions{
		All:         false,
		ListOptions: gogithub.ListOptions{PerPage: 50},
	}
	notifications, _, err := client.Activity.ListNotifications(ctx, notifOpts)
	if err != nil {
		return nil, fmt.Errorf("github: list notifications: %w", err)
	}
	for _, n := range notifications {
		items = append(items, notificationItem(n))
	}

	searchOpts := &gogithub.SearchOptions{
		ListOptions: gogithub.ListOptions{PerPage: 50},
	}

	// PRs authored by current user
	authored, _, err := client.Search.Issues(ctx, "is:open is:pr author:@me", searchOpts)
	if err != nil {
		return nil, fmt.Errorf("github: search author PRs: %w", err)
	}
	for _, issue := range authored.Issues {
		items = append(items, prItem(issue, false))
	}

	// PRs where review is requested
	reviewReq, _, err := client.Search.Issues(ctx, "is:open is:pr review-requested:@me", searchOpts)
	if err != nil {
		return nil, fmt.Errorf("github: search review-requested PRs: %w", err)
	}
	for _, issue := range reviewReq.Issues {
		items = append(items, prItem(issue, true))
	}

	return items, nil
}

// ---------------------------------------------------------------------------
// Item builders
// ---------------------------------------------------------------------------

func notificationItem(n *gogithub.Notification) plugin.Item {
	return plugin.Item{
		Title:       n.GetSubject().GetTitle(),
		Subtitle:    n.GetRepository().GetFullName(),
		Meta:        formatReason(n.GetReason()) + " · " + formatType(n.GetSubject().GetType()),
		URL:         subjectHTMLURL(n.GetSubject().GetURL(), n.GetSubject().GetType(), n.GetRepository().GetHTMLURL()),
		Highlighted: isHighPriority(n.GetReason()),
	}
}

// subjectHTMLURL converts a GitHub API subject URL to its HTML equivalent.
// The API URL looks like:
//
//	https://api.github.com/repos/owner/repo/pulls/123
//	https://api.github.com/repos/owner/repo/issues/456
//	https://api.github.com/repos/owner/repo/commits/abc
//	https://api.github.com/repos/owner/repo/releases/789
//
// Falls back to repoHTMLURL when the URL cannot be converted.
func subjectHTMLURL(apiURL, subjectType, repoHTMLURL string) string {
	if apiURL == "" {
		return repoHTMLURL
	}
	// Replace API host and /repos prefix with html host (no prefix).
	// api.github.com/repos/owner/repo/pulls/123
	//  →  github.com/owner/repo/pull/123   (note: "pulls" → "pull")
	const apiPrefix = "https://api.github.com/repos/"
	const htmlPrefix = "https://github.com/"
	if !strings.HasPrefix(apiURL, apiPrefix) {
		return repoHTMLURL
	}
	path := strings.TrimPrefix(apiURL, apiPrefix) // "owner/repo/pulls/123"

	// Normalise path segment names to HTML equivalents.
	path = strings.Replace(path, "/pulls/", "/pull/", 1)
	path = strings.Replace(path, "/commits/", "/commit/", 1)

	// Releases: API uses /releases/<id> (numeric), HTML uses /releases/tag/<tag>.
	// We can't easily recover the tag from the ID here, so fall back to the
	// releases index page for the repo.
	if subjectType == "Release" {
		// Strip to repo portion and append /releases.
		parts := strings.SplitN(path, "/", 3)
		if len(parts) >= 2 {
			return htmlPrefix + parts[0] + "/" + parts[1] + "/releases"
		}
		return repoHTMLURL
	}

	return htmlPrefix + path
}

func prItem(issue *gogithub.Issue, reviewRequested bool) plugin.Item {
	repo := ""
	if issue.RepositoryURL != nil {
		url := *issue.RepositoryURL
		lastSlash := -1
		for i := len(url) - 1; i >= 0; i-- {
			if url[i] == '/' {
				if lastSlash == -1 {
					lastSlash = i
				} else {
					repo = url[i+1:]
					break
				}
			}
		}
	}
	meta := issue.GetState()
	if reviewRequested {
		meta = "review requested"
	}
	return plugin.Item{
		Title:       issue.GetTitle(),
		Subtitle:    repo,
		Meta:        meta,
		URL:         issue.GetHTMLURL(),
		Highlighted: reviewRequested,
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func formatReason(reason string) string {
	switch reason {
	case "assign":
		return "assigned"
	case "author":
		return "author"
	case "comment":
		return "comment"
	case "mention":
		return "mentioned"
	case "review_requested":
		return "review requested"
	case "subscribed":
		return "subscribed"
	case "team_mention":
		return "team mention"
	case "ci_activity":
		return "CI"
	case "state_change":
		return "state changed"
	case "approval_requested":
		return "approval requested"
	default:
		return reason
	}
}

func formatType(t string) string {
	switch t {
	case "PullRequest":
		return "PR"
	case "Issue":
		return "issue"
	case "Release":
		return "release"
	case "Discussion":
		return "discussion"
	case "Commit":
		return "commit"
	case "CheckSuite":
		return "CI"
	default:
		return strings.ToLower(t)
	}
}

func isHighPriority(reason string) bool {
	switch reason {
	case "review_requested", "mention", "team_mention", "assign", "approval_requested":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// gh CLI fallback
// ---------------------------------------------------------------------------

type ghNotification struct {
	Reason  string `json:"reason"`
	Subject struct {
		Title string `json:"title"`
		Type  string `json:"type"`
		URL   string `json:"url"`
	} `json:"subject"`
	Repository struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
}

type ghSearchResult struct {
	Title      string `json:"title"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	State string `json:"state"`
	URL   string `json:"url"`
}

func fetchViaGHCLI(ctx context.Context) ([]plugin.Item, error) {
	var items []plugin.Item

	notifItems, err := fetchNotificationsViaGHCLI(ctx)
	if err != nil {
		return nil, err
	}
	items = append(items, notifItems...)

	authorItems, err := runGHSearch(ctx, "is:open is:pr author:@me", false)
	if err != nil {
		return nil, err
	}
	items = append(items, authorItems...)

	reviewItems, err := runGHSearch(ctx, "is:open is:pr review-requested:@me", true)
	if err != nil {
		// Non-fatal: some setups may not support this query.
		return items, nil
	}
	items = append(items, reviewItems...)

	return items, nil
}

func fetchNotificationsViaGHCLI(ctx context.Context) ([]plugin.Item, error) {
	cmd := exec.CommandContext(ctx, "gh", "api",
		"notifications?all=false&per_page=50",
		"--paginate",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("github: gh api notifications failed: %w – %s", err, stderr.String())
	}

	raw := strings.TrimSpace(stdout.String())
	raw = strings.ReplaceAll(raw, "][", ",")

	var notifications []ghNotification
	if err := json.Unmarshal([]byte(raw), &notifications); err != nil {
		return nil, fmt.Errorf("github: parse notifications: %w", err)
	}

	items := make([]plugin.Item, 0, len(notifications))
	for _, n := range notifications {
		items = append(items, plugin.Item{
			Title:       n.Subject.Title,
			Subtitle:    n.Repository.FullName,
			Meta:        formatReason(n.Reason) + " · " + formatType(n.Subject.Type),
			URL:         subjectHTMLURL(n.Subject.URL, n.Subject.Type, n.Repository.HTMLURL),
			Highlighted: isHighPriority(n.Reason),
		})
	}
	return items, nil
}

func runGHSearch(ctx context.Context, query string, reviewRequested bool) ([]plugin.Item, error) {
	cmd := exec.CommandContext(ctx, "gh", "search", "prs", "--json",
		"title,number,repository,state,url", "--limit", "50", query)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("github: gh search prs failed: %w – stderr: %s", err, stderr.String())
	}

	var results []ghSearchResult
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		return nil, fmt.Errorf("github: failed to parse gh output: %w", err)
	}

	items := make([]plugin.Item, 0, len(results))
	for _, r := range results {
		meta := r.State
		if reviewRequested {
			meta = "review requested"
		}
		items = append(items, plugin.Item{
			Title:       r.Title,
			Subtitle:    r.Repository.NameWithOwner,
			Meta:        meta,
			URL:         r.URL,
			Highlighted: reviewRequested,
		})
	}
	return items, nil
}
