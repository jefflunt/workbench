package layout

import (
	"testing"
)

func TestRender(t *testing.T) {
	rows := []RowConfig{
		{
			Weight: 1,
			Panes: []PaneConfig{
				{Provider: "p1", Weight: 1},
				{Provider: "p2", Weight: 1},
			},
		},
	}
	views := map[string]string{
		"p1": "content1",
		"p2": "content2",
	}
	titles := map[string]string{
		"p1": "title1",
		"p2": "title2",
	}
	activeProvider := "p1"
	activeBorderColor := "212"
	termW := 40
	termH := 10
	reservedRows := 0

	output, dims := Render(rows, views, titles, activeProvider, activeBorderColor, termW, termH, reservedRows)

	if output == "" {
		t.Error("expected non-empty output")
	}

	if _, ok := dims["p1"]; !ok {
		t.Error("expected p1 in dims")
	}

	if dims["p1"].Width != 20 {
		t.Errorf("expected width 20, got %d", dims["p1"].Width)
	}

	// Border takes 2 rows (top+bottom), and padding takes 2 columns (left+right).
	// Height: 10 rows. Inner height = 10-2 = 8.
	if dims["p1"].ContentHeight != 8 {
		t.Errorf("expected content height 8, got %d", dims["p1"].ContentHeight)
	}
}
