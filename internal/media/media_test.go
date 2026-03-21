package media

import (
	"testing"

	"github.com/jluntpcty/workbench/internal/plugin"
)

func TestExtractData(t *testing.T) {
	data := extractData("music://ytm/123", "ytm")
	if data != "123" {
		t.Errorf("Expected 123, got %s", data)
	}

	data = extractData("music-shuffle://plex/456", "plex")
	if data != "456" {
		t.Errorf("Expected 456, got %s", data)
	}
}

func TestResolveYTM(t *testing.T) {
	queue, targets, err := Resolve("music://ytm/abcdef", plugin.Item{Title: "My Song"})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(queue) != 1 || queue[0].Title != "My Song" {
		t.Errorf("Unexpected queue: %v", queue)
	}

	if len(targets) != 1 || targets[0] != "https://music.youtube.com/watch?v=abcdef" {
		t.Errorf("Unexpected targets: %v", targets)
	}
}

func TestResolveInvalid(t *testing.T) {
	_, _, err := Resolve("https://google.com", plugin.Item{})
	if err == nil {
		t.Error("Expected error for non-music URL")
	}

	_, _, err = Resolve("music://unknown/123", plugin.Item{})
	if err == nil {
		t.Error("Expected error for unknown scheme")
	}
}
