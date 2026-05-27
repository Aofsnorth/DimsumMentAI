package bot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-gl/mathgl/mgl32"
)

type persistedStateFile struct {
	Positions map[string]persistedPosition `json:"positions"`
}

type persistedPosition struct {
	X       float32   `json:"x"`
	Y       float32   `json:"y"`
	Z       float32   `json:"z"`
	SavedAt time.Time `json:"saved_at"`
}

func (b *Bot) stateKey() string {
	addr := "unknown"
	if b.Conn != nil && b.Conn.RemoteAddr() != nil {
		addr = b.Conn.RemoteAddr().String()
	}
	return fmt.Sprintf("%s|%s", addr, b.Name)
}

func (b *Bot) LoadLastStandingPosition() (mgl32.Vec3, bool) {
	state, err := b.readStateFile()
	if err != nil {
		return mgl32.Vec3{}, false
	}
	pos, ok := state.Positions[b.stateKey()]
	if !ok {
		return mgl32.Vec3{}, false
	}
	return mgl32.Vec3{pos.X, pos.Y, pos.Z}, true
}

func (b *Bot) SaveLastStandingPosition() {
	if b.StatePath == "" {
		return
	}
	b.Mu.Lock()
	pos := b.Pos
	grounded := b.IsGrounded
	b.Mu.Unlock()
	if !grounded {
		return
	}

	state, err := b.readStateFile()
	if err != nil {
		state = persistedStateFile{Positions: make(map[string]persistedPosition)}
	}
	if state.Positions == nil {
		state.Positions = make(map[string]persistedPosition)
	}
	state.Positions[b.stateKey()] = persistedPosition{
		X:       pos.X(),
		Y:       pos.Y(),
		Z:       pos.Z(),
		SavedAt: time.Now(),
	}
	_ = os.MkdirAll(filepath.Dir(b.StatePath), 0o755)
	data, err := json.MarshalIndent(state, "", "  ")
	if err == nil {
		_ = os.WriteFile(b.StatePath, data, 0o600)
	}
}

func (b *Bot) StartPositionSaver(ctxDone <-chan struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctxDone:
			b.SaveLastStandingPosition()
			return
		case <-ticker.C:
			b.SaveLastStandingPosition()
		}
	}
}

func (b *Bot) readStateFile() (persistedStateFile, error) {
	var state persistedStateFile
	if b.StatePath == "" {
		return state, os.ErrNotExist
	}
	data, err := os.ReadFile(b.StatePath)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	if state.Positions == nil {
		state.Positions = make(map[string]persistedPosition)
	}
	return state, nil
}
