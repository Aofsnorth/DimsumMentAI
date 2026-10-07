package world

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"sync"

	"github.com/sandertv/gophertunnel/minecraft/nbt"
)

//go:embed block_states_121.nbt.gz
var blockStates121Gz []byte

var (
	palette121Once   sync.Once
	palette121Names  []string
	palette121Solids []bool
	palette121Airs   []bool
	palette121Tops   []float32
)

func initPalette121() {
	palette121Once.Do(func() {
		zr, err := gzip.NewReader(bytes.NewReader(blockStates121Gz))
		if err != nil {
			return
		}
		defer zr.Close()

		type blockState struct {
			Name       string         `nbt:"name"`
			Properties map[string]any `nbt:"states"`
			Version    int32          `nbt:"version"`
		}

		dec := nbt.NewDecoder(zr)
		palette121Names = make([]string, 0, 22091)
		palette121Solids = make([]bool, 0, 22091)
		palette121Airs = make([]bool, 0, 22091)
		palette121Tops = make([]float32, 0, 22091)

		for {
			var s blockState
			if err := dec.Decode(&s); err != nil {
				break
			}
			palette121Names = append(palette121Names, s.Name)
			isAir := s.Name == "minecraft:air"
			palette121Airs = append(palette121Airs, isAir)
			isPassable := isAir || IsBlockNamePassable(s.Name)
			palette121Solids = append(palette121Solids, !isPassable)
			palette121Tops = append(palette121Tops, TopOffsetFromState(s.Name, s.Properties))
		}
	})
}
