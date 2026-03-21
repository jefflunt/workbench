package player

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/jluntpcty/workbench/internal/log"
)

// Player is the interface for audio playback control.
type Player interface {
	Start(targets []string, shuffle bool) (*exec.Cmd, error)
	Pause() error
	Next() error
	Prev() error
	PlayIndex(index int) error
	CurrentTimestamp() (float64, error)
	QueryTrackIndex() (int, error)
}

// MPVPlayer implements the Player interface by shelling out to mpv and
// communicating with it over a unix socket.
type MPVPlayer struct {
	SocketPath string
}

func NewMPV() *MPVPlayer {
	return &MPVPlayer{
		SocketPath: filepath.Join(os.TempDir(), "workbench-mpv.sock"),
	}
}

func (m *MPVPlayer) Start(targets []string, shuffle bool) (*exec.Cmd, error) {
	args := []string{"--no-video", "--input-ipc-server=" + m.SocketPath}
	if shuffle {
		args = append(args, "--shuffle")
	}
	args = append(args, targets...)

	log.Info("player", fmt.Sprintf("spawning mpv with %d targets", len(targets)))
	cmd := exec.Command("mpv", args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mpv start failed: %w", err)
	}

	return cmd, nil
}

func (m *MPVPlayer) Pause() error {
	return m.sendCommand("cycle", "pause")
}

func (m *MPVPlayer) Next() error {
	return m.sendCommand("playlist-next")
}

func (m *MPVPlayer) Prev() error {
	return m.sendCommand("playlist-prev")
}

func (m *MPVPlayer) PlayIndex(index int) error {
	return m.sendCommand("playlist-play-index", fmt.Sprintf("%d", index))
}

func (m *MPVPlayer) CurrentTimestamp() (float64, error) {
	return m.queryFloat("playback-time")
}

func (m *MPVPlayer) QueryTrackIndex() (int, error) {
	val, err := m.queryFloat("playlist-pos")
	if err != nil {
		return 0, err
	}
	return int(val), nil
}

func (m *MPVPlayer) sendCommand(args ...string) error {
	conn, err := net.Dial("unix", m.SocketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to mpv socket: %w", err)
	}
	defer conn.Close()

	type cmd struct {
		Command []string `json:"command"`
	}
	req, _ := json.Marshal(cmd{Command: args})
	_, err = conn.Write(append(req, '\n'))
	return err
}

func (m *MPVPlayer) queryFloat(property string) (float64, error) {
	conn, err := net.DialTimeout("unix", m.SocketPath, 500*time.Millisecond)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))

	type cmd struct {
		Command []string `json:"command"`
	}
	req, _ := json.Marshal(cmd{Command: []string{"get_property", property}})
	_, _ = conn.Write(append(req, '\n'))

	var buf [512]byte
	n, err := conn.Read(buf[:])
	if err != nil {
		return 0, err
	}

	var resp struct {
		Data  float64 `json:"data"`
		Error string  `json:"error"`
	}
	if err := json.NewDecoder(bytes.NewReader(buf[:n])).Decode(&resp); err != nil {
		return 0, err
	}
	if resp.Error != "success" && resp.Error != "" {
		return 0, fmt.Errorf("mpv error: %s", resp.Error)
	}
	return resp.Data, nil
}
