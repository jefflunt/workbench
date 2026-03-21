package main

import (
	"testing"
)

func TestFormatReason(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"assign", "assigned"},
		{"author", "author"},
		{"comment", "comment"},
		{"mention", "mentioned"},
		{"review_requested", "review requested"},
		{"subscribed", "subscribed"},
		{"team_mention", "team mention"},
		{"ci_activity", "CI"},
		{"state_change", "state changed"},
		{"approval_requested", "approval requested"},
		{"unknown_reason", "unknown_reason"},
	}

	for _, test := range tests {
		if got := formatReason(test.input); got != test.expected {
			t.Errorf("formatReason(%q) = %q; want %q", test.input, got, test.expected)
		}
	}
}

func TestFormatType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"PullRequest", "PR"},
		{"Issue", "issue"},
		{"Release", "release"},
		{"Discussion", "discussion"},
		{"Commit", "commit"},
		{"CheckSuite", "CI"},
		{"Unknown", "unknown"},
	}

	for _, test := range tests {
		if got := formatType(test.input); got != test.expected {
			t.Errorf("formatType(%q) = %q; want %q", test.input, got, test.expected)
		}
	}
}

func TestIsHighPriority(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"review_requested", true},
		{"mention", true},
		{"team_mention", true},
		{"assign", true},
		{"approval_requested", true},
		{"author", false},
		{"comment", false},
	}

	for _, test := range tests {
		if got := isHighPriority(test.input); got != test.expected {
			t.Errorf("isHighPriority(%q) = %v; want %v", test.input, got, test.expected)
		}
	}
}
