package plugin

import (
	"encoding/json"
	"fmt"
	"os"
)

// RunPlugin is the entrypoint helper for plugin binaries.
func RunPlugin(fetch func(cfg map[string]any, query string) ([]Item, error), expand func(cfg map[string]any, item Item) ([]Item, error), delete func(cfg map[string]any, item Item) error) {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <fetch|expand|delete>\n", os.Args[0])
		os.Exit(2)
	}

	mode := os.Args[1]
	var items []Item
	var err error
	var config map[string]any

	switch mode {
	case "fetch":
		var req FetchRequest
		if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
			fmt.Fprintf(os.Stderr, "plugin: decode fetch request: %v\n", err)
			os.Exit(1)
		}
		config = req.Config
		if config == nil {
			config = make(map[string]any)
		}
		items, err = fetch(config, req.Query)

	case "expand":
		if expand == nil {
			fmt.Fprintf(os.Stderr, "plugin: expand not supported\n")
			os.Exit(1)
		}
		var req ExpandRequest
		if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
			fmt.Fprintf(os.Stderr, "plugin: decode expand request: %v\n", err)
			os.Exit(1)
		}
		config = req.Config
		if config == nil {
			config = make(map[string]any)
		}
		items, err = expand(config, req.Item)

	case "delete":
		if delete == nil {
			fmt.Fprintf(os.Stderr, "plugin: delete not supported\n")
			os.Exit(1)
		}
		var req DeleteRequest
		if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
			fmt.Fprintf(os.Stderr, "plugin: decode delete request: %v\n", err)
			os.Exit(1)
		}
		config = req.Config
		if config == nil {
			config = make(map[string]any)
		}
		err = delete(config, req.Item)

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", mode)
		os.Exit(2)
	}

	resp := FetchResponse{Items: items}
	if err != nil {
		resp.Error = err.Error()
	}

	if encErr := json.NewEncoder(os.Stdout).Encode(resp); encErr != nil {
		fmt.Fprintf(os.Stderr, "plugin: encode response: %v\n", encErr)
		os.Exit(1)
	}

	if err != nil {
		os.Exit(1)
	}
}
