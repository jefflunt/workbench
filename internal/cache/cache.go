// Package cache persists provider items to disk so that workbench can show
// stale data immediately on startup while fresh data loads in the background.
//
// Each provider gets its own JSON file under:
//
//	$XDG_CACHE_HOME/workbench/<provider>.json   (or ~/.cache/workbench/)
package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/jluntpcty/workbench/internal/plugin"
)

// entry is the on-disk structure for a single provider's cached data.
type entry struct {
	UpdatedAt time.Time     `json:"updated_at"`
	Items     []plugin.Item `json:"items"`
}

// dir returns (and creates if necessary) the workbench cache directory.
func dir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	d := filepath.Join(base, "workbench")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	return d, nil
}

func filePath(providerName string) (string, error) {
	d, err := dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, providerName+".json"), nil
}

// Load reads cached items for the named provider.  It returns nil (no error)
// when no cache file exists yet.
func Load(providerName string) ([]plugin.Item, error) {
	path, err := filePath(providerName)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var e entry
	if err := json.Unmarshal(data, &e); err != nil {
		// Corrupt cache — treat as empty rather than erroring.
		return nil, nil
	}
	return e.Items, nil
}

// Save writes items for the named provider to disk, overwriting any existing
// cache file.  Errors are non-fatal; a failed write is silently ignored by
// callers.
func Save(providerName string, items []plugin.Item) error {
	path, err := filePath(providerName)
	if err != nil {
		return err
	}

	e := entry{
		UpdatedAt: time.Now(),
		Items:     items,
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}

	// Write atomically via a temp file so a crash never leaves a partial file.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
