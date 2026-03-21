# Media Architecture Refactor Plan

## 1. Objective
Formalize the separation between media plugins (which fetch data and return custom URL schemes like `music://plex/...`) and `workbench` (which translates those schemes into actionable streams, manages the playback queue, and controls the media player). This solidifies `workbench` as the authoritative, secure controller while providing a clear path for future plugin developers to contribute new streaming services via PRs.

## 2. Phase 1: Media Scheme Registry (`internal/media`)
*   **Goal:** Extract the hardcoded scheme translation (e.g., `ytm/`, `plex-playlist/`) from `cmd/workbench/main.go` into a dedicated package.
*   **Tasks:**
    *   Create `internal/media/registry.go` to hold registered media schemes.
    *   Define a `SchemeHandler` interface/function signature: `Resolve(url string) ([]Target, error)`.
    *   Implement handlers for existing schemes: `ytm`, `ytm-playlist`, `plex`, and `plex-playlist`.
    *   Move the Plex HTTP expansion logic out of the TUI into the `plex-playlist` handler.
    *   Add unit tests for the registry and the specific handlers (`media_test.go`).

## 3. Phase 2: Player Abstraction (`internal/player`)
*   **Goal:** Decouple the specific `mpv` process and socket IPC logic from the main UI event loop.
*   **Tasks:**
    *   Create `internal/player/player.go` with an interface `Player` (Play, Pause, Next, Prev, Stop).
    *   Extract the `mpv` socket communication (`queryMPV`, `sendMPVCommand`) and subprocess management into `internal/player/mpv.go`.
    *   *Note:* The queue state (`nowPlayingQueue`, `queueCursor`) will remain in `main.go`'s Bubbletea model, as it is heavily tied to the TUI rendering, but the *actions* will flow through the `Player` interface.

## 4. Phase 3: TUI Integration (`cmd/workbench/main.go`)
*   **Goal:** Wire the new `internal/media` and `internal/player` packages into the TUI.
*   **Tasks:**
    *   Refactor `openItem` to use `media.Resolve(url)` to determine what to play.
    *   Replace direct `sendMPVCommand` calls with `player.Action()` calls.
    *   Ensure all `tea.Msg` types (like `playbackStartedMsg`) still flow correctly to keep the UI in sync.

## 5. Phase 4: Documentation
*   **Goal:** Ensure the architecture docs reflect this clear boundary.
*   **Tasks:**
    *   Update `agent_docs/architecture.md` to mention the `media` and `player` packages.
    *   Update `agent_docs/providers.md` (or add a new guide) to explicitly state how a plugin author should structure a new media plugin and submit a PR to `internal/media/registry.go` for their scheme.
