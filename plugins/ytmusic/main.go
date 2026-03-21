package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/jluntpcty/workbench/internal/plugin"
)

// YTDLPResult is a simplified yt-dlp JSON output.
type YTDLPResult struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Uploader string `json:"uploader"`
	Duration int    `json:"duration"`
}

func main() {
	plugin.RunPlugin(fetch)
}

func fetch(cfg map[string]any) ([]plugin.Item, error) {
	query, _ := cfg["query"].(string)

	if query == "" {
		// Default to some popular music or similar if no query.
		// For now we'll just return nothing until the user searches.
		return nil, nil
	}

	return performSearch(query)
}

func performSearch(query string) ([]plugin.Item, error) {
	// Search for music only with --no-playlist (unless we want to support playlists later).
	// We'll search specifically in the music category.
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "yt-dlp",
		"ytsearch10:"+query,
		"--dump-json",
		"--no-playlist",
		"--flat-playlist")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("yt-dlp error: %w — %s", err, stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	var items []plugin.Item
	for _, line := range lines {
		if line == "" {
			continue
		}
		var res YTDLPResult
		if err := json.Unmarshal([]byte(line), &res); err != nil {
			continue
		}

		durationStr := fmt.Sprintf("%02d:%02d", res.Duration/60, res.Duration%60)
		items = append(items, plugin.Item{
			Title:    res.Title,
			Subtitle: res.Uploader,
			Meta:     durationStr + " · YTM",
			URL:      "music://ytm/" + res.ID,
		})
	}

	return items, nil
}
