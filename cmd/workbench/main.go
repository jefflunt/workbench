// Package main is the entry point for the workbench TUI dashboard.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/jluntpcty/workbench/internal/cache"
	"github.com/jluntpcty/workbench/internal/layout"
	wblog "github.com/jluntpcty/workbench/internal/log"
	"github.com/jluntpcty/workbench/internal/media"
	"github.com/jluntpcty/workbench/internal/player"
	"github.com/jluntpcty/workbench/internal/plugin"
)

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// PaneConfig mirrors the TOML [[layout.row.pane]] structure.
type PaneConfig struct {
	Provider string `toml:"provider"`
	Weight   int    `toml:"weight"`
}

// RowConfig mirrors the TOML [[layout.row]] structure.
type RowConfig struct {
	Weight int          `toml:"weight"`
	Panes  []PaneConfig `toml:"pane"`
}

// LayoutConfig mirrors the TOML [layout] section.
type LayoutConfig struct {
	Rows []RowConfig `toml:"row"`
}

// ThemeConfig holds visual styling settings.
type ThemeConfig struct {
	// ActivePaneColor is a terminal color code (ANSI 256 or hex) used for the
	// focused pane's border.  Defaults to "#00008B" (pink/magenta).
	ActivePaneColor string `toml:"active_pane_color"`
}

// LogConfig holds logging settings.
type LogConfig struct {
	// MaxLines is the maximum number of log lines retained in memory and on
	// disk.  Defaults to 1,000,000.
	MaxLines int `toml:"max_lines"`
}

// Config is the root configuration structure.
type Config struct {
	// Plugins holds per-plugin configuration.  Each key is a plugin name that
	// must match the plugin binary's filename (e.g. "github", "jira",
	// "applemail") and the [[layout.row.pane]] provider value.  The value is
	// passed verbatim to the plugin as its FetchRequest.Config map.
	Plugins map[string]map[string]any `toml:"plugins"`
	Theme   ThemeConfig               `toml:"theme"`
	Log     LogConfig                 `toml:"log"`
	Layout  LayoutConfig              `toml:"layout"`
}

// PlaybackState is used to persist playback state to disk.
type PlaybackState struct {
	Queue        []plugin.Item `json:"queue"`
	Index        int           `json:"index"`
	ProviderName string        `json:"provider_name"`
	Timestamp    float64       `json:"timestamp"`
}

// defaultConfig returns a sensible default configuration.
func defaultConfig() Config {
	return Config{
		Plugins: map[string]map[string]any{},
		Theme: ThemeConfig{
			ActivePaneColor: "#00008B",
		},
		Log: LogConfig{
			MaxLines: 1_000_000,
		},
		Layout: LayoutConfig{
			Rows: []RowConfig{
				{
					Weight: 1,
					Panes: []PaneConfig{
						{Provider: "applemail", Weight: 2},
						{Provider: "github", Weight: 3},
					},
				},
				{
					Weight: 1,
					Panes: []PaneConfig{
						{Provider: "jira", Weight: 1},
					},
				},
			},
		},
	}
}

// loadConfig reads the config file from the XDG config directory, falling
// back gracefully to defaults when the file is absent.
func loadConfig() (Config, error) {
	cfg := defaultConfig()

	cfgDir := os.Getenv("XDG_CONFIG_HOME")
	if cfgDir == "" {
		cfgDir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	path := filepath.Join(cfgDir, "workbench", "config.toml")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("config: read %s: %w", path, err)
	}

	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("config: parse %s: %w", path, err)
	}
	// Ensure theme defaults are applied when the config omits them.
	if cfg.Theme.ActivePaneColor == "" {
		cfg.Theme.ActivePaneColor = "#00008B"
	}
	return cfg, nil
}

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

// fetchResultMsg carries the result of a single provider's Fetch call.
type fetchResultMsg struct {
	providerName string
	items        []plugin.Item
	err          error
}

// playbackStartedMsg is sent when a new music player process has started.
type playbackStartedMsg struct {
	cmd          *exec.Cmd
	player       player.Player
	queue        []plugin.Item
	providerName string
}

// playbackFinishedMsg is sent when a music player process exits.
type playbackFinishedMsg struct{ cmd *exec.Cmd }

// expandResultMsg is sent when a plugin expands an item.
type expandResultMsg struct {
	providerName string
	items        []plugin.Item
	err          error
}

// playerTrackChangedMsg is sent when the player playlist index changes.
type playerTrackChangedMsg struct {
	index int
}

// loginDoneMsg is sent after a sub-shell login command exits.
type loginDoneMsg struct{ err error }

// logTickMsg triggers a log pane refresh.
type logTickMsg struct{}

// ---------------------------------------------------------------------------
// Pane state
// ---------------------------------------------------------------------------

type paneState struct {
	providerName string
	items        []plugin.Item
	err          error
	loading      bool
	stale        bool // true when showing cached data while a fetch is in flight
	cursor       int
	spinner      spinner.Model

	// Per-pane search/filter state (mirrors the log overlay search model).
	searchMode    string          // "", "pending", "s", "f", "F"
	searchQuery   string          // committed query
	searchMatches []int           // item indices matching searchQuery
	searchCursor  int             // index into searchMatches for n/N navigation
	searchInput   textinput.Model // text input for typing the query
}

func newPaneState(name string) paneState {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	ti := textinput.New()
	ti.CharLimit = 100
	return paneState{
		providerName: name,
		loading:      true,
		spinner:      s,
		searchInput:  ti,
	}
}

// ---------------------------------------------------------------------------
// UI mode
// ---------------------------------------------------------------------------

type uiMode int

const (
	modeNormal uiMode = iota
	modeHelp
	modeLogin
	modeLog
	modeNowPlaying
)

// ---------------------------------------------------------------------------
// Styles
// ---------------------------------------------------------------------------

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#00008B"))

	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("57"))

	highlightedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("196"))

	normalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	matchStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("226")).
			Background(lipgloss.Color("58"))

	metaStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243"))

	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("110"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	overlayBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#00008B")).
				Padding(1, 3)

	overlayTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#00008B"))

	overlayKeyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("229"))

	overlayDescStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("252"))

	searchBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00008B")).
			Bold(true)

	searchNoMatchStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("196"))
)

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

type model struct {
	cfg       Config
	providers map[string]plugin.Provider
	panes     []paneState    // ordered list matching layout row/pane order
	paneIndex map[string]int // providerName → index in panes slice
	active    int            // index of the focused pane
	termW     int
	termH     int

	// UI mode
	mode     uiMode
	prevMode uiMode // mode to restore when closing the help overlay

	// Login state
	loginErr error // last error from a login sub-shell, if any

	// Log overlay state
	logLines       []wblog.Line // snapshot refreshed on each logTickMsg
	logScroll      int          // index of the first visible line (from top)
	logAutoScroll  bool         // true = stick to bottom (disabled during search)
	logSearchMode  string       // "", "s" (search), "f" (filter), "F" (dim filter)
	logInput       textinput.Model
	logQuery       string // committed search/filter query
	logMatches     []int  // line indices matching logQuery (search mode)
	logMatchCursor int    // index into logMatches for n/N navigation

	// Playback state
	activePlayer       player.Player
	activeCmd          *exec.Cmd
	nowPlayingQueue    []plugin.Item
	nowPlayingIndex    int
	queueCursor        int
	queueScroll        int
	nowPlayingActive   bool
	nowPlayingProvider string
	savedPlayback      *PlaybackState

	searchHistory []string
	searchHistIdx int
}

// allProviderNames returns the ordered unique list of provider names from the
// layout configuration.
func allProviderNames(cfg Config) []string {
	seen := map[string]bool{}
	var names []string
	for _, row := range cfg.Layout.Rows {
		for _, pane := range row.Panes {
			if !seen[pane.Provider] {
				seen[pane.Provider] = true
				names = append(names, pane.Provider)
			}
		}
	}
	return names
}

func initialModel(cfg Config, providers map[string]plugin.Provider) model {
	names := allProviderNames(cfg)
	panes := make([]paneState, len(names))
	paneIndex := make(map[string]int, len(names))
	for i, name := range names {
		ps := newPaneState(name)
		// Pre-populate from cache so data is visible immediately.
		if cached, err := cache.Load(name); err == nil && len(cached) > 0 {
			ps.items = cached
			ps.loading = false
			ps.stale = true
		}
		panes[i] = ps
		paneIndex[name] = i
	}

	logInput := textinput.New()
	logInput.CharLimit = 100

	saved, _ := loadPlayback()

	return model{
		cfg:           cfg,
		providers:     providers,
		panes:         panes,
		paneIndex:     paneIndex,
		termW:         80,
		termH:         24,
		logInput:      logInput,
		logAutoScroll: true,
		savedPlayback: saved,
		searchHistory: loadSearchHistory(),
	}
}

// ---------------------------------------------------------------------------
// Init / Update / View
// ---------------------------------------------------------------------------

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{}
	for _, p := range m.panes {
		// Always tick spinners; stale panes show cached content but are
		// still fetching in the background.
		cmds = append(cmds, p.spinner.Tick)
	}
	cmds = append(cmds, fetchAll(m.providers))
	cmds = append(cmds, logTick())
	return tea.Batch(cmds...)
}

// logTick schedules a log pane refresh after a short delay.
func logTick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
		return logTickMsg{}
	})
}

// fetchAll kicks off concurrent Fetch calls for all providers.
func fetchAll(providers map[string]plugin.Provider) tea.Cmd {
	return func() tea.Msg {
		span := wblog.Begin("main", fmt.Sprintf("refresh all providers count=%d", len(providers)))
		var wg sync.WaitGroup
		results := make(chan fetchResultMsg, len(providers))

		for _, p := range providers {
			wg.Add(1)
			go func(p plugin.Provider) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				wblog.ChildInfo("main", span, fmt.Sprintf("fetch start provider=%s", p.Name()))
				items, err := p.Fetch(ctx, "")
				if err != nil {
					wblog.ChildError("main", span, fmt.Sprintf("fetch error provider=%s err=%v", p.Name(), err))
				} else {
					wblog.ChildInfo("main", span, fmt.Sprintf("fetch done provider=%s items=%d", p.Name(), len(items)))
				}
				results <- fetchResultMsg{
					providerName: p.Name(),
					items:        items,
					err:          err,
				}
			}(p)
		}

		go func() {
			wg.Wait()
			close(results)
		}()

		var msgs []fetchResultMsg
		for r := range results {
			msgs = append(msgs, r)
		}
		wblog.ChildInfo("main", span, "refresh complete")
		return msgs
	}
}

// fetchOne kicks off a Fetch call for a single provider, optionally with a
// query. Returns a slice of length 1 containing the result.
func fetchOne(p plugin.Provider, query string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		wblog.Info("main", fmt.Sprintf("fetch start provider=%s query=%q", p.Name(), query))
		items, err := p.Fetch(ctx, query)
		if err != nil {
			wblog.Error("main", fmt.Sprintf("fetch error provider=%s err=%v", p.Name(), err))
		} else {
			wblog.Info("main", fmt.Sprintf("fetch done provider=%s items=%d", p.Name(), len(items)))
		}
		return []fetchResultMsg{{
			providerName: p.Name(),
			items:        items,
			err:          err,
		}}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termW = msg.Width
		m.termH = msg.Height

	case tea.KeyMsg:
		// Global keys
		if msg.String() == "ctrl+c" {
			if m.activePlayer != nil {
				ts, _ := m.activePlayer.CurrentTimestamp()
				_ = savePlayback(PlaybackState{
					Queue:        m.nowPlayingQueue,
					Index:        m.nowPlayingIndex,
					ProviderName: m.nowPlayingProvider,
					Timestamp:    ts,
				})
				_ = m.activeCmd.Process.Kill()
			}
			return m, tea.Quit
		}

		// '?' is global — opens help from any mode and returns to the previous mode.
		if msg.String() == "?" && m.mode != modeHelp {
			m.prevMode = m.mode
			m.mode = modeHelp
			return m, nil
		}

		switch m.mode {
		case modeHelp:
			// Any key closes the help overlay and restores the previous mode.
			m.mode = m.prevMode
			return m, nil

		case modeLogin:
			switch msg.String() {
			case "esc":
				m.mode = modeNormal
				m.loginErr = nil
				return m, nil
			case "g":
				m.mode = modeNormal
				m.loginErr = nil
				return m, runLoginCmd("gh", "auth", "login")
			case "a":
				m.mode = modeNormal
				m.loginErr = nil
				return m, runLoginCmd("acli", "jira", "auth", "login")
			default:
				// Any other key cancels.
				m.mode = modeNormal
				m.loginErr = nil
				return m, nil
			}

		case modeLog:
			// If we're in a sub-mode (search/filter input), route to it first.
			if m.logSearchMode != "" && m.logInput.Focused() {
				switch msg.String() {
				case "esc":
					m.logSearchMode = ""
					m.logQuery = ""
					m.logMatches = nil
					m.logMatchCursor = 0
					m.logAutoScroll = true
					m.logInput.SetValue("")
					m.logInput.Blur()
					return m, nil
				case "enter":
					m.logQuery = strings.TrimSpace(m.logInput.Value())
					m.logInput.Blur()
					if m.logSearchMode == "s" || m.logSearchMode == "f" || m.logSearchMode == "F" {
						m.logMatches = wblog.Global().Search(m.logLines, m.logQuery)
						m.logMatchCursor = len(m.logMatches) - 1 // start at most recent
						if m.logSearchMode == "s" && len(m.logMatches) > 0 {
							// Jump to first match (most recent).
							m.logScroll = m.logMatches[m.logMatchCursor]
							m.logAutoScroll = false
						}
					}
					return m, nil
				default:
					var cmd tea.Cmd
					m.logInput, cmd = m.logInput.Update(msg)
					return m, cmd
				}
			}
			// Log pane navigation.
			switch msg.String() {
			case "ctrl+l", "q", "esc":
				m.mode = modeNormal
				m.logSearchMode = ""
				m.logQuery = ""
				m.logMatches = nil
				m.logMatchCursor = 0
				m.logAutoScroll = true
				m.logInput.SetValue("")
				m.logInput.Blur()
			case "/":
				// Wait for next char: 's', 'f', or 'F'.
				m.logSearchMode = "pending"
				m.logInput.SetValue("")
				return m, nil
			case "s":
				if m.logSearchMode == "pending" {
					m.logSearchMode = "s"
					m.logQuery = ""
					m.logMatches = nil
					m.logInput.SetValue("")
					m.logInput.Placeholder = "reverse search…"
					m.logAutoScroll = false
					return m, m.logInput.Focus()
				}
			case "f":
				if m.logSearchMode == "pending" {
					m.logSearchMode = "f"
					m.logQuery = ""
					m.logMatches = nil
					m.logInput.SetValue("")
					m.logInput.Placeholder = "filter (hide non-matching)…"
					m.logAutoScroll = false
					return m, m.logInput.Focus()
				}
			case "F":
				if m.logSearchMode == "pending" {
					m.logSearchMode = "F"
					m.logQuery = ""
					m.logMatches = nil
					m.logInput.SetValue("")
					m.logInput.Placeholder = "filter (dim non-matching)…"
					m.logAutoScroll = false
					return m, m.logInput.Focus()
				}
			case "n":
				if m.logSearchMode == "s" && len(m.logMatches) > 0 {
					m.logMatchCursor = (m.logMatchCursor + 1) % len(m.logMatches)
					m.logScroll = m.logMatches[m.logMatchCursor]
				}
			case "N":
				if m.logSearchMode == "s" && len(m.logMatches) > 0 {
					m.logMatchCursor = (m.logMatchCursor - 1 + len(m.logMatches)) % len(m.logMatches)
					m.logScroll = m.logMatches[m.logMatchCursor]
				}
			case "w", "e", "i":
				if m.logSearchMode != "pending" {
					quick := map[string]string{"w": "war", "e": "err", "i": "ifo"}
					q := quick[msg.String()]
					// Toggle off if already filtering by this exact query.
					if m.logSearchMode == "f" && m.logQuery == q {
						m.logSearchMode = ""
						m.logQuery = ""
						m.logMatches = nil
						m.logMatchCursor = 0
						m.logAutoScroll = true
						m.logScroll = max(0, len(m.logLines)-m.logVisibleHeight())
					} else {
						m.logSearchMode = "f"
						m.logQuery = q
						m.logMatches = wblog.Global().Search(m.logLines, q)
						m.logMatchCursor = 0
						m.logAutoScroll = false
					}
				}
			case "j", "down":
				visHeight := m.logVisibleHeight()
				if m.logScroll < len(m.logLines)-visHeight {
					m.logScroll++
				}
				m.logAutoScroll = false
			case "k", "up":
				if m.logScroll > 0 {
					m.logScroll--
				}
				m.logAutoScroll = false
			case "G":
				m.logAutoScroll = true
				m.logScroll = max(0, len(m.logLines)-m.logVisibleHeight())
			case "g":
				m.logScroll = 0
				m.logAutoScroll = false
			}
			return m, nil

		case modeNowPlaying:
			switch msg.String() {
			case "esc":
				m.mode = modeNormal
			case "j", "down":
				if m.queueCursor < len(m.nowPlayingQueue)-1 {
					m.queueCursor++
				}
				// Keep cursor in view.
				innerH := (m.termH * 60 / 100) - 4
				if innerH < 1 {
					innerH = 1
				}
				if m.queueCursor >= m.queueScroll+innerH {
					m.queueScroll = m.queueCursor - innerH + 1
				}
			case "k", "up":
				if m.queueCursor > 0 {
					m.queueCursor--
				}
				// Keep cursor in view.
				if m.queueCursor < m.queueScroll {
					m.queueScroll = m.queueCursor
				}
			case " ":
				if m.activePlayer != nil {
					_ = m.activePlayer.Pause()
				}
			case "h":
				if m.activePlayer != nil {
					_ = m.activePlayer.Prev()
					m.updateNowPlayingIndex(-1)
					m.queueCursor = m.nowPlayingIndex
				}
			case "l":
				if m.activePlayer != nil {
					_ = m.activePlayer.Next()
					m.updateNowPlayingIndex(1)
					m.queueCursor = m.nowPlayingIndex
				}
			case "enter":
				if m.activePlayer != nil {
					_ = m.activePlayer.PlayIndex(m.queueCursor + 1)
					m.nowPlayingIndex = m.queueCursor
				}
			}
			return m, nil

		case modeNormal:
			p := &m.panes[m.active]

			// If the active pane's search input is focused, route keys to it.
			if p.searchInput.Focused() {
				switch msg.String() {
				case "up":
					if len(m.searchHistory) > 0 {
						if m.searchHistIdx > 0 {
							m.searchHistIdx--
						}
						p.searchInput.SetValue(m.searchHistory[m.searchHistIdx])
						p.searchInput.SetCursor(len(m.searchHistory[m.searchHistIdx]))
					}
				case "down":
					if len(m.searchHistory) > 0 {
						if m.searchHistIdx < len(m.searchHistory)-1 {
							m.searchHistIdx++
							p.searchInput.SetValue(m.searchHistory[m.searchHistIdx])
							p.searchInput.SetCursor(len(m.searchHistory[m.searchHistIdx]))
						} else if m.searchHistIdx == len(m.searchHistory)-1 {
							m.searchHistIdx++
							p.searchInput.SetValue("")
						}
					}
				case "esc":
					p.searchMode = ""
					p.searchQuery = ""
					p.searchMatches = nil
					p.searchCursor = 0
					p.searchInput.SetValue("")
					p.searchInput.Blur()
				case "enter":
					p.searchQuery = strings.TrimSpace(p.searchInput.Value())
					p.searchInput.Blur()
					if p.searchMode == "s" || p.searchMode == "f" || p.searchMode == "F" {
						p.searchMatches = paneMatches(p.items, p.searchQuery)
						p.searchCursor = 0
						if p.searchMode == "s" && len(p.searchMatches) > 0 {
							p.cursor = p.searchMatches[0]
						}
						// If in /s search mode, also trigger a fresh fetch with the
						// query to support live results (e.g. from Plex or YTM).
						addSearchHistory(&m.searchHistory, p.searchQuery)
						if p.searchMode == "s" {
							p.stale = true
							prov := m.providers[p.providerName]
							return m, tea.Batch(p.spinner.Tick, fetchOne(prov, p.searchQuery))
						}
					}
				default:
					var cmd tea.Cmd
					p.searchInput, cmd = p.searchInput.Update(msg)
					return m, cmd
				}
				return m, nil
			}

			// Pending mode: waiting for s/f/F after '/'.
			if p.searchMode == "pending" {
				switch msg.String() {
				case "a":
					items := m.visibleItems(m.active)
					if p.cursor >= 0 && p.cursor < len(items) {
						item := items[p.cursor]
						wblog.Info("main", fmt.Sprintf("attempting to add to queue: %s, active: %v", item.Title, m.nowPlayingActive))
						if m.nowPlayingActive {
							m.nowPlayingQueue = append(m.nowPlayingQueue, item)
							wblog.Info("main", "added to queue: "+item.Title)
						} else {
							// Optionally allow adding to queue even if not playing?
							// For now, let's just make it work if active is false but queue exists.
							m.nowPlayingQueue = append(m.nowPlayingQueue, item)
							m.nowPlayingActive = true
							m.nowPlayingProvider = p.providerName
							wblog.Info("main", "added to queue (was inactive): "+item.Title)
						}
					}

				case "s":

					p.searchMode = "s"
					p.searchQuery = ""
					p.searchMatches = nil
					p.searchInput.SetValue("")
					p.searchInput.Placeholder = "reverse search…"
					m.searchHistIdx = len(m.searchHistory)
					return m, p.searchInput.Focus()
				case "f":
					p.searchMode = "f"
					p.searchQuery = ""
					p.searchMatches = nil
					p.searchInput.SetValue("")
					p.searchInput.Placeholder = "filter (hide non-matching)…"
					m.searchHistIdx = len(m.searchHistory)
					return m, p.searchInput.Focus()
				case "F":
					p.searchMode = "F"
					p.searchQuery = ""
					p.searchMatches = nil
					p.searchInput.SetValue("")
					p.searchInput.Placeholder = "filter (dim non-matching)…"
					m.searchHistIdx = len(m.searchHistory)
					return m, p.searchInput.Focus()
				default:
					// Any other key cancels pending.
					p.searchMode = ""
					// Fall through to normal key handling below.
				}
			}

			switch msg.String() {
			case "q", "ctrl+c":
				if m.activePlayer != nil {
					ts, _ := m.activePlayer.CurrentTimestamp()
					_ = savePlayback(PlaybackState{
						Queue:        m.nowPlayingQueue,
						Index:        m.nowPlayingIndex,
						ProviderName: m.nowPlayingProvider,
						Timestamp:    ts,
					})
					_ = m.activeCmd.Process.Kill()
				}
				return m, tea.Quit

			case "/":
				p.searchMode = "pending"
				p.searchInput.SetValue("")
				return m, nil

			case "esc":
				// Clear active pane's search/filter.
				if p.searchMode != "" {
					p.searchMode = ""
					p.searchQuery = ""
					p.searchMatches = nil
					p.searchCursor = 0
					p.searchInput.SetValue("")
					p.searchInput.Blur()
				}

			case "enter":
				// Open the selected item's URL.
				items := m.visibleItems(m.active)
				if p.cursor >= 0 && p.cursor < len(items) {
					item := items[p.cursor]
					if strings.HasPrefix(item.URL, "music://") && m.activePlayer != nil {
						_ = m.activeCmd.Process.Kill()
						m.activePlayer = nil
						m.activeCmd = nil
						m.nowPlayingActive = false
					}
					if cmd := openItem(item, p.providerName); cmd != nil {
						return m, cmd
					}
				}

			case "m":
				if p.searchMode == "s" && len(p.searchMatches) > 0 {
					p.searchCursor = (p.searchCursor + 1) % len(p.searchMatches)
					p.cursor = p.searchMatches[p.searchCursor]
				}

			case "M":
				if p.searchMode == "s" && len(p.searchMatches) > 0 {
					p.searchCursor = (p.searchCursor - 1 + len(p.searchMatches)) % len(p.searchMatches)
					p.cursor = p.searchMatches[p.searchCursor]
				}

			case "ctrl+l":
				m.mode = modeLog
				m.logLines = wblog.Global().Lines()
				m.logAutoScroll = true
				m.logScroll = max(0, len(m.logLines)-m.logVisibleHeight())
				return m, nil

			case "L":
				m.mode = modeLogin
				m.loginErr = nil
				return m, nil

			case "tab":
				m.active = (m.active + 1) % len(m.panes)

			case "shift+tab":
				m.active = (m.active - 1 + len(m.panes)) % len(m.panes)

			case "j", "down":
				m.handleNav(1)

			case "k", "up":
				m.handleNav(-1)

			case "J":
				m.handleNav(10)

			case "K":
				m.handleNav(-10)

			case " ":
				if m.activePlayer != nil {
					_ = m.activePlayer.Pause()
				} else if m.savedPlayback != nil {
					wblog.Info("main", "resuming playback from saved state")
					state := m.savedPlayback
					m.savedPlayback = nil

					// Trigger playback
					if len(state.Queue) > state.Index {
						item := state.Queue[state.Index]
						if cmd := openItem(item, state.ProviderName); cmd != nil {
							return m, cmd
						}
					}
				}

			case "s":
				items := m.visibleItems(m.active)
				if p.cursor >= 0 && p.cursor < len(items) {
					item := items[p.cursor]
					if strings.HasPrefix(item.URL, "music://") {
						item.URL = strings.Replace(item.URL, "music://", "music-shuffle://", 1)
						if m.activePlayer != nil {
							_ = m.activeCmd.Process.Kill()
							m.activePlayer = nil
							m.activeCmd = nil
							m.nowPlayingActive = false
						}
						if cmd := openItem(item, p.providerName); cmd != nil {
							return m, cmd
						}
					}
				}

			case "delete":
				items := m.visibleItems(m.active)
				if p.cursor >= 0 && p.cursor < len(items) {
					item := items[p.cursor]
					prov := m.providers[p.providerName]
					p.stale = true
					return m, tea.Batch(p.spinner.Tick, func() tea.Msg {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						err := prov.Delete(ctx, item)
						wblog.Info("main", fmt.Sprintf("delete result for %s: %v", item.Title, err))
						if err != nil {
							return expandResultMsg{
								providerName: p.providerName,
								err:          err,
							}
						}
						// Refresh pane after delete
						return []fetchResultMsg{{
							providerName: p.providerName,
						}}
					})
				}
			case "x":
				items := m.visibleItems(m.active)
				if p.cursor >= 0 && p.cursor < len(items) {
					item := items[p.cursor]
					prov := m.providers[p.providerName]
					p.stale = true
					return m, tea.Batch(p.spinner.Tick, func() tea.Msg {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						newItems, err := prov.Expand(ctx, item)
						return expandResultMsg{
							providerName: p.providerName,
							items:        newItems,
							err:          err,
						}
					})
				}

			case "h":
				if m.activePlayer != nil {
					_ = m.activePlayer.Prev()
				}

			case "l":
				if m.activePlayer != nil {
					_ = m.activePlayer.Next()
				}

			case "n":
				if p.providerName == "music-streamer" || p.providerName == "plex" || p.providerName == "ytmusic" {
					m.mode = modeNowPlaying
				}

			case "R":
				// Keep existing items visible (stale) while refreshing.
				for i := range m.panes {
					m.panes[i].loading = false
					m.panes[i].stale = true
					m.panes[i].err = nil
				}
				wblog.Info("main", "manual refresh triggered")
				cmds := []tea.Cmd{fetchAll(m.providers)}
				for _, p := range m.panes {
					cmds = append(cmds, p.spinner.Tick)
				}
				return m, tea.Batch(cmds...)
			}
		}

	case loginDoneMsg:
		m.loginErr = msg.err
		return m, nil

	case playbackStartedMsg:
		m.activePlayer = msg.player
		m.activeCmd = msg.cmd
		m.nowPlayingQueue = msg.queue
		m.nowPlayingIndex = 0
		m.nowPlayingActive = true
		m.nowPlayingProvider = msg.providerName
		return m, tea.Batch(waitForPlayback(msg.cmd), watchPlayer(msg.player))

	case playbackFinishedMsg:
		if m.activeCmd == msg.cmd {
			m.activePlayer = nil
			m.activeCmd = nil
			m.nowPlayingQueue = nil
			m.nowPlayingActive = false
			m.nowPlayingProvider = ""
			m.mode = modeNormal
		}
		return m, nil

	case playerTrackChangedMsg:
		m.nowPlayingIndex = msg.index
		return m, watchPlayer(m.activePlayer)

	case expandResultMsg:
		if idx, ok := m.paneIndex[msg.providerName]; ok {
			m.panes[idx].loading = false
			m.panes[idx].stale = false
			m.panes[idx].items = msg.items
			m.panes[idx].err = msg.err
			m.panes[idx].cursor = 0
		}
		return m, nil

	case []fetchResultMsg:
		for _, result := range msg {
			if idx, ok := m.paneIndex[result.providerName]; ok {
				m.panes[idx].loading = false
				m.panes[idx].stale = false
				m.panes[idx].items = result.items
				m.panes[idx].err = result.err
				// Clamp cursor to the new list size
				if m.panes[idx].cursor >= len(m.panes[idx].items) {
					m.panes[idx].cursor = len(m.panes[idx].items) - 1
					if m.panes[idx].cursor < 0 {
						m.panes[idx].cursor = 0
					}
				}
				// Persist fresh data to cache (best-effort; ignore errors).
				if result.err == nil && len(result.items) > 0 {
					_ = cache.Save(result.providerName, result.items)
				}
			}
		}

	case spinner.TickMsg:
		var cmds []tea.Cmd
		for i := range m.panes {
			if m.panes[i].loading || m.panes[i].stale {
				updated, cmd := m.panes[i].spinner.Update(msg)
				m.panes[i].spinner = updated
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)

	case logTickMsg:
		m.logLines = wblog.Global().Lines()
		if m.mode == modeLog && m.logAutoScroll {
			m.logScroll = max(0, len(m.logLines)-m.logVisibleHeight())
		}
		// Also refresh match indices if a query is active.
		if m.logQuery != "" {
			m.logMatches = wblog.Global().Search(m.logLines, m.logQuery)
		}
		return m, logTick()
	}

	return m, nil
}

func (m *model) handleNav(delta int) {
	p := &m.panes[m.active]
	items := m.visibleItems(m.active)
	if delta > 0 {
		p.cursor = min(p.cursor+delta, len(items)-1)
	} else {
		p.cursor = max(p.cursor+delta, 0)
	}
}

func (m *model) updateNowPlayingIndex(delta int) {
	m.nowPlayingIndex += delta
	if m.nowPlayingIndex < 0 {
		m.nowPlayingIndex = 0
	} else if m.nowPlayingIndex >= len(m.nowPlayingQueue) {
		m.nowPlayingIndex = len(m.nowPlayingQueue) - 1
	}
}

// runLoginCmd suspends the TUI, runs the given command interactively in the
// user's terminal, then resumes the TUI and reports any error.
func runLoginCmd(name string, args ...string) tea.Cmd {
	c := exec.Command(name, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return loginDoneMsg{err: err}
	})
}

func savePlayback(state PlaybackState) error {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	path := filepath.Join(dir, "workbench", "playback.json")

	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func loadPlayback() (*PlaybackState, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	path := filepath.Join(dir, "workbench", "playback.json")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state PlaybackState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func openItem(item plugin.Item, providerName string) tea.Cmd {
	if item.URL == "" {
		return nil
	}
	return func() tea.Msg {
		wblog.Info("main", fmt.Sprintf("openItem: url=%s provider=%s", item.URL, providerName))
		if strings.HasPrefix(item.URL, "music://") || strings.HasPrefix(item.URL, "music-shuffle://") {
			isShuffle := strings.HasPrefix(item.URL, "music-shuffle://")
			queue, targets, err := media.Resolve(item.URL, item)
			if err != nil {
				wblog.Error("main", fmt.Sprintf("media resolution failed: %v", err))
				return nil
			}
			if len(targets) == 0 {
				wblog.Warn("main", "no playback targets found for music URL")
				return nil
			}
			p := player.NewMPV()
			cmd, err := p.Start(targets, isShuffle)
			if err != nil {
				wblog.Error("main", fmt.Sprintf("player start failed: %v", err))
				return nil
			}
			return playbackStartedMsg{
				cmd:          cmd,
				player:       p,
				queue:        queue,
				providerName: providerName,
			}
		}
		cmd := exec.Command("open", item.URL) //nolint:gosec
		if err := cmd.Run(); err != nil {
			wblog.Warn("main", fmt.Sprintf("open url=%s err=%v", item.URL, err))
		}
		return nil
	}
}

// waitForPlayback finishes waits for the given command to exit and sends a
// playbackFinishedMsg.
func waitForPlayback(cmd *exec.Cmd) tea.Cmd {
	return func() tea.Msg {
		_ = cmd.Wait()
		return playbackFinishedMsg{cmd: cmd}
	}
}

// watchPlayer polls the player for the current track index and sends a playerTrackChangedMsg.
func watchPlayer(p player.Player) tea.Cmd {
	return tea.Tick(1*time.Second, func(time.Time) tea.Msg {
		if p == nil {
			return nil
		}
		idx, err := p.QueryTrackIndex()
		if err != nil {
			return nil
		}
		return playerTrackChangedMsg{index: idx}
	})
}

// visibleItems returns items for the given pane, applying the pane's active
// filter if one is set.  In /f mode non-matching items are hidden; in /s and
// /F modes all items are returned (highlighting/dimming is handled at render
// time).  When there is no active query the full item list is returned.
func (m model) visibleItems(paneIdx int) []plugin.Item {
	p := m.panes[paneIdx]
	if p.searchMode == "f" && p.searchQuery != "" {
		q := strings.ToLower(p.searchQuery)
		var out []plugin.Item
		for _, it := range p.items {
			if itemMatches(it, q) {
				out = append(out, it)
			}
		}
		return out
	}
	return p.items
}

// paneMatches returns the indices (into items) of items that match q
// (case-insensitive).  Used to build the searchMatches slice for /s mode.
func paneMatches(items []plugin.Item, q string) []int {
	if q == "" {
		return nil
	}
	lq := strings.ToLower(q)
	var out []int
	for i, it := range items {
		if itemMatches(it, lq) {
			out = append(out, i)
		}
	}
	return out
}

// itemMatches reports whether item contains q (case-insensitive) in any field.
func itemMatches(it plugin.Item, q string) bool {
	return strings.Contains(strings.ToLower(it.Title), q) ||
		strings.Contains(strings.ToLower(it.Subtitle), q) ||
		strings.Contains(strings.ToLower(it.Meta), q)
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// View renders the entire TUI.
func (m model) View() string {
	// Convert config rows to layout.RowConfig.
	layoutRows := make([]layout.RowConfig, len(m.cfg.Layout.Rows))
	for i, row := range m.cfg.Layout.Rows {
		panes := make([]layout.PaneConfig, len(row.Panes))
		for j, pane := range row.Panes {
			panes[j] = layout.PaneConfig{
				Provider: pane.Provider,
				Weight:   pane.Weight,
			}
		}
		layoutRows[i] = layout.RowConfig{
			Weight: row.Weight,
			Panes:  panes,
		}
	}

	// Determine upfront whether a footer row will be shown, so both the
	// dims pass and the render pass use the same reservedRows value.
	ap := m.panes[m.active]
	footerVisible := m.mode == modeLogin || m.loginErr != nil ||
		ap.searchMode != "" || ap.searchInput.Focused()
	reservedRows := 0
	if footerVisible {
		reservedRows = 1
	}

	// First pass: compute pane dimensions with placeholder content so we know
	// each pane's content height before we render the real content.
	_, dims := layout.Render(layoutRows, map[string]string{}, map[string]string{}, m.panes[m.active].providerName, m.cfg.Theme.ActivePaneColor, m.termW, m.termH, reservedRows)

	// Second pass: render each pane using its computed content height.
	views := make(map[string]string, len(m.panes))
	titles := make(map[string]string, len(m.panes))
	for i, p := range m.panes {
		h := 0
		if d, ok := dims[p.providerName]; ok {
			h = d.ContentHeight
		}
		w := 0
		if d, ok := dims[p.providerName]; ok {
			w = d.Width
		}
		views[p.providerName] = m.renderPane(i, h, w)

		// Construct border title
		title := strings.ToUpper(p.providerName)
		if m.nowPlayingActive && m.nowPlayingProvider == p.providerName {
			title = "▶ " + truncate(m.nowPlayingQueue[m.nowPlayingIndex].Title, 40)
		} else {
			switch p.searchMode {
			case "s":
				title += fmt.Sprintf(" [search: %q %d match(es)]", p.searchQuery, len(p.searchMatches))
			case "f":
				count := len(paneMatches(p.items, p.searchQuery))
				title += fmt.Sprintf(" [filter: %q %d]", p.searchQuery, count)
			case "F":
				title += fmt.Sprintf(" [dim: %q]", p.searchQuery)
			case "pending":
				title += " [/s search /f filter /F dim]"
			}
		}
		titles[p.providerName] = title
	}

	// Build footer for the active pane's search state or login mode.
	var footer string
	switch m.mode {
	case modeLogin:
		footer = overlayKeyStyle.Render("Login:") +
			footerStyle.Render("  g: GitHub  a: Atlassian/Jira  esc: cancel")
	default:
		if m.loginErr != nil {
			footer = errorStyle.Render(" login error: " + m.loginErr.Error())
		}
		// Per-pane search footer for the active pane.
		p := &m.panes[m.active]
		if p.searchInput.Focused() {
			footer = searchBarStyle.Render("/"+p.searchMode) + p.searchInput.View() +
				footerStyle.Render("  enter: apply  esc: cancel")
		} else if p.searchMode == "pending" {
			footer = searchBarStyle.Render("/") + footerStyle.Render("s: search  f: filter  F: dim  esc: cancel")
		} else if p.searchMode == "s" && len(p.searchMatches) > 0 {
			footer = metaStyle.Render(fmt.Sprintf(
				" search: %q  match %d/%d  n: next  N: prev  esc: clear",
				p.searchQuery, p.searchCursor+1, len(p.searchMatches),
			))
		} else if p.searchMode == "s" && p.searchQuery != "" {
			footer = searchNoMatchStyle.Render(fmt.Sprintf(" search: %q  no matches  esc: clear", p.searchQuery))
		} else if (p.searchMode == "f" || p.searchMode == "F") && p.searchQuery != "" {
			count := len(paneMatches(p.items, p.searchQuery))
			footer = matchStyle.Render(fmt.Sprintf(" %s: %q  %d match(es)  esc: clear",
				p.searchMode, p.searchQuery, count))
		}
	}

	body, _ := layout.Render(layoutRows, views, titles, m.panes[m.active].providerName, m.cfg.Theme.ActivePaneColor, m.termW, m.termH, reservedRows)

	var view string
	if footer != "" {
		view = lipgloss.JoinVertical(lipgloss.Left, body, footer)
	} else {
		view = body
	}

	// Render overlays on top.
	if m.mode == modeHelp {
		view = m.renderHelpOverlay(view)
	}
	if m.mode == modeLog {
		view = m.renderLogOverlay(view)
	}
	if m.mode == modeNowPlaying {
		view = m.renderQueueOverlay(view)
	}

	return view
}

// renderHelpOverlay overlays a centered help box over the given base view.
func (m model) renderHelpOverlay(base string) string {
	global := []struct{ key, desc string }{
		{"?", "Toggle this help"},
		{"q / Ctrl+C", "Quit"},
		{"Tab / Shift+Tab", "Switch pane"},
		{"j / ↓", "Move cursor down"},
		{"k / ↑", "Move cursor up"},
		{"J / K", "Cursor ±10"},
		{"Enter", "Open selected item"},
		{"Delete", "Delete selected item"},
		{"R", "Refresh all panes"},
		{"L then g/a", "Login (GitHub/Jira)"},
		{"Ctrl+L", "Open log pane"},
		{"/s <term>", "Search current pane"},
		{"/f <term>", "Filter (hide non-match)"},
		{"/F <term>", "Filter (dim non-match)"},
		{"n / N", "Next / prev match"},
		{"esc", "Clear search/filter"},
	}
	logPane := []struct{ key, desc string }{
		{"Ctrl+L / q / Esc", "Close log pane"},
		{"j / k", "Scroll down / up"},
		{"g / G", "Top / bottom"},
		{"/s <term>", "Reverse search"},
		{"/f <term>", "Filter (hide non-match)"},
		{"/F <term>", "Filter (dim non-match)"},
		{"n / N", "Next / prev match"},
		{"w / e / i", "Quick-filter: warn/err/info"},
	}

	p := m.panes[m.active]
	var paneSpecific []struct{ key, desc string }
	if p.providerName == "music-streamer" || p.providerName == "plex" || p.providerName == "ytmusic" {
		paneSpecific = []struct{ key, desc string }{
			{"Space", "Play / Pause"},
			{"h / l", "Prev / next track"},
			{"s", "Shuffle"},
			{"a", "Add to Queue"},
			{"n", "Now Playing (Queue)"},
		}
	}

	// Helper to build a section.
	buildSection := func(title string, items []struct{ key, desc string }) string {
		var sb strings.Builder
		sb.WriteString(overlayTitleStyle.Render(title))
		sb.WriteString("\n\n")
		for _, s := range items {
			key := overlayKeyStyle.Render(fmt.Sprintf("%-18s", s.key))
			desc := overlayDescStyle.Render(s.desc)
			sb.WriteString(key + "  " + desc + "\n")
		}
		return sb.String()
	}

	globalSection := buildSection("Global", global)
	logSection := buildSection("Log Pane", logPane)
	paneSection := ""
	if len(paneSpecific) > 0 {
		paneSection = buildSection(strings.ToUpper(p.providerName), paneSpecific)
	}

	// Layout into columns.
	top := lipgloss.JoinHorizontal(lipgloss.Top, globalSection, "    ", logSection)
	cols := lipgloss.JoinVertical(lipgloss.Left, top, "\n", paneSection)

	var sb strings.Builder
	sb.WriteString(overlayTitleStyle.Render("Keyboard Shortcuts"))
	sb.WriteString("\n\n")
	sb.WriteString(cols)
	sb.WriteString("\n\n")
	sb.WriteString(metaStyle.Render("press any key to close"))

	box := overlayBorderStyle.Render(sb.String())

	// Overlay by placing the box over the base using lipgloss.Place.
	return lipgloss.Place(
		m.termW, m.termH,
		lipgloss.Center, lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(lipgloss.AdaptiveColor{Light: "0", Dark: "0"}),
	)
}

// logOverlayDims returns the width and height of the log overlay box.
func (m model) logOverlayDims() (w, h int) {
	w = m.termW * 80 / 100
	h = m.termH * 80 / 100
	if w < 40 {
		w = 40
	}
	if h < 10 {
		h = 10
	}
	return
}

// logVisibleHeight returns the number of log content lines visible inside the
// overlay (excluding border rows, title row, and status bar row).
func (m model) logVisibleHeight() int {
	_, h := m.logOverlayDims()
	// 2 border rows + 1 title row + 1 status bar row = 4 overhead
	visible := h - 4
	if visible < 1 {
		visible = 1
	}
	return visible
}

// logDimStyle renders non-matching lines in "dim filter" (/F) mode.
var logDimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

// renderLogOverlay renders the log pane as a centered overlay on top of base.
func (m model) renderLogOverlay(base string) string {
	boxW, boxH := m.logOverlayDims()
	innerW := boxW - 2 // subtract border cols
	visH := m.logVisibleHeight()

	// Title row.
	modeLabel := ""
	switch m.logSearchMode {
	case "s":
		modeLabel = " [search]"
	case "f":
		modeLabel = " [filter]"
	case "F":
		modeLabel = " [dim-filter]"
	case "pending":
		modeLabel = " [/s search  /f filter  /F dim-filter]"
	}
	titleLine := overlayTitleStyle.Render("LOGS") + metaStyle.Render(modeLabel)

	// Build a set of matched line indices for highlighting.
	matchSet := make(map[int]bool, len(m.logMatches))
	for _, idx := range m.logMatches {
		matchSet[idx] = true
	}

	type displayLine struct {
		text    string
		matched bool
		lineIdx int // original index into m.logLines
	}
	var displayLines []displayLine

	q := strings.ToLower(m.logQuery)
	for i, l := range m.logLines {
		matched := q == "" || strings.Contains(l.Lower, q)
		if m.logSearchMode == "f" && m.logQuery != "" && !matched {
			continue // hide non-matching lines in /f filter mode
		}
		displayLines = append(displayLines, displayLine{
			text:    l.Raw,
			matched: matchSet[i],
			lineIdx: i,
		})
	}

	// The line index of the current search match cursor (for the ▶ indicator).
	currentMatchLineIdx := -1
	if m.logSearchMode == "s" && len(m.logMatches) > 0 {
		currentMatchLineIdx = m.logMatches[m.logMatchCursor]
	}

	// Clamp scroll offset.
	totalLines := len(displayLines)
	scrollOffset := m.logScroll
	if totalLines <= visH {
		scrollOffset = 0
	} else if scrollOffset > totalLines-visH {
		scrollOffset = totalLines - visH
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}

	end := scrollOffset + visH
	if end > totalLines {
		end = totalLines
	}
	var window []displayLine
	if scrollOffset < len(displayLines) {
		window = displayLines[scrollOffset:end]
	}

	var sb strings.Builder
	sb.WriteString(titleLine + "\n")

	for _, dl := range window {
		isCurrent := dl.lineIdx == currentMatchLineIdx
		cursor := "  "
		if isCurrent {
			cursor = "▶ "
		}
		// Reserve 2 chars for the cursor prefix.
		maxContent := innerW - 2
		if maxContent < 1 {
			maxContent = 1
		}
		runes := []rune(dl.text)
		if len(runes) > maxContent {
			runes = runes[:maxContent]
		}
		line := cursor + string(runes)
		var rendered string
		switch {
		case isCurrent:
			rendered = selectedStyle.Render(line)
		case dl.matched && q != "":
			rendered = matchStyle.Render(line)
		case m.logSearchMode == "F" && m.logQuery != "" && !dl.matched:
			rendered = logDimStyle.Render(line)
		default:
			rendered = normalStyle.Render(line)
		}
		sb.WriteString(rendered + "\n")
	}

	// Pad remaining rows so the box fills to visH content rows.
	written := len(window)
	for written < visH {
		sb.WriteString("\n")
		written++
	}

	// Status bar at the bottom of the content area.
	var statusStr string
	if m.logInput.Focused() {
		statusStr = searchBarStyle.Render("/"+m.logSearchMode) + m.logInput.View() +
			footerStyle.Render("  enter: apply  esc: cancel")
	} else if m.logSearchMode == "s" && len(m.logMatches) > 0 {
		statusStr = metaStyle.Render(fmt.Sprintf(
			"match %d/%d  n: next  N: prev  esc: clear",
			m.logMatchCursor+1, len(m.logMatches),
		))
	} else if m.logQuery != "" {
		statusStr = matchStyle.Render(fmt.Sprintf("query: %q  %d lines", m.logQuery, len(displayLines))) +
			footerStyle.Render("  esc: clear")
	} else {
		pct := 0
		if totalLines > visH {
			pct = (scrollOffset + visH) * 100 / totalLines
			if pct > 100 {
				pct = 100
			}
		} else {
			pct = 100
		}
		statusStr = metaStyle.Render(fmt.Sprintf(
			"%d lines  %d%%  /s search  /f filter  /F dim  ctrl+l: close",
			totalLines, pct,
		))
	}
	sb.WriteString(statusStr)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#00008B")).
		Width(innerW).
		Height(boxH - 2).
		Render(sb.String())

	return lipgloss.Place(
		m.termW, m.termH,
		lipgloss.Center, lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(lipgloss.AdaptiveColor{Light: "0", Dark: "0"}),
	)
}

// renderQueueOverlay renders the current playback queue as a centered overlay.
func (m model) renderQueueOverlay(base string) string {
	boxW := m.termW * 60 / 100
	boxH := m.termH * 60 / 100
	innerH := boxH - 4 // border + title + padding

	// Clamp scroll window to queueCursor.
	if m.queueCursor < m.queueScroll {
		m.queueScroll = m.queueCursor
	} else if m.queueCursor >= m.queueScroll+innerH {
		m.queueScroll = m.queueCursor - innerH + 1
	}

	var sb strings.Builder
	sb.WriteString(overlayTitleStyle.Render("Now Playing Queue") + "\n\n")

	if !m.nowPlayingActive || len(m.nowPlayingQueue) == 0 {
		sb.WriteString(normalStyle.Render("Queue is empty."))
	} else {
		end := m.queueScroll + innerH
		if end > len(m.nowPlayingQueue) {
			end = len(m.nowPlayingQueue)
		}
		for i := m.queueScroll; i < end; i++ {
			item := m.nowPlayingQueue[i]
			prefix := "  "
			if i == m.nowPlayingIndex {
				prefix = "▶ "
			}

			line := prefix + truncate(item.Title, 50)
			if i == m.queueCursor {
				sb.WriteString(selectedStyle.Render(line) + "\n")
			} else {
				sb.WriteString(normalStyle.Render(line) + "\n")
			}
		}
	}

	sb.WriteString("\n\n" + metaStyle.Render("press esc to close"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#00008B")).
		Width(boxW).
		Height(boxH).
		Render(sb.String())

	return lipgloss.Place(
		m.termW, m.termH,
		lipgloss.Center, lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(lipgloss.AdaptiveColor{Light: "0", Dark: "0"}),
	)
}

// renderPane produces the content string for a single pane.
// contentHeight is the number of rows available for content (excluding the
// border); a value of 0 means unconstrained (used during the dims-only pass).
func (m model) renderPane(idx, contentHeight, paneWidth int) string {
	p := m.panes[idx]
	active := idx == m.active

	var sb strings.Builder

	if p.stale {
		sb.WriteString(p.spinner.View() + " refreshing…\n")
		if contentHeight > 0 {
			contentHeight--
		}
	}

	if p.loading {
		sb.WriteString(p.spinner.View())
		sb.WriteString(" Loading…")
		return sb.String()
	}

	if p.err != nil {
		sb.WriteString(errorStyle.Render("Error: " + p.err.Error()))
		return sb.String()
	}

	items := m.visibleItems(idx)

	if len(items) == 0 {
		if p.searchMode != "" && p.searchQuery != "" {
			sb.WriteString(searchNoMatchStyle.Render("No matches."))
		} else {
			sb.WriteString(normalStyle.Render("No items."))
		}
		return sb.String()
	}

	q := strings.ToLower(p.searchQuery)

	// Build a set of matched item indices for /s and /F rendering.
	matchSet := make(map[int]bool, len(p.searchMatches))
	for _, mi := range p.searchMatches {
		matchSet[mi] = true
	}

	// contentHeight is what renderPane should fill.
	itemRows := contentHeight
	if itemRows < 1 {
		itemRows = len(items) // unconstrained
	}

	// If content overflows we'll show a scroll indicator, which itself takes a
	// row. Reserve that row from the item budget.
	showIndicator := len(items) > itemRows
	if showIndicator && itemRows > 1 {
		itemRows--
	}

	// Clamp scroll offset so cursor is always visible.
	scrollOffset := 0
	if p.cursor >= itemRows {
		scrollOffset = p.cursor - itemRows + 1
	}
	// Clamp to valid range.
	maxOffset := len(items) - itemRows
	if maxOffset < 0 {
		maxOffset = 0
	}
	if scrollOffset > maxOffset {
		scrollOffset = maxOffset
	}

	end := scrollOffset + itemRows
	if end > len(items) {
		end = len(items)
	}
	window := items[scrollOffset:end]

	filterMode := p.searchMode == "f"

	for i, item := range window {
		absIdx := scrollOffset + i
		isCurrent := absIdx == p.cursor && active

		var origIdx int
		if filterMode {
			origIdx = -1
			if q != "" {
				n := 0
				for oi, it := range p.items {
					if itemMatches(it, strings.ToLower(q)) {
						if n == absIdx {
							origIdx = oi
							break
						}
						n++
					}
				}
			}
		} else {
			origIdx = absIdx
		}

		cursorStr := "  "
		if isCurrent {
			cursorStr = "▶ "
		}

		availW := paneWidth - 6 // 2 border chars, 2 padding chars, 2 cursor chars
		if availW < 10 {
			availW = 10
		}

		metaStr := truncate(item.Meta, 15)
		metaW := runewidth.StringWidth(metaStr)
		if metaW > 0 {
			metaW += 1 // space before meta
		}

		subStr := truncate(item.Subtitle, 20)
		subW := runewidth.StringWidth(subStr)
		if subW > 0 {
			subW += 1 // space before subtitle
		}

		titleW := availW - subW - metaW
		if titleW < 5 {
			titleW = 5
		}

		title := truncate(item.Title, titleW)
		subtitle := subtitleStyle.Render(subStr)
		meta := metaStyle.Render(metaStr)

		// Fill remaining space with spaces to push meta to the right
		paddingLen := availW - runewidth.StringWidth(title) - subW - metaW
		if paddingLen < 0 {
			paddingLen = 0
		}

		var line string
		if subStr != "" && metaStr != "" {
			line = fmt.Sprintf("%s%s %s %s", title, strings.Repeat(" ", paddingLen), subtitle, meta)
		} else if subStr != "" {
			line = fmt.Sprintf("%s%s %s", title, strings.Repeat(" ", paddingLen), subtitle)
		} else if metaStr != "" {
			line = fmt.Sprintf("%s%s %s", title, strings.Repeat(" ", paddingLen), meta)
		} else {
			line = title
		}

		isMatch := origIdx >= 0 && matchSet[origIdx]
		isCurrentMatch := p.searchMode == "s" && len(p.searchMatches) > 0 &&
			origIdx == p.searchMatches[p.searchCursor]

		var rendered string
		switch {
		case isCurrent:
			rendered = selectedStyle.Render(cursorStr + line)
		case isCurrentMatch:
			rendered = selectedStyle.Render(cursorStr + line)
		case isMatch && (p.searchMode == "s" || p.searchMode == "F"):
			rendered = matchStyle.Render(cursorStr + line)
		case p.searchMode == "F" && q != "" && !isMatch:
			rendered = logDimStyle.Render(cursorStr + line)
		case item.Highlighted:
			rendered = highlightedStyle.Render(cursorStr + line)
		default:
			rendered = normalStyle.Render(cursorStr + line)
		}

		sb.WriteString(rendered)
		sb.WriteString("\n")
	}

	// Scroll indicator: show position when content overflows.
	if showIndicator {
		pct := 0
		if maxOffset > 0 {
			pct = scrollOffset * 100 / maxOffset
		}
		indicator := metaStyle.Render(fmt.Sprintf("↑↓ %d/%d (%d%%)", scrollOffset+1, len(items), pct))
		sb.WriteString(indicator)
	}

	return sb.String()
}

// truncate shortens s to at most n runes, appending "…" if it was shortened.
// truncate shortens s to at most n visible cells, appending "…" if shortened.
func truncate(s string, n int) string {
	if runewidth.StringWidth(s) <= n {
		return s
	}
	return runewidth.Truncate(s, n, "…")
}

// ---------------------------------------------------------------------------
// Plugin discovery
// ---------------------------------------------------------------------------

// pluginsDir returns the path to the plugins directory:
//
//	$XDG_CONFIG_HOME/workbench/plugins/   or   ~/.config/workbench/plugins/
func pluginsDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(base, "workbench", "plugins")
}

// discoverPlugins scans pluginsDir for executable files and returns a
// SubprocessProvider for each one, keyed by filename.  Non-executable files
// are silently skipped.  Missing directory is not an error.
func discoverPlugins(cfg Config) map[string]plugin.Provider {
	dir := pluginsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			wblog.Info("main", fmt.Sprintf("plugins dir not found: %s", dir))
		} else {
			wblog.Warn("main", fmt.Sprintf("plugins dir read error: %v", err))
		}
		return map[string]plugin.Provider{}
	}

	providers := make(map[string]plugin.Provider, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		// Only include executable files.
		if info.Mode()&fs.ModeType != 0 {
			continue // skip symlinks, devices, etc. (regular files only)
		}
		if info.Mode().Perm()&0o111 == 0 {
			continue // not executable
		}
		name := e.Name()
		path := filepath.Join(dir, name)
		pluginCfg := cfg.Plugins[name] // nil → empty map (handled by NewSubprocessProvider)
		providers[name] = plugin.NewSubprocessProvider(name, path, pluginCfg)
		wblog.Info("main", fmt.Sprintf("registered plugin %s at %s", name, path))
	}
	return providers
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workbench: %v\n", err)
		os.Exit(1)
	}

	// Initialise the logger before anything else so plugins can log freely.
	if err := wblog.Init("", cfg.Log.MaxLines); err != nil {
		fmt.Fprintf(os.Stderr, "workbench: log init: %v\n", err)
		os.Exit(1)
	}
	wblog.Info("main", "workbench starting")

	providers := discoverPlugins(cfg)

	m := initialModel(cfg, providers)

	prog := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "workbench: %v\n", err)
		os.Exit(1)
	}
}

// loadSearchHistory reads recent searches from ~/.config/workbench/history.json.
func loadSearchHistory() []string {
	var history []string
	cfgDir := os.Getenv("XDG_CONFIG_HOME")
	if cfgDir == "" {
		cfgDir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	path := filepath.Join(cfgDir, "workbench", "history.json")
	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, &history)
	}
	return history
}

// saveSearchHistory writes recent searches to ~/.config/workbench/history.json.
func saveSearchHistory(history []string) {
	cfgDir := os.Getenv("XDG_CONFIG_HOME")
	if cfgDir == "" {
		cfgDir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	dir := filepath.Join(cfgDir, "workbench")
	os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, "history.json")
	data, _ := json.MarshalIndent(history, "", "  ")
	os.WriteFile(path, data, 0644)
}

// addSearchHistory adds a query to the history, limiting to 20 entries.
func addSearchHistory(history *[]string, query string) {
	if query == "" {
		return
	}
	// Remove if already exists (bring to front).
	filtered := make([]string, 0, len(*history))
	for _, h := range *history {
		if h != query {
			filtered = append(filtered, h)
		}
	}
	filtered = append(filtered, query)
	if len(filtered) > 20 {
		filtered = filtered[len(filtered)-20:]
	}
	*history = filtered
	saveSearchHistory(filtered)
}
