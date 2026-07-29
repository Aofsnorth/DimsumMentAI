package world

import (
	"bytes"
	"encoding/binary"
	"sort"

	"bedrock-ai/internal/safecast"

	"github.com/df-mc/dragonfly/server/world/chunk"
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

	var scratch []byte
	count := uint32(0)
	airRID, airFound := chunk.StateToRuntimeID("minecraft:air", nil)
	var airHash uint32
	for {
		name, properties, found := chunk.RuntimeIDToState(count)
		if !found {
			break
		}

		hash, sc := networkBlockHash(name, properties, scratch)
		scratch = sc
		wc.hashToRID[hash] = count
		if airFound && count == airRID {
			airHash = hash
		}
		count++
	}
	if wc.logger != nil {
		wc.logger.Info("precomputed block hashes",
			"total", count,
			"air_rid", airRID,
			"air_hash", airHash,
			"air_found", airFound,
		)
	}
}

// networkBlockHash produces the canonical "network block hash" Bedrock uses
// for UseBlockNetworkIDHashes. Algorithm mirrors dragonfly's
// network_block_hash.go: name + sorted properties encoded as raw NBT tags
// (little-endian, no version field) then FNV-1a. The previous implementation
// used nbt.MarshalEncoding, whose byte layout does not match Bedrock's and
// produced hashes that never appeared on the wire.
func networkBlockHash(name string, properties map[string]any, scratch []byte) (uint32, []byte) {
	if name == "minecraft:unknown" {
		return 0xfffffffe, scratch
	}

	keys := make([]string, 0, len(properties))
	for k := range properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	data := scratch[:0]
	writeString := func(str string) {
		data = binary.LittleEndian.AppendUint16(data, safecast.To[uint16](len(str)))
		data = append(data, []byte(str)...)
	}

	data = append(data, 10) // compound
	data = append(data, 0)
	data = append(data, 0)

	data = append(data, 8) // string
	writeString("name")
	writeString(name)

	data = append(data, 10) // compound
	writeString("states")
	for _, k := range keys {
		v := properties[k]
		switch v := v.(type) {
		case string:
			data = append(data, 8)
			writeString(k)
			writeString(v)
		case uint8:
			data = append(data, 1)
			writeString(k)
			data = append(data, v)
		case int8:
			data = append(data, 1)
			writeString(k)
			data = append(data, byte(v))
		case bool:
			b := byte(0)
			if v {
				b = 1
			}
			data = append(data, 1)
			writeString(k)
			data = append(data, b)
		case uint16:
			data = append(data, 2)
			writeString(k)
			data = binary.LittleEndian.AppendUint16(data, v)
		case int16:
			data = append(data, 2)
			writeString(k)
			data = binary.LittleEndian.AppendUint16(data, safecast.To[uint16](int32(v)&0xffff))
		case uint32:
			data = append(data, 3)
			writeString(k)
			data = binary.LittleEndian.AppendUint32(data, v)
		case int32:
			data = append(data, 3)
			writeString(k)
			data = binary.LittleEndian.AppendUint32(data, safecast.To[uint32](int64(v)&0xffffffff))
		default:
			// Skip unknown NBT types — dragonfly panics here, but we prefer a
			// partial hash map over crashing the bot during world load.
			continue
		}
	}
	data = append(data, 0) // end
	data = append(data, 0)

	return fnv1a(data), data
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
	bitOffset := safecast.To[uint](offset % filledBits)
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
	rawPalette := make([]int32, paletteCount)
	for i := int32(0); i < paletteCount; i++ {
		v, err := readVarint32(buf)
		if err != nil {
			return nil, err
		}
		rawPalette[i] = v
		// Bit-cast, not safecast: Bedrock block state hashes are uint32 FNV-1a
		// values that can exceed 2^31. readVarint32 returns them as int32, and
		// safecast clamps negative values to 0 — collapsing every high-bit hash
		// into RID 0 (cyan_terracotta) and making the world look solid.
		palette[i] = wc.TranslateRuntimeID(safecast.To[uint32](int64(v) & 0xffffffff))
	}

	// Diagnostic: dump the first storage's raw + translated palette so we can
	// see exactly what Bedrock sent (hashes or runtime IDs) and how the local
	// hash map handled each entry.
	if wc.logger != nil && len(rawPalette) > 0 && wc.paletteDumpCount < 3 {
		wc.paletteDumpCount++
		lim := 8
		if len(rawPalette) < lim {
			lim = len(rawPalette)
		}
		raw := make([]int32, lim)
		tr := make([]uint32, lim)
		hits := make([]bool, lim)
		copy(raw, rawPalette[:lim])
		copy(tr, palette[:lim])
		for i := 0; i < lim; i++ {
			hits[i] = wc.HashLookupHit(safecast.To[uint32](int64(rawPalette[i]) & 0xffffffff))
		}
		wc.logger.Info("palette dump",
			"bitsPerBlock", bitsPerBlock,
			"paletteCount", paletteCount,
			"raw", raw,
			"translated", tr,
			"hash_hit", hits,
		)
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
