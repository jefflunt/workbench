package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

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
	plugin.RunPlugin(fetch, expand)
}

func fetch(cfg map[string]any, query string) ([]plugin.Item, error) {
	serverURL, _ := cfg["server_url"].(string)
	token, _ := cfg["token"].(string)
	library, _ := cfg["library"].(string)

	if serverURL == "" || token == "" {
		return nil, fmt.Errorf("plex: server_url and token are required in [plugins.plex]")
	}

	fmt.Fprintf(os.Stderr, "plex: fetching with query %q, library %q\n", query, library)

	sectionID := ""
	if library != "" {
		var err error
		sectionID, err = findSectionID(serverURL, token, library)
		if err != nil {
			fmt.Fprintf(os.Stderr, "plex: error finding library %q: %v\n", library, err)
		}
	}

	if query != "" {
		return performSearch(serverURL, token, query, sectionID)
	}

	// Default: Check connectivity and return playlists
	err := checkPlexConnection(serverURL, token)
	if err != nil {
		return []plugin.Item{{
			Title:       "🟧 Plex",
			Subtitle:    "Connection Error",
			Meta:        "ERROR",
			URL:         "",
			Highlighted: true,
		}}, nil
	}

	playlists, err := fetchPlaylists(serverURL, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plex: error fetching playlists: %v\n", err)
	}

	status := "OK"
	if library != "" {
		if sectionID != "" {
			status = fmt.Sprintf("OK (%s)", library)
		} else {
			status = fmt.Sprintf("OK (Library %q not found)", library)
		}
	}

	// Add status as the first item
	items := []plugin.Item{{
		Title:       "🟧 Plex Server",
		Subtitle:    "Connected",
		Meta:        status,
		URL:         "",
		Highlighted: false,
	}}
	items = append(items, playlists...)

	return items, nil
}

func expand(cfg map[string]any, item plugin.Item) ([]plugin.Item, error) {
	serverURL, _ := cfg["server_url"].(string)
	token, _ := cfg["token"].(string)
	if serverURL == "" || token == "" {
		return nil, fmt.Errorf("plex: server_url and token are required")
	}

	if strings.HasPrefix(item.URL, "music://plex-playlist/") {
		parts := strings.SplitN(strings.TrimPrefix(item.URL, "music://plex-playlist/"), "/", 2)
		if len(parts) == 2 {
			serverURL, _ := url.QueryUnescape(parts[0])
			relPath := parts[1]
			fullURL := serverURL + "/" + relPath
			return fetchPlexPlaylistItems(fullURL, serverURL, token)
		}
	}
	return nil, nil
}

func fetchPlexPlaylistItems(fullURL, serverURL, token string) ([]plugin.Item, error) {
	resp, err := getPlex(fullURL)
	if err != nil {
		return nil, err
	}
	var items []plugin.Item
	for _, m := range resp.MediaContainer.Metadata {
		if len(m.Media) > 0 && len(m.Media[0].Part) > 0 {
			streamURL := fmt.Sprintf("%s%s?X-Plex-Token=%s", serverURL, m.Media[0].Part[0].Key, token)
			items = append(items, plugin.Item{
				Title:    "🟧 " + m.Title,
				Subtitle: fmt.Sprintf("%s — %s", m.GrandparentTitle, m.ParentTitle),
				Meta:     "Track",
				URL:      "music://plex/" + streamURL,
			})
		}
	}
	return items, nil
}

func checkPlexConnection(serverURL, token string) error {
	u, _ := url.Parse(serverURL)
	u.Path = "/"
	q := u.Query()
	q.Set("X-Plex-Token", token)
	u.RawQuery = q.Encode()

	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", u.String(), nil)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
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
	q.Set("limit", "100")
	// 8=Artist, 9=Album, 10=Track, 15=Playlist
	q.Set("type", "8,9,10,15")
	q.Set("X-Plex-Token", token)
	u.RawQuery = q.Encode()

	resp, err := getPlex(u.String())
	if err != nil {
		return nil, err
	}

	var items []plugin.Item
	for _, m := range resp.MediaContainer.Metadata {
		item := plugin.Item{
			Title: "🟧 " + m.Title,
		}

		switch m.Type {
		case "artist":
			item.Subtitle = "Artist"
			item.Meta = "Artist"
			item.URL = fmt.Sprintf("music://plex-playlist/%s/library/metadata/%s/allLeaves?X-Plex-Token=%s", url.PathEscape(serverURL), m.RatingKey, token)
		case "album":
			item.Subtitle = m.ParentTitle // Artist name for albums
			item.Meta = "Album"
			item.URL = fmt.Sprintf("music://plex-playlist/%s/library/metadata/%s/children?X-Plex-Token=%s", url.PathEscape(serverURL), m.RatingKey, token)
		case "track":
			if len(m.Media) > 0 && len(m.Media[0].Part) > 0 {
				item.Subtitle = fmt.Sprintf("%s — %s", m.GrandparentTitle, m.ParentTitle)
				item.Meta = "Track"
				item.URL = "music://plex/" + fmt.Sprintf("%s%s?X-Plex-Token=%s", serverURL, m.Media[0].Part[0].Key, token)
			}
		case "playlist":
			item.Subtitle = "Playlist"
			item.Meta = "Playlist"
			item.URL = fmt.Sprintf("music://plex-playlist/%s/playlists/%s/items?X-Plex-Token=%s", url.PathEscape(serverURL), m.RatingKey, token)
		}

		if item.Subtitle != "" {
			items = append(items, item)
		}
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
		items = append(items, plugin.Item{
			Title:    "🟧 " + m.Title,
			Subtitle: "Playlist",
			Meta:     "Playlist",
			URL:      fmt.Sprintf("music://plex-playlist/%s/%s/items?X-Plex-Token=%s", url.PathEscape(serverURL), m.RatingKey, token),
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
