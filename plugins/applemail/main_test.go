package main

import (
	"testing"
)

func TestParseSender(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"John Doe <john@example.com>", "John Doe"},
		{"<john@example.com>", "<john@example.com>"},
		{"plain email", "plain email"},
	}

	for _, test := range tests {
		if got := parseSender(test.input); got != test.expected {
			t.Errorf("parseSender(%q) = %q; want %q", test.input, got, test.expected)
		}
	}
}

func TestFormatMeta(t *testing.T) {
	// Need to be careful with relative time in formatMeta
	// The function uses time.Now()
	// Let's test with a fixed known date or mockable time if possible.
	// Actually, formatMeta uses time.Local.

	// Test for a date in the past
	input := "2023-01-01T10:00:00"
	expected := "01/01/23"
	if got := formatMeta(input); got != expected {
		t.Errorf("formatMeta(%q) = %q; want %q", input, got, expected)
	}
}

func TestEscapeAS(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Exchange", "Exchange"},
		{`"Test"`, `\"Test\"`},
		{`\slash`, `\\slash`},
	}

	for _, test := range tests {
		if got := escapeAS(test.input); got != test.expected {
			t.Errorf("escapeAS(%q) = %q; want %q", test.input, got, test.expected)
		}
	}
}

func TestParseMessages(t *testing.T) {
	raw := "123\x1eHello World\x1eSender <sender@example.com>\x1e2023-01-01T10:00:00\x1efalse\x1efalse\x1f" +
		"456\x1e\x1eSender <sender@example.com>\x1e2023-01-01T10:00:00\x1etrue\x1etrue\x1f"
	
	items := parseMessages(raw)
	
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	
	if items[0].Title != "Hello World" {
		t.Errorf("expected Title 'Hello World', got %q", items[0].Title)
	}
	if items[0].URL != "message://%3C123%3E" {
		t.Errorf("expected URL 'message://%%3C123%%3E', got %q", items[0].URL)
	}
	if !items[0].Highlighted {
		t.Error("expected Highlighted to be true for unread message")
	}

	if items[1].Title != "(no subject)" {
		t.Errorf("expected Title '(no subject)', got %q", items[1].Title)
	}
	if !items[1].Highlighted {
		t.Error("expected Highlighted to be true for flagged message")
	}
}
