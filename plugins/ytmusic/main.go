package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jluntpcty/workbench/internal/plugin"
)

// YTDLPResult is a simplified yt-dlp JSON output.
type YTDLPResult struct {
	Type          string `json:"_type"`
	ID            string `json:"id"`
	Title         string `json:"title"`
	Uploader      string `json:"uploader"`
	Duration      int    `json:"duration"`
	PlaylistCount int    `json:"playlist_count"`
	WebpageURL    string `json:"webpage_url"`
}

func main() {
	plugin.RunPlugin(fetch, expand)
}

func expand(cfg map[string]any, item plugin.Item) ([]plugin.Item, error) {
	// YTM doesn't currently support expanding playlists via yt-dlp in this plugin.
	return nil, nil
}

func fetch(cfg map[string]any, query string) ([]plugin.Item, error) {
	fmt.Fprintf(os.Stderr, "ytmusic: fetching with query %q\n", query)

	if query != "" {
		return performSearch(query)
	}

	// Default: Check if yt-dlp is available and connection works
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return []plugin.Item{{
			Title:       "YouTube Music",
			Subtitle:    "yt-dlp not found",
			Meta:        "ERROR",
			Highlighted: true,
		}}, nil
	}

	// Check if we can reach google.com (a proxy for connection)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://www.google.com")
	if err != nil {
		return []plugin.Item{{
			Title:       "YouTube Music",
			Subtitle:    "Network error",
			Meta:        "ERROR",
			Highlighted: true,
		}}, nil
	}
	defer resp.Body.Close()

	return []plugin.Item{{
		Title:       "YouTube Music",
		Subtitle:    "Connected",
		Meta:        "OK",
		Highlighted: false,
	}}, nil
}

func performSearch(query string) ([]plugin.Item, error) {
	ctx := context.Background()
	// Use --flat-playlist and remove --no-playlist to allow playlist results.
	// Add --ignore-errors to skip problematic entries
	cmd := exec.CommandContext(ctx, "yt-dlp",
		"ytsearch10:"+query,
		"--dump-json",
		"--flat-playlist",
		"--ignore-errors")

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

		item := plugin.Item{
			Title:    res.Title,
			Subtitle: res.Uploader,
		}

		if res.Type == "playlist" || res.PlaylistCount > 0 {
			item.Meta = "Playlist · YTM"
			item.URL = "music://ytm-playlist/" + res.WebpageURL
		} else {
			durationStr := fmt.Sprintf("%02d:%02d", res.Duration/60, res.Duration%60)
			item.Meta = durationStr + " · Track · YTM"
			item.URL = "music://ytm/" + res.ID
		}

		items = append(items, item)
	}

	return items, nil
}
