package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/jluntpcty/workbench/internal/plugin"
)

// PlexResponse is a simplified Plex API response.
type PlexResponse struct {
	MediaContainer struct {
		Directory []struct {
			Key   string `json:"key"`
			Title string `json:"title"`
		} `json:"Directory"`
		Metadata []struct {
			RatingKey        string `json:"ratingKey"`
			Title            string `json:"title"`
			ParentTitle      string `json:"parentTitle"`      // Album
			GrandparentTitle string `json:"grandparentTitle"` // Artist
			LibrarySectionID int    `json:"librarySectionID"`
			Type             string `json:"type"`
			Media            []struct {
				Part []struct {
					Key string `json:"key"`
				} `json:"part"`
			} `json:"media"`
		} `json:"metadata"`
	} `json:"MediaContainer"`
}

func main() {
	plugin.RunPlugin(fetch)
}

func fetch(cfg map[string]any) ([]plugin.Item, error) {
	serverURL, _ := cfg["server_url"].(string)
	token, _ := cfg["token"].(string)
	library, _ := cfg["library"].(string)

	if serverURL == "" || token == "" {
		return nil, fmt.Errorf("plex: server_url and token are required in [plugins.plex]")
	}

	query, _ := cfg["query"].(string)
	fmt.Fprintf(os.Stderr, "plex: fetching with query %q, library %q\n", query, library)

	sectionID := ""
	if library != "" {
		var err error
		sectionID, err = findSectionID(serverURL, token, library)
		if err != nil {
			return nil, err
		}
		if sectionID == "" {
			return nil, fmt.Errorf("plex: library %q not found", library)
		}
	}

	if query != "" {
		return performSearch(serverURL, token, query, sectionID)
	}

	return fetchPlaylists(serverURL, token)
}

func findSectionID(serverURL, token, name string) (string, error) {
	u, _ := url.Parse(serverURL)
	u.Path = "/library/sections"
	q := u.Query()
	q.Set("X-Plex-Token", token)
	u.RawQuery = q.Encode()

	resp, err := getPlex(u.String())
	if err != nil {
		return "", err
	}

	for _, d := range resp.MediaContainer.Directory {
		if strings.EqualFold(d.Title, name) {
			return d.Key, nil
		}
	}
	return "", nil
}

func performSearch(serverURL, token, query, sectionID string) ([]plugin.Item, error) {
	u, _ := url.Parse(serverURL)
	if sectionID != "" {
		u.Path = fmt.Sprintf("/library/sections/%s/search", sectionID)
	} else {
		u.Path = "/search"
	}
	q := u.Query()
	q.Set("query", query)
	q.Set("type", "10")
	q.Set("X-Plex-Token", token)
	u.RawQuery = q.Encode()

	resp, err := getPlex(u.String())
	if err != nil {
		return nil, err
	}

	var items []plugin.Item
	for _, m := range resp.MediaContainer.Metadata {
		if len(m.Media) == 0 || len(m.Media[0].Part) == 0 {
			continue
		}

		streamURL := fmt.Sprintf("%s%s?X-Plex-Token=%s", serverURL, m.Media[0].Part[0].Key, token)

		items = append(items, plugin.Item{
			Title:    m.Title,
			Subtitle: fmt.Sprintf("%s — %s", m.GrandparentTitle, m.ParentTitle),
			Meta:     "Track",
			URL:      "music://plex/" + streamURL,
		})
	}
	return items, nil
}

func fetchPlaylists(serverURL, token string) ([]plugin.Item, error) {
	u, _ := url.Parse(serverURL)
	u.Path = "/playlists"
	q := u.Query()
	q.Set("playlistType", "audio")
	q.Set("X-Plex-Token", token)
	u.RawQuery = q.Encode()

	resp, err := getPlex(u.String())
	if err != nil {
		return nil, err
	}

	var items []plugin.Item
	for _, m := range resp.MediaContainer.Metadata {
		// Note: Plex playlists need to be expanded to their tracks,
		// but for a single entry we'll return the playlist item.
		// In a real implementation we might want a custom music://plex-playlist/...
		// that mpv handles by expanding, but for now we'll just return the entries.
		items = append(items, plugin.Item{
			Title:    m.Title,
			Subtitle: "Playlist",
			Meta:     "Plex",
			URL:      "", // We'd need an endpoint for the playlist's tracks to play them all
		})
	}
	return items, nil
}

func getPlex(url string) (*PlexResponse, error) {
	client := &http.Client{}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("plex API error: %s", resp.Status)
	}

	var pr PlexResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("plex: failed to decode JSON: %w", err)
	}
	return &pr, nil
}
