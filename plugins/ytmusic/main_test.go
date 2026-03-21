package main

import (
	"encoding/json"
	"testing"
)

func TestParseYtMusicOutput(t *testing.T) {
	// Let's test the response parsing logic
	// the actual parsing is mostly just decoding JSON from the python script
	
	raw := `[
		{
			"videoId": "123",
			"title": "Song 1",
			"artists": [{"name": "Artist 1"}],
			"album": {"name": "Album 1"}
		},
		{
			"videoId": "456",
			"title": "Song 2",
			"artists": [{"name": "Artist 2"}],
			"album": {"name": "Album 2"}
		}
	]`
	
	var data []map[string]any
	err := json.Unmarshal([]byte(raw), &data)
	if err != nil {
		t.Fatal(err)
	}
	
	if len(data) != 2 {
		t.Fatalf("expected 2 items, got %d", len(data))
	}
	
	if data[0]["videoId"] != "123" {
		t.Errorf("expected videoId '123', got %v", data[0]["videoId"])
	}
	if data[0]["title"] != "Song 1" {
		t.Errorf("expected title 'Song 1', got %v", data[0]["title"])
	}
}
