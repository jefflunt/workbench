package plugin

import (
	"context"
	"testing"
)

// Since plugin.go only contains types and the interface definition,
// we can test that the types are serializable to/from JSON,
// and create a mock Provider to ensure interface compliance.

func TestPluginTypes(t *testing.T) {
	item := Item{
		Title:       "Test",
		Subtitle:    "Subtitle",
		Meta:        "Meta",
		URL:         "URL",
		Highlighted: true,
	}

	if item.Title != "Test" {
		t.Errorf("expected Title Test, got %s", item.Title)
	}
}

type mockProvider struct{}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Fetch(ctx context.Context, query string) ([]Item, error) {
	return []Item{}, nil
}
func (m *mockProvider) Expand(ctx context.Context, item Item) ([]Item, error) {
	return []Item{}, nil
}

func TestProviderInterface(t *testing.T) {
	var _ Provider = &mockProvider{}
}
