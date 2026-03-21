package log

import (
	"testing"
)

func TestTruncPane(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"mail", "mail"},
		{"github", "gith"},
		{"a", "a"},
		{"verylongname", "very"},
	}

	for _, test := range tests {
		if got := truncPane(test.input); got != test.expected {
			t.Errorf("truncPane(%q) = %q; want %q", test.input, got, test.expected)
		}
	}
}

func TestWrapText(t *testing.T) {
	tests := []struct {
		text     string
		width    int
		expected []string
	}{
		{"hello world", 5, []string{"hello", "world"}},
		{"hello world", 10, []string{"hello", "world"}},
		{"short", 10, []string{"short"}},
		{"verylongwordthatneedswrapping", 5, []string{"veryl", "ongwo", "rdtha", "tneed", "swrap", "ping"}},
	}

	for _, test := range tests {
		got := wrapText(test.text, test.width)
		if len(got) != len(test.expected) {
			t.Fatalf("wrapText(%q, %d) returned %d lines; want %d", test.text, test.width, len(got), len(test.expected))
		}
		for i := range got {
			if got[i] != test.expected[i] {
				t.Errorf("wrapText(%q, %d)[%d] = %q; want %q", test.text, test.width, i, got[i], test.expected[i])
			}
		}
	}
}
