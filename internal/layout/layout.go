// Package layout implements the weight-based pane layout engine.
//
// lipgloss has no built-in grid or flexbox system.  This package converts the
// weight ratios declared in config.toml into integer cell dimensions and then
// assembles the final view string using lipgloss.JoinHorizontal /
// lipgloss.JoinVertical.
package layout

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// PaneView is a fully-rendered string for a single pane together with its
// layout metadata.
type PaneView struct {
	Provider string
	Content  string
}

// RowConfig describes one horizontal band of panes.
type RowConfig struct {
	Weight int
	Panes  []PaneConfig
}

// PaneConfig describes a single pane within a row.
type PaneConfig struct {
	Provider string
	Weight   int
}

// PaneDims holds the computed pixel dimensions for a single pane.
type PaneDims struct {
	// ContentHeight is the number of content rows available inside the border
	// and padding — i.e. what renderPane should fill.
	ContentHeight int
	Width         int
}

// Render assembles the complete terminal view from pre-rendered pane strings.
//
// rows is the ordered list of row descriptors from config.toml.
// views maps provider name → rendered string for this frame.
// titles maps provider name → title to display in the border.
// activeProvider is the provider name of the currently focused pane.
// activeBorderColor is the terminal color used for the active pane border.
// termW and termH are the current terminal dimensions in cells.
// reservedRows is the number of rows taken by headers/footers (not part of the
// pane area).
//
// It also returns a map of provider name → PaneDims so callers can know the
// usable content height for each pane before rendering.
func Render(rows []RowConfig, views map[string]string, titles map[string]string, activeProvider, activeBorderColor string, termW, termH, reservedRows int) (string, map[string]PaneDims) {
	paneH := termH - reservedRows
	if paneH < 1 {
		paneH = 1
	}

	// Sum row weights to compute each row's height.
	totalRowWeight := 0
	for _, r := range rows {
		totalRowWeight += r.Weight
	}
	if totalRowWeight == 0 {
		totalRowWeight = 1
	}

	dims := make(map[string]PaneDims)
	renderedRows := make([]string, 0, len(rows))

	for _, row := range rows {
		rowHeight := int(float64(paneH) * float64(row.Weight) / float64(totalRowWeight))
		if rowHeight < 1 {
			rowHeight = 1
		}

		// Sum pane weights within this row.
		totalPaneWeight := 0
		for _, p := range row.Panes {
			totalPaneWeight += p.Weight
		}
		if totalPaneWeight == 0 {
			totalPaneWeight = 1
		}

		renderedPanes := make([]string, 0, len(row.Panes))
		for _, pane := range row.Panes {
			paneWidth := int(float64(termW) * float64(pane.Weight) / float64(totalPaneWeight))
			if paneWidth < 1 {
				paneWidth = 1
			}

			// lipgloss Height() pads the string BEFORE the border is applied,
			// so Height(h) produces an outer box of h+2 rows (border top+bottom).
			// We want the outer box to be exactly rowHeight rows, so we pass
			// rowHeight-2 to Height().
			innerWidth := paneWidth - 2
			if innerWidth < 1 {
				innerWidth = 1
			}

			innerHeight := rowHeight - 2
			if innerHeight < 1 {
				innerHeight = 1
			}
			contentHeight := innerHeight
			if contentHeight < 1 {
				contentHeight = 1
			}

			dims[pane.Provider] = PaneDims{
				ContentHeight: contentHeight,
				Width:         paneWidth,
			}

			content := views[pane.Provider]
			title := titles[pane.Provider]

			borderColor := lipgloss.Color("240")
			if pane.Provider == activeProvider {
				borderColor = lipgloss.Color(activeBorderColor)
			}

			border := lipgloss.RoundedBorder()
			if title != "" {
				// Inject title into top border: ─ title ─
				t := " " + title + " "
				if len(t) < paneWidth-2 {
					fill := paneWidth - 2 - len(t)
					left := fill / 2
					right := fill - left
					border.Top = strings.Repeat("─", left) + t + strings.Repeat("─", right)
				}
			}

			style := lipgloss.NewStyle().
				Width(innerWidth).
				Height(innerHeight).
				Border(border).
				BorderForeground(borderColor).
				Padding(0, 1)

			styled := style.Render(content)

			renderedPanes = append(renderedPanes, styled)
		}

		renderedRows = append(renderedRows,
			lipgloss.JoinHorizontal(lipgloss.Top, renderedPanes...))
	}

	return lipgloss.JoinVertical(lipgloss.Left, renderedRows...), dims
}
