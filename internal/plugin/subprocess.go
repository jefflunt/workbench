package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// SubprocessProvider implements Provider by spawning a plugin binary as a
// child process, writing a FetchRequest to its stdin, and decoding the
// FetchResponse from its stdout.
//
// The binary must be an absolute path to an executable that follows the
// workbench plugin protocol (see package-level doc).
type SubprocessProvider struct {
	// name is the plugin name (== binary filename, == PaneConfig.Provider).
	name string
	// binaryPath is the absolute path to the plugin executable.
	binaryPath string
	// config is the plugin's config map from [plugins.<name>] in config.toml.
	config map[string]any
}

// NewSubprocessProvider returns a SubprocessProvider for the plugin binary at
// binaryPath.  name must match the filename and the value used in layout
// config; config is passed verbatim to the plugin as FetchRequest.Config.
func NewSubprocessProvider(name, binaryPath string, config map[string]any) *SubprocessProvider {
	if config == nil {
		config = map[string]any{}
	}
	return &SubprocessProvider{
		name:       name,
		binaryPath: binaryPath,
		config:     config,
	}
}

// Name implements Provider.
func (p *SubprocessProvider) Name() string { return p.name }

// Fetch implements Provider.  It spawns the plugin binary with the "fetch"
// argument, writes a FetchRequest JSON to stdin, and reads a FetchResponse
// JSON from stdout.  Stderr is captured and returned as part of any error
// message.
func (p *SubprocessProvider) Fetch(ctx context.Context) ([]Item, error) {
	req := FetchRequest{Config: p.config}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: encode request: %w", p.name, err)
	}

	cmd := exec.CommandContext(ctx, p.binaryPath, "fetch") //nolint:gosec
	cmd.Stdin = bytes.NewReader(reqBytes)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := stderr.String()
		if stderrStr != "" {
			return nil, fmt.Errorf("plugin %s: %s", p.name, stderrStr)
		}
		return nil, fmt.Errorf("plugin %s: %w", p.name, err)
	}

	var resp FetchResponse
	if err := json.NewDecoder(&stdout).Decode(&resp); err != nil {
		return nil, fmt.Errorf("plugin %s: decode response: %w", p.name, err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("plugin %s: %s", p.name, resp.Error)
	}

	return resp.Items, nil
}
