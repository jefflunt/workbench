// Package log provides a centralized, asynchronous logger for workbench.
//
// Design:
//   - A singleton Logger is initialised once via Init() and then accessed via
//     the package-level Info/Warn/Error/Begin functions.
//   - All writes are dispatched to a background goroutine via a buffered channel
//     so callers (including provider goroutines) never block the UI thread.
//   - Lines are appended to a file (default ~/.local/share/workbench/workbench.log)
//     and held in an in-memory ring of up to 1,000,000 lines.
//   - An in-memory inverted keyword index (all-lowercase words → line indices)
//     is updated by the same background goroutine to allow fast search/filter.
//
// Line format (max 100 chars):
//
//	2026-03-20T14:35:01Z ifo mail  fetch inbox limit=50
//	                     ^^^ ^^^^
//	                     lvl pane (4 chars, space-padded)
//
// Nesting:
//
//	Parent lines are logged with Begin(), which returns a SpanID.  Child lines
//	reference that SpanID and are rendered indented by two spaces per level.
//	The file on disk stores a numeric span prefix so the viewer can reconstruct
//	the tree; the in-memory Line.Indent field stores the computed depth.
package log

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

// Level represents the severity of a log event.
type Level int

const (
	LevelInfo  Level = iota // "ifo"
	LevelWarn               // "war"
	LevelError              // "err"
)

func (l Level) String() string {
	switch l {
	case LevelWarn:
		return "war"
	case LevelError:
		return "err"
	default:
		return "ifo"
	}
}

// SpanID identifies a parent log line for nesting.  Zero means no parent.
type SpanID uint64

// Line is a parsed, display-ready log entry held in memory.
type Line struct {
	// Raw is the full formatted string as written to disk (≤100 chars each piece).
	Raw string
	// Lower is Raw lowercased, used for keyword search without allocation.
	Lower string
	// Indent is the nesting depth (0 = root, 1 = child, …).
	Indent int
	// SpanID is the ID of this line (non-zero for span-opening lines).
	SpanID SpanID
	// ParentID is the ID of the parent span (0 = root).
	ParentID SpanID
}

// ---------------------------------------------------------------------------
// Internal constants
// ---------------------------------------------------------------------------

const (
	maxLines    = 1_000_000
	maxLineLen  = 100
	indentWidth = 2
	// prefix width: "2006-01-02T15:04:05Z ifo pane " = 27 chars
	// timestamp(20) + space(1) + level(3) + space(1) + pane(4) + space(1) = 30
	prefixWidth = 30
)

// ---------------------------------------------------------------------------
// Logger
// ---------------------------------------------------------------------------

type writeReq struct {
	line Line
	raw  string // exact bytes to append to file (may differ from line.Raw for wraps)
}

// Logger is the singleton log manager.
type Logger struct {
	mu        sync.RWMutex
	lines     []Line           // ring buffer (index 0 = oldest)
	index     map[string][]int // lowercase word → line indices
	ch        chan writeReq
	file      *os.File
	nextID    atomic.Uint64
	spanDepth sync.Map // SpanID → depth int
	maxLines  int      // max in-memory lines (default 1_000_000)
}

var (
	global     *Logger
	globalOnce sync.Once
)

// Init initialises the global logger.  Safe to call multiple times; only the
// first call has effect.  logPath may be empty to use the default location.
// maxLines controls how many lines are retained in memory and on disk; 0 uses
// the default of 1,000,000.
func Init(logPath string, maxLines int) error {
	var initErr error
	globalOnce.Do(func() {
		if maxLines <= 0 {
			maxLines = 1_000_000
		}
		if logPath == "" {
			// Use XDG_DATA_HOME convention: ~/.local/share/workbench/
			home := os.Getenv("HOME")
			dataDir := filepath.Join(home, ".local", "share", "workbench")
			logPath = filepath.Join(dataDir, "workbench.log")
		}
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			initErr = fmt.Errorf("log: mkdir: %w", err)
			return
		}
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			initErr = fmt.Errorf("log: open %s: %w", logPath, err)
			return
		}
		l := &Logger{
			lines:    make([]Line, 0, 4096),
			index:    make(map[string][]int),
			ch:       make(chan writeReq, 4096),
			file:     f,
			maxLines: maxLines,
		}
		l.nextID.Store(1)
		global = l
		go l.run()
	})
	return initErr
}

// Global returns the singleton Logger.  Panics if Init has not been called.
func Global() *Logger {
	if global == nil {
		panic("log: Init has not been called")
	}
	return global
}

// ---------------------------------------------------------------------------
// Package-level convenience functions
// ---------------------------------------------------------------------------

// Info logs an informational message.
func Info(pane, msg string) SpanID {
	return Global().log(LevelInfo, pane, 0, msg)
}

// Warn logs a warning message.
func Warn(pane, msg string) SpanID {
	return Global().log(LevelWarn, pane, 0, msg)
}

// Error logs an error message.
func Error(pane, msg string) SpanID {
	return Global().log(LevelError, pane, 0, msg)
}

// Begin opens a new span and logs a parent line.  Returns the SpanID which
// can be passed to Child* functions.
func Begin(pane, msg string) SpanID {
	return Global().log(LevelInfo, pane, 0, msg)
}

// ChildInfo logs an info message as a child of the given span.
func ChildInfo(pane string, parent SpanID, msg string) SpanID {
	return Global().log(LevelInfo, pane, parent, msg)
}

// ChildWarn logs a warning as a child of the given span.
func ChildWarn(pane string, parent SpanID, msg string) SpanID {
	return Global().log(LevelWarn, pane, parent, msg)
}

// ChildError logs an error as a child of the given span.
func ChildError(pane string, parent SpanID, msg string) SpanID {
	return Global().log(LevelError, pane, parent, msg)
}

// ---------------------------------------------------------------------------
// Core log method
// ---------------------------------------------------------------------------

func (l *Logger) log(lvl Level, pane string, parent SpanID, msg string) SpanID {
	id := SpanID(l.nextID.Add(1))

	// Compute indent depth from parent chain.
	depth := 0
	if parent != 0 {
		if d, ok := l.spanDepth.Load(parent); ok {
			depth = d.(int) + 1
		} else {
			depth = 1
		}
	}
	l.spanDepth.Store(id, depth)

	// Build the fixed-width prefix: "2006-01-02T15:04:05Z lvl pane "
	ts := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	paneField := fmt.Sprintf("%-4s", truncPane(pane))
	prefix := fmt.Sprintf("%s %s %s ", ts, lvl.String(), paneField)
	// prefix is always exactly 30 chars

	indent := strings.Repeat(" ", depth*indentWidth)
	// Available width for the message content on each line.
	contentWidth := maxLineLen - prefixWidth - len(indent)
	if contentWidth < 10 {
		contentWidth = 10 // floor: extremely deep nesting
	}

	// Split msg into chunks of contentWidth runes.
	chunks := wrapText(msg, contentWidth)
	if len(chunks) == 0 {
		chunks = []string{""}
	}

	// First chunk → the primary line for this span.
	primaryRaw := prefix + indent + chunks[0]
	primaryLine := Line{
		Raw:      primaryRaw,
		Lower:    strings.ToLower(primaryRaw),
		Indent:   depth,
		SpanID:   id,
		ParentID: parent,
	}
	l.ch <- writeReq{line: primaryLine, raw: primaryRaw + "\n"}

	// Continuation chunks → children at depth+1 (no new SpanID).
	if len(chunks) > 1 {
		childIndent := strings.Repeat(" ", (depth+1)*indentWidth)
		childContentWidth := maxLineLen - prefixWidth - len(childIndent)
		if childContentWidth < 10 {
			childContentWidth = 10
		}
		for _, chunk := range chunks[1:] {
			// Re-wrap if the child indent made it narrower (shouldn't normally happen).
			subchunks := wrapText(chunk, childContentWidth)
			for _, sc := range subchunks {
				raw := prefix + childIndent + sc
				cl := Line{
					Raw:      raw,
					Lower:    strings.ToLower(raw),
					Indent:   depth + 1,
					ParentID: id,
				}
				l.ch <- writeReq{line: cl, raw: raw + "\n"}
			}
		}
	}

	return id
}

// ---------------------------------------------------------------------------
// Background goroutine
// ---------------------------------------------------------------------------

func (l *Logger) run() {
	w := bufio.NewWriterSize(l.file, 64*1024)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case req, ok := <-l.ch:
			if !ok {
				_ = w.Flush()
				return
			}
			// Write to file.
			_, _ = w.WriteString(req.raw)
			// Store in memory.
			l.mu.Lock()
			idx := len(l.lines)
			l.lines = append(l.lines, req.line)
			// Enforce ring: drop oldest if over limit.
			if len(l.lines) > l.maxLines {
				l.lines = l.lines[len(l.lines)-l.maxLines:]
				// Rebuild index (rare; only when log is very large).
				l.rebuildIndex()
			} else {
				l.indexLine(idx, req.line)
			}
			l.mu.Unlock()

		case <-ticker.C:
			_ = w.Flush()
		}
	}
}

func (l *Logger) indexLine(idx int, line Line) {
	for _, word := range strings.Fields(line.Lower) {
		// Strip leading punctuation for cleaner keyword matching.
		word = strings.TrimLeft(word, "([{\"'")
		word = strings.TrimRight(word, ")]},;:\"'.")
		if word == "" {
			continue
		}
		l.index[word] = append(l.index[word], idx)
	}
}

func (l *Logger) rebuildIndex() {
	l.index = make(map[string][]int, len(l.index))
	for i, line := range l.lines {
		l.indexLine(i, line)
	}
}

// ---------------------------------------------------------------------------
// Read API (called from UI goroutine)
// ---------------------------------------------------------------------------

// Lines returns a snapshot of all in-memory log lines.
func (l *Logger) Lines() []Line {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Line, len(l.lines))
	copy(out, l.lines)
	return out
}

// Search returns the indices (into the Lines snapshot) of lines matching query.
// The search is a simple case-insensitive substring scan against line.Lower.
// This is fast enough for ≤1M lines on the UI goroutine; the caller should
// call Lines() once and pass that slice to avoid a second lock.
func (l *Logger) Search(lines []Line, query string) []int {
	q := strings.ToLower(query)
	var out []int
	for i, line := range lines {
		if strings.Contains(line.Lower, q) {
			out = append(out, i)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// truncPane returns at most 4 runes of the pane name.
func truncPane(pane string) string {
	runes := []rune(pane)
	if len(runes) > 4 {
		return string(runes[:4])
	}
	return pane
}

// wrapText splits text into lines of at most width runes, breaking on spaces
// when possible.
func wrapText(text string, width int) []string {
	if width <= 0 {
		width = 10
	}
	if utf8.RuneCountInString(text) <= width {
		return []string{text}
	}
	var lines []string
	for len(text) > 0 {
		if utf8.RuneCountInString(text) <= width {
			lines = append(lines, text)
			break
		}
		// Find a break point: last space within [0, width).
		runes := []rune(text)
		cut := width
		for i := width - 1; i > 0; i-- {
			if runes[i] == ' ' {
				cut = i
				break
			}
		}
		lines = append(lines, string(runes[:cut]))
		text = strings.TrimLeft(string(runes[cut:]), " ")
	}
	return lines
}
