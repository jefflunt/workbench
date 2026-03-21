package main

import (
	"encoding/json"
	"testing"
)

func TestParseJiraOutput(t *testing.T) {
	raw := `[
		{
			"key": "PROJ-1",
			"fields": {
				"summary": "Fix bug",
				"priority": { "name": "High" },
				"status": { "name": "In Progress" }
			}
		},
		{
			"key": "PROJ-2",
			"fields": {
				"summary": "Do work",
				"status": { "name": "Blocked" }
			}
		}
	]`

	var workItems []workItem
	if err := json.Unmarshal([]byte(raw), &workItems); err != nil {
		t.Fatal(err)
	}

	if len(workItems) != 2 {
		t.Fatalf("expected 2 items, got %d", len(workItems))
	}

	if workItems[0].Fields.Summary != "Fix bug" {
		t.Errorf("expected Title 'Fix bug', got %q", workItems[0].Fields.Summary)
	}
	if workItems[0].Key != "PROJ-1" {
		t.Errorf("expected Subtitle 'PROJ-1', got %q", workItems[0].Key)
	}
    
    // Testing the logic from runQuery
    status := workItems[0].Fields.Status.Name
    priority := workItems[0].Fields.Priority.Name
    meta := status
    if priority != "" {
        meta = priority + " · " + status
    }
	if meta != "High · In Progress" {
		t.Errorf("expected Meta 'High · In Progress', got %q", meta)
	}
	if needsFollowUp[status] {
		t.Error("expected Highlighted to be false for 'In Progress'")
	}

    status2 := workItems[1].Fields.Status.Name
    priority2 := workItems[1].Fields.Priority.Name
    meta2 := status2
    if priority2 != "" {
        meta2 = priority2 + " · " + status2
    }
	if meta2 != "Blocked" {
		t.Errorf("expected Meta 'Blocked', got %q", meta2)
	}
	if !needsFollowUp[status2] {
		t.Error("expected Highlighted to be true for 'Blocked'")
	}
}
