// Package plugin defines the shared types and helpers for the workbench plugin
// system.
//
// # Protocol
//
// A workbench plugin is a standalone executable that follows this contract:
//
//  1. workbench spawns the binary with the single argument "fetch".
//  2. workbench writes a JSON-encoded FetchRequest to the binary's stdin.
//  3. The plugin reads stdin, performs its fetch, then writes a JSON-encoded
//     FetchResponse to stdout and exits with code 0 on success or non-zero on
//     error.
//
// Plugins may write arbitrary text to stderr; workbench captures it and
// surfaces it as the error message when the exit code is non-zero.
//
// # Discovery
//
// workbench scans ~/.config/workbench/plugins/ (or
// $XDG_CONFIG_HOME/workbench/plugins/) at startup.  Every executable file in
// that directory is treated as a plugin; the plugin name is the filename
// (e.g. "github", "jira", "applemail").
//
// # Configuration
//
// Plugin-specific config lives under [plugins.<name>] in config.toml.
// The value is decoded as map[string]any and passed as FetchRequest.Config.
// Plugins are responsible for reading whatever keys they need.
package plugin

import "context"

// Item is a single displayable entry returned by a plugin.
// The fields map directly to the three visible columns in a workbench pane.
type Item struct {
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	Meta        string `json:"meta"`
	URL         string `json:"url,omitempty"`
	Highlighted bool   `json:"highlighted,omitempty"`
}

// FetchRequest is written to a plugin's stdin when workbench invokes it.
type FetchRequest struct {
	// Config is the plugin's configuration map from [plugins.<name>] in
	// config.toml.  Keys and value types are defined entirely by each plugin.
	Config map[string]any `json:"config"`

	// Query is an optional search term. When provided, the plugin should return
	// live search results instead of its default item list.
	Query string `json:"query,omitempty"`
}

// FetchResponse is what a plugin must write to stdout before exiting.
type FetchResponse struct {
	// Items is the list of items to display in the pane.
	Items []Item `json:"items"`
	// Error, if non-empty, is treated as a fetch failure.  The plugin should
	// also exit with a non-zero code, but workbench checks this field too.
	Error string `json:"error,omitempty"`
}

// Provider is the interface workbench uses internally to talk to plugins.
// It mirrors internal/provider.Provider but uses plugin.Item instead of
// provider.Item so that main.go does not need to import both packages.
type Provider interface {
	Name() string
	Fetch(ctx context.Context, query string) ([]Item, error)
}
