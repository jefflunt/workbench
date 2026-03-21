package plugin

import (
	"encoding/json"
	"fmt"
	"os"
)

// RunPlugin is the entrypoint helper for plugin binaries.  Call it from
// main() passing a fetch function that accepts a config map and returns items
// or an error.
//
// Usage in a plugin binary:
//
//	func main() {
//	    plugin.RunPlugin(func(cfg map[string]any) ([]plugin.Item, error) {
//	        // read cfg, do work, return items
//	    })
//	}
//
// RunPlugin reads a FetchRequest from stdin, calls fetch, writes a
// FetchResponse to stdout, and exits.  Any error causes a non-zero exit.
func RunPlugin(fetch func(cfg map[string]any) ([]Item, error)) {
	if len(os.Args) < 2 || os.Args[1] != "fetch" {
		fmt.Fprintf(os.Stderr, "usage: %s fetch\n", os.Args[0])
		os.Exit(2)
	}

	var req FetchRequest
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "plugin: decode request: %v\n", err)
		os.Exit(1)
	}

	items, err := fetch(req.Config)

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
