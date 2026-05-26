package world

import (
	"bytes"

	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/sandertv/gophertunnel/minecraft/nbt"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// palettedResult is a decoded paletted storage with runtime IDs.
type palettedResult struct {
	bitsPerBlock byte
	blocks       []uint32 // raw uint32 words
	palette      []uint32 // runtime ID palette
}

func (wc *WorldCache) precomputeBlockHashes() {
	wc.hashToRID = make(map[uint32]uint32)
	if chunk.RuntimeIDToState == nil {
		return
	}

	count := uint32(0)
	for {
		name, properties, found := chunk.RuntimeIDToState(count)
		if !found {
			break
		}

		sNoVer := struct {
			Name       string         `nbt:"name"`
			Properties map[string]any `nbt:"states"`
		}{Name: name, Properties: properties}

		leBytes, err := nbt.MarshalEncoding(sNoVer, nbt.LittleEndian)
		if err == nil {
			h := fnv1a(leBytes)
			wc.hashToRID[h] = count
		}
		count++
	}
}

func fnv1a(data []byte) uint32 {
	var hash uint32 = 0x811c9dc5
	for _, b := range data {
		hash ^= uint32(b)
		hash *= 0x01000193
	}
	return hash
}

// runtimeIDAt returns the runtime ID stored at local (x, y, z) in [0..15].
func (p *palettedResult) runtimeIDAt(x, y, z byte) uint32 {
	if p.bitsPerBlock == 0 {
		if len(p.palette) > 0 {
			return p.palette[0]
		}
		return 0
	}

	if p.bitsPerBlock > 32 {
		if len(p.palette) > 0 {
			return p.palette[0]
		}
		return 0
	}

	offset := int(uint16(x)<<8|uint16(z)<<4|uint16(y)) * int(p.bitsPerBlock)
	filledBits := int(32 / p.bitsPerBlock * p.bitsPerBlock)
	if filledBits == 0 {
		if len(p.palette) > 0 {
			return p.palette[0]
		}
		return 0
	}
	uint32Offset := offset / filledBits
	bitOffset := uint(offset % filledBits)
	mask := uint32((1 << p.bitsPerBlock) - 1)

	if uint32Offset >= len(p.blocks) {
		return 0
	}
	index := (p.blocks[uint32Offset] >> bitOffset) & mask
	if int(index) >= len(p.palette) {
		return 0
	}
	return p.palette[index]
}

func (wc *WorldCache) decodeNetworkPalettedStorage(buf *bytes.Buffer) (*palettedResult, error) {
	blockSizeByte, err := buf.ReadByte()
	if err != nil {
		return nil, err
	}
	bitsPerBlock := blockSizeByte >> 1

	if bitsPerBlock == 0x7f {
		return &palettedResult{}, nil
	}

	uint32Count := 0
	if bitsPerBlock > 0 {
		if bitsPerBlock > 32 {
			return &palettedResult{bitsPerBlock: 0}, nil
		}
		blocksPerUint32 := 32 / int(bitsPerBlock)
		if blocksPerUint32 == 0 {
			return &palettedResult{bitsPerBlock: 0}, nil
		}
		uint32Count = 4096 / blocksPerUint32
		if 4096%blocksPerUint32 != 0 {
			uint32Count++
		}
	}

	blocks := make([]uint32, uint32Count)
	for i := 0; i < uint32Count; i++ {
		data := buf.Next(4)
		if len(data) < 4 {
			return nil, bytes.ErrTooLarge
		}
		blocks[i] = uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24
	}

	var paletteCount int32 = 1
	if bitsPerBlock != 0 {
		paletteCount, err = readVarint32(buf)
		if err != nil {
			return nil, err
		}
		if paletteCount <= 0 {
			paletteCount = 1
		}
	}

	palette := make([]uint32, paletteCount)
	for i := int32(0); i < paletteCount; i++ {
		v, err := readVarint32(buf)
		if err != nil {
			return nil, err
		}
		palette[i] = wc.TranslateRuntimeID(uint32(v))
	}

	return &palettedResult{
		bitsPerBlock: bitsPerBlock,
		blocks:       blocks,
		palette:      palette,
	}, nil
}

func readVarint32(buf *bytes.Buffer) (int32, error) {
	var val int32
	err := protocol.Varint32(buf, &val)
	return val, err
}
