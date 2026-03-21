package media

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jluntpcty/workbench/internal/log"
	"github.com/jluntpcty/workbench/internal/plugin"
)

// Handler processes a specific media URL scheme and translates it into
// playable targets and corresponding queue items.
type Handler func(urlStr string, item plugin.Item) ([]plugin.Item, []string, error)

var registry = map[string]Handler{}

func init() {
	// Register the built-in media handlers.
	registry["ytm"] = handleYTM
	registry["ytm-playlist"] = handleYTMPlaylist
	registry["plex"] = handlePlex
	registry["plex-playlist"] = handlePlexPlaylist
}

// Resolve looks up the registered handler for the given URL and returns the
// translated playback queue and stream targets.
//
// Expects urls in the format `music://<scheme>/<data>`
func Resolve(urlStr string, baseItem plugin.Item) ([]plugin.Item, []string, error) {
	// Trim the music:// or music-shuffle:// prefix.
	schemeRaw := urlStr
	if strings.HasPrefix(schemeRaw, "music://") {
		schemeRaw = strings.TrimPrefix(schemeRaw, "music://")
	} else if strings.HasPrefix(schemeRaw, "music-shuffle://") {
		schemeRaw = strings.TrimPrefix(schemeRaw, "music-shuffle://")
	} else {
		return nil, nil, fmt.Errorf("unsupported media URL prefix: %s", urlStr)
	}

	parts := strings.SplitN(schemeRaw, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return nil, nil, fmt.Errorf("invalid media URL format: %s", urlStr)
	}

	scheme := parts[0]
	handler, ok := registry[scheme]
	if !ok {
		return nil, nil, fmt.Errorf("no media handler registered for scheme: %s", scheme)
	}

	return handler(urlStr, baseItem)
}

func handleYTM(urlStr string, item plugin.Item) ([]plugin.Item, []string, error) {
	id := extractData(urlStr, "ytm")
	queue := []plugin.Item{item}
	targets := []string{"https://music.youtube.com/watch?v=" + id}
	return queue, targets, nil
}

func handleYTMPlaylist(urlStr string, item plugin.Item) ([]plugin.Item, []string, error) {
	id := extractData(urlStr, "ytm-playlist")
	queue := []plugin.Item{item} // Placeholder for playlist
	targets := []string{id}
	return queue, targets, nil
}

func handlePlex(urlStr string, item plugin.Item) ([]plugin.Item, []string, error) {
	data := extractData(urlStr, "plex")
	queue := []plugin.Item{item}
	targets := []string{data}
	return queue, targets, nil
}

func handlePlexPlaylist(urlStr string, item plugin.Item) ([]plugin.Item, []string, error) {
	data := extractData(urlStr, "plex-playlist")
	parts := strings.SplitN(data, "/", 2)
	if len(parts) != 2 {
		return nil, nil, fmt.Errorf("invalid plex-playlist format")
	}

	serverURL, err := url.QueryUnescape(parts[0])
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode plex server URL: %w", err)
	}
	relPath := parts[1]
	fullURL := serverURL + "/" + relPath

	log.Info("media", fmt.Sprintf("expanding plex playlist: %s", fullURL))
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create plex request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("plex expansion failed: %w", err)
	}
	defer resp.Body.Close()

	var pr struct {
		MediaContainer struct {
			Metadata []struct {
				Title string `json:"title"`
				Media []struct {
					Part []struct {
						Key string `json:"key"`
					} `json:"part"`
				} `json:"media"`
			} `json:"metadata"`
		} `json:"MediaContainer"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, nil, fmt.Errorf("plex decode failed: %w", err)
	}

	u, err := url.Parse(fullURL)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse full plex URL: %w", err)
	}
	token := u.Query().Get("X-Plex-Token")

	var queue []plugin.Item
	var targets []string

	for _, m := range pr.MediaContainer.Metadata {
		if len(m.Media) > 0 && len(m.Media[0].Part) > 0 {
			trackURL := fmt.Sprintf("%s%s?X-Plex-Token=%s", serverURL, m.Media[0].Part[0].Key, token)
			targets = append(targets, trackURL)
			queue = append(queue, plugin.Item{Title: m.Title, URL: "music://plex/" + trackURL})
		}
	}

	if len(targets) == 0 {
		return nil, nil, fmt.Errorf("expanded plex playlist but found 0 tracks")
	}

	log.Info("media", fmt.Sprintf("expanded plex playlist to %d tracks", len(targets)))
	return queue, targets, nil
}

func extractData(urlStr, scheme string) string {
	base := "music://"
	if strings.HasPrefix(urlStr, "music-shuffle://") {
		base = "music-shuffle://"
	}
	return strings.TrimPrefix(urlStr, base+scheme+"/")
}
