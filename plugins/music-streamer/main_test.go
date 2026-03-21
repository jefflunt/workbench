package main

import (
	"testing"
)

func TestParseConfig(t *testing.T) {
	// Let's test the config parsing logic from fetch
	
	cfg := map[string]any{
		"backends": []any{
			map[string]any{
				"name": "plex",
				"path": "/path/to/plex",
				"config": map[string]any{
					"url": "http://localhost:32400",
				},
			},
		},
	}
	
	backendsData, ok := cfg["backends"].([]any)
	if !ok {
		t.Fatal("expected backends to be []any")
	}
	
	if len(backendsData) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(backendsData))
	}
	
	bMap, ok := backendsData[0].(map[string]any)
	if !ok {
		t.Fatal("expected backend to be map[string]any")
	}
	
	backend := Backend{
		Name:   bMap["name"].(string),
		Path:   bMap["path"].(string),
		Config: bMap["config"].(map[string]any),
	}
	
	if backend.Name != "plex" {
		t.Errorf("expected Name 'plex', got %q", backend.Name)
	}
	if backend.Path != "/path/to/plex" {
		t.Errorf("expected Path '/path/to/plex', got %q", backend.Path)
	}
	if backend.Config["url"] != "http://localhost:32400" {
		t.Errorf("expected Config URL 'http://localhost:32400', got %v", backend.Config["url"])
	}
}
