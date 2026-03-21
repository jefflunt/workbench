package cache

import (
	"os"
	"testing"

	"github.com/jluntpcty/workbench/internal/plugin"
)

func TestCacheSaveAndLoad(t *testing.T) {
	// Setup a temporary directory for cache
	tmpDir, err := os.MkdirTemp("", "workbench_cache_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Override dir function for testing
	// This is a bit tricky because dir() is not exported and uses os.UserCacheDir()
	// Maybe just test with the real file system but in a temp directory,
	// and ensure the test cleans up.
	// The current implementation of dir() is:
	// base, err := os.UserCacheDir() ... filepath.Join(base, "workbench")

	// I can't easily override dir() from here without refactoring.
	// Let's just create a test that uses the actual cache dir,
	// but maybe use a provider name that's unlikely to conflict,
	// and clean up afterwards.

	providerName := "test_provider"

	items := []plugin.Item{
		{Title: "Test Item", Subtitle: "Subtitle"},
	}

	err = Save(providerName, items)
	if err != nil {
		t.Fatal(err)
	}

	loadedItems, err := Load(providerName)
	if err != nil {
		t.Fatal(err)
	}

	if len(loadedItems) != 1 || loadedItems[0].Title != "Test Item" {
		t.Errorf("expected 1 item, got %d", len(loadedItems))
	}
}
