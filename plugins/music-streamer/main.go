package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/jluntpcty/workbench/internal/plugin"
)

type Backend struct {
	Name   string         `json:"name"`
	Path   string         `json:"path"`
	Config map[string]any `json:"config"`
}

func main() {
	plugin.RunPlugin(fetch)
}

func fetch(cfg map[string]any) ([]plugin.Item, error) {
	backendsData, ok := cfg["backends"].([]any)
	if !ok {
		return nil, fmt.Errorf("music-streamer: backends list required in [plugins.music-streamer]")
	}

	query, _ := cfg["query"].(string)

	var wg sync.WaitGroup
	results := make(chan []plugin.Item, len(backendsData))
	errors := make(chan error, len(backendsData))

	for _, bd := range backendsData {
		bMap, ok := bd.(map[string]any)
		if !ok {
			continue
		}

		backend := Backend{
			Name:   bMap["name"].(string),
			Path:   bMap["path"].(string),
			Config: bMap["config"].(map[string]any),
		}

		wg.Add(1)
		go func(b Backend) {
			defer wg.Done()
			items, err := callBackend(b, query)
			if err != nil {
				errors <- fmt.Errorf("%s: %w", b.Name, err)
			} else {
				results <- items
			}
		}(backend)
	}

	wg.Wait()
	close(results)
	close(errors)

	var allItems []plugin.Item
	for res := range results {
		allItems = append(allItems, res...)
	}

	for err := range errors {
		fmt.Fprintf(os.Stderr, "music-streamer: backend error: %v\n", err)
	}

	return allItems, nil
}

func callBackend(b Backend, query string) ([]plugin.Item, error) {
	req := plugin.FetchRequest{
		Config: b.Config,
		Query:  query,
	}
	reqBytes, _ := json.Marshal(req)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, b.Path, "fetch")
	cmd.Stdin = bytes.NewReader(reqBytes)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("backend error: %w — %s", err, stderr.String())
	}

	var resp plugin.FetchResponse
	if err := json.NewDecoder(&stdout).Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to decode backend response: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("backend error: %s", resp.Error)
	}

	return resp.Items, nil
}
