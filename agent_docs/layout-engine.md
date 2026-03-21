# Layout Engine

## Overview

`internal/layout/layout.go` converts weight ratios from config into integer terminal cell dimensions and assembles the final view string using lipgloss.

**Exported API:**

```go
func Render(
    rows              []RowConfig,
    views             map[string]string,  // providerName → pre-rendered content string
    activeProvider    string,
    activeBorderColor string,
    termW, termH      int,
    reservedRows      int,               // rows taken by footer (0 or 1)
) (string, map[string]PaneDims)
```

Returns the assembled terminal view and a `map[providerName]PaneDims` with computed dimensions.

```go
type PaneDims struct {
    ContentHeight int  // rows available for content inside the border (header already subtracted)
    Width         int  // total pane width in columns
}
```

---

## Weight Math

```
paneH     = termH - reservedRows
rowHeight = floor(paneH * row.Weight / totalRowWeight)    minimum 1
paneWidth = floor(termW * pane.Weight / totalPaneWeight)  minimum 1
```

Both use integer truncation. With two equal-weight rows on an odd `paneH`, both rows get `floor(paneH/2)` — the total is `paneH - 1`, leaving one unrendered row. This is acceptable rounding behaviour; fixing it would require distributing remainder rows to specific panes.

---

## lipgloss Height — Critical Quirk

**In lipgloss v1.1.0, `Height(h)` sets the content height *before* the border is applied.**

The rendering pipeline inside `style.Render()` is:
1. Apply padding to content string
2. `alignTextVertical(str, pos, height)` — pads to `height` lines
3. Apply horizontal alignment / width padding
4. `applyBorder(str)` — adds border top + bottom rows

This means `Height(h) + Border(RoundedBorder())` produces an **outer** box of `h + 2` rows.

To get an outer box of exactly `rowHeight` rows:

```go
innerHeight = rowHeight - 2          // what to pass to Height()
// outer height = innerHeight + 2 = rowHeight ✓
```

The content string passed to `Render()` must be ≤ `innerHeight` lines, otherwise it overflows and the outer box grows beyond `rowHeight`, breaking the layout.

---

## Content Height Derivation

```
outerHeight  = rowHeight          (allocated terminal rows)
innerHeight  = rowHeight - 2      (passed to Height(); border adds 2)
contentRows  = innerHeight - 1    (renderPane writes a 1-line header first)
```

`contentRows` is what gets stored in `PaneDims.ContentHeight` and passed to `renderPane` as `contentHeight`.

Both `innerHeight` and `contentHeight` are clamped to ≥ 1.

---

## Scroll Indicator Budget

`renderPane` conditionally writes a scroll indicator line when `len(items) > itemRows`. This line must come **out of** the item budget — not in addition to it — otherwise the content string has one more line than `innerHeight` and the box overflows.

The fix (in `renderPane`, not in `layout.go`):

```go
showIndicator := len(items) > itemRows
if showIndicator && itemRows > 1 {
    itemRows--   // reserve one row for the indicator
}
// ... render items ...
if showIndicator {
    sb.WriteString(indicatorLine)
}
```

---

## Assembly

```
for each row:
    for each pane:
        build styled box: lipgloss.NewStyle().
            Width(paneWidth).
            Height(innerHeight).        ← NOT rowHeight
            Border(RoundedBorder()).
            BorderForeground(color).
            Padding(0, 1).             ← 1 col left+right, no vertical padding
            Render(content)
    JoinHorizontal(Top, panes...)
JoinVertical(Left, rows...)
```

Active pane border: `lipgloss.Color(activeBorderColor)` (from config, default `"212"`).  
Inactive pane border: `lipgloss.Color("240")` — hard-coded.

---

## The Two-Pass Render

`View()` calls `layout.Render` twice per frame:

| Pass | `views` arg | Purpose |
|------|------------|---------|
| 1 | `map[string]string{}` (empty) | Get `PaneDims` for each pane; rendered string discarded |
| 2 | Real content strings from `renderPane` | Produce the final view string |

Both passes use the same `reservedRows` value (computed once before both calls).

**Why not compute dims without rendering?** The layout engine currently always calls `lipgloss.Render` internally. A future optimisation could split the dims calculation into a separate function that returns only the math without any lipgloss calls.

---

## `PaneView` — Unused Type

`layout.PaneView{ Provider string; Content string }` is exported but never used anywhere. It is a leftover from an earlier design iteration and can be safely deleted.
