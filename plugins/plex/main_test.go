package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPlexConnection(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("X-Plex-Token") != "mytoken" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"MediaContainer":{}}`))
	}))
	defer ts.Close()

	err := checkPlexConnection(ts.URL, "mytoken")
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	err = checkPlexConnection(ts.URL, "wrongtoken")
	if err == nil {
		t.Error("Expected error with wrong token, got nil")
	}
}

func TestFetchPlaylists(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := PlexResponse{}
		resp.MediaContainer.Metadata = append(resp.MediaContainer.Metadata, struct {
			RatingKey        string `json:"ratingKey"`
			Title            string `json:"title"`
			ParentTitle      string `json:"parentTitle"`
			GrandparentTitle string `json:"grandparentTitle"`
			LibrarySectionID int    `json:"librarySectionID"`
			Type             string `json:"type"`
			Media            []struct {
				Part []struct {
					Key string `json:"key"`
				} `json:"part"`
			} `json:"media"`
		}{
			RatingKey: "1234",
			Title:     "My Awesome Playlist",
		})

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	items, err := fetchPlaylists(ts.URL, "mytoken")
	if err != nil {
		t.Fatalf("Expected nil error, got %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("Expected 1 item, got %d", len(items))
	}

	if items[0].Title != "My Awesome Playlist" {
		t.Errorf("Expected Title 'My Awesome Playlist', got %q", items[0].Title)
	}
	if items[0].Subtitle != "Playlist" {
		t.Errorf("Expected Subtitle 'Playlist', got %q", items[0].Subtitle)
	}
	if items[0].Meta != "Plex" {
		t.Errorf("Expected Meta 'Plex', got %q", items[0].Meta)
	}
}
