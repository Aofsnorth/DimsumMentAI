package world

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/nbt"
)

// Sign text lives in block entities, which arrive as a run of little-endian
// NBT compounds appended after the block data of a chunk (or, in sub-chunk
// request mode, after the storage data of each SubChunk entry). Nothing else
// in this codebase parses block entities, so without this the bot is
// functionally illiterate: a sign is just another solid block.
//
// maxStoredSigns bounds the map; a big build area can carry a lot of signs and
// every one of them is only a few hundred bytes, but the cap keeps a hostile
// world from growing the map without limit.
const maxStoredSigns = 4096

// scanSignBlockEntities parses data as a sequence of little-endian NBT
// compounds and stores the text of every sign it finds. A decode error simply
// stops the scan: partial data still yields any signs decoded so far, and the
// rest of the cache is unaffected.
func (wc *WorldCache) scanSignBlockEntities(data []byte) {
	if len(data) == 0 {
		return
	}
	dec := nbt.NewDecoderWithEncoding(bytes.NewReader(data), nbt.LittleEndian)
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			return
		}
		id, _ := m["id"].(string)
		if id != "Sign" && id != "HangingSign" {
			continue
		}
		x, xOK := m["x"].(int32)
		y, yOK := m["y"].(int32)
		z, zOK := m["z"].(int32)
		if !xOK || !yOK || !zOK {
			continue
		}
		text, ok := signTextFromNBT(m)
		if !ok || strings.TrimSpace(stripFormatting(text)) == "" {
			continue
		}
		wc.StoreSignText(x, y, z, text)
	}
}

// signTextFromNBT extracts the readable text of a sign. Modern signs (1.20+)
// carry FrontText/BackText compounds; older ones carry a plain Text field, and
// a couple of builds used TextObject1..4. The front face is what a player
// standing in front of the chest reads, so it wins; back text is a fallback.
func signTextFromNBT(m map[string]any) (string, bool) {
	if front, ok := m["FrontText"].(map[string]any); ok {
		if text, ok := front["Text"].(string); ok && text != "" {
			return text, true
		}
	}
	if back, ok := m["BackText"].(map[string]any); ok {
		if text, ok := back["Text"].(string); ok && text != "" {
			return text, true
		}
	}
	if text, ok := m["Text"].(string); ok && text != "" {
		return text, true
	}
	// Legacy text-object signs: four named lines.
	var lines []string
	for _, key := range []string{"TextObject1", "TextObject2", "TextObject3", "TextObject4"} {
		if text, ok := m[key].(string); ok {
			lines = append(lines, text)
		}
	}
	if len(lines) > 0 {
		return strings.Join(lines, "\n"), true
	}
	return "", false
}

// stripFormatting removes Bedrock colour/format codes (§x) from sign text so
// label matching works on what the words actually say.
func stripFormatting(text string) string {
	var sb strings.Builder
	sb.Grow(len(text))
	for i := 0; i < len(text); i++ {
		if text[i] == 0xC2 && i+1 < len(text) && text[i+1] == 0xA7 {
			i++ // skip the § and the code byte after it
			continue
		}
		if text[i] == 0xA7 {
			i++ // bare § byte: skip the code byte after it
			continue
		}
		sb.WriteByte(text[i])
	}
	return sb.String()
}

// StoreSignText records the text of one sign. Position is absolute world
// coordinates.
func (wc *WorldCache) StoreSignText(x, y, z int32, text string) {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	if wc.signTexts == nil {
		wc.signTexts = make(map[string]string)
	}
	if len(wc.signTexts) >= maxStoredSigns {
		if _, exists := wc.signTexts[signKey(x, y, z)]; !exists {
			return
		}
	}
	wc.signTexts[signKey(x, y, z)] = text
}

// SignText returns the stored text of the sign at a position.
func (wc *WorldCache) SignText(x, y, z int32) (string, bool) {
	wc.mu.RLock()
	defer wc.mu.RUnlock()
	if wc.signTexts == nil {
		return "", false
	}
	text, ok := wc.signTexts[signKey(x, y, z)]
	return text, ok
}

// SignCount reports how many signs the cache knows about. Used by tests.
func (wc *WorldCache) SignCount() int {
	wc.mu.RLock()
	defer wc.mu.RUnlock()
	return len(wc.signTexts)
}

func signKey(x, y, z int32) string {
	return fmt.Sprintf("%d,%d,%d", x, y, z)
}
