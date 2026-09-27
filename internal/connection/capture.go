// capture.go implements an opt-in raw packet recorder for diagnosing
// server-specific connection drops.
//
// Why this exists: the Geyser-fronted server goes silent ~0.5s after the bot
// spawns while an idle probe built on the same dial stack survives 40+s, so the
// difference has to be in what the two clients SEND. The bot's own logging only
// covers packets the code explicitly logs, and an external MITM proxy changes
// the network path. Recording from gophertunnel's PacketFunc hook keeps the
// path identical and catches every packet in both directions, including ones
// written by library internals during the login handshake.
//
// Enable with BOT_PACKET_CAPTURE=<file.jsonl>. No effect when unset.
package connection

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// inputFlagNames maps PlayerAuthInput InputData bit indices to readable names,
// in the iota order of gophertunnel v1.62.0 (player_auth_input.go).
var inputFlagNames = []string{
	"Ascend", "Descend", "NorthJump", "JumpDown", "SprintDown", "ChangeHeight",
	"Jumping", "AutoJumpingInWater", "Sneaking", "SneakDown", "Up", "Down",
	"Left", "Right", "UpLeft", "UpRight", "WantUp", "WantDown", "WantDownSlow",
	"WantUpSlow", "Sprinting", "AscendBlock", "DescendBlock", "SneakToggleDown",
	"PersistSneak", "StartSprinting", "StopSprinting", "StartSneaking",
	"StopSneaking", "StartSwimming", "StopSwimming", "StartJumping",
	"StartGliding", "StopGliding", "PerformItemInteraction", "PerformBlockActions",
	"PerformItemStackRequest", "HandledTeleport", "Emoting", "MissedSwing",
	"StartCrawling", "StopCrawling", "StartFlying", "StopFlying",
	"ClientAckServerData", "ClientPredictedVehicle", "PaddlingLeft",
	"PaddlingRight", "BlockBreakingDelayEnabled", "HorizontalCollision",
	"VerticalCollision", "DownLeft", "DownRight", "StartUsingItem",
	"CameraRelativeMovementEnabled", "RotControlledByMoveDirection",
	"StartSpinAttack", "StopSpinAttack", "IsHotbarTouchOnly", "JumpReleasedRaw",
	"JumpPressedRaw", "JumpCurrentRaw", "SneakReleasedRaw", "SneakPressedRaw",
	"SneakCurrentRaw", "InternalUpdate",
}

// hexPreviewBytes caps the raw payload preview per record. PlayerAuthInput and
// other large packets are decoded by name instead; the preview exists to make
// unknown packets greppable.
const hexPreviewBytes = 48

type packetCapture struct {
	mu    sync.Mutex
	file  *os.File
	start time.Time
	// remote is conn.RemoteAddr(), learned after Dial returns. Writes pass
	// (local, remote) as (src, dst) and reads pass (remote, local), so once
	// remote is known the direction of every record is unambiguous. Records
	// from the login handshake (before Dial returns) are tagged "?".
	remote       net.Addr
	clientNames  map[uint32]string
	serverNames  map[uint32]string
	writtenCount map[string]int
	readCount    map[string]int
}

// newPacketCapture opens the capture file and pre-builds the packet ID name
// maps from gophertunnel's pools.
func newPacketCapture(path string) (*packetCapture, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	c := &packetCapture{
		file:         file,
		start:        time.Now(),
		clientNames:  poolNames(packet.NewClientPool()),
		serverNames:  poolNames(packet.NewServerPool()),
		writtenCount: map[string]int{},
		readCount:    map[string]int{},
	}
	c.write(map[string]any{
		"event": "capture-start",
		"wall":  time.Now().Format(time.RFC3339Nano),
	})
	return c, nil
}

func (c *packetCapture) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.file != nil {
		c.writeLocked(map[string]any{
			"event":   "capture-end",
			"written": c.writtenCount,
			"read":    c.readCount,
		})
		_ = c.file.Close()
		c.file = nil
	}
}

// setRemote records the server address so record() can classify direction.
func (c *packetCapture) setRemote(addr net.Addr) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.remote = addr
}

// record is the gophertunnel PacketFunc. Writes arrive as (src=local,
// dst=remote); reads as (src=remote, dst=local).
func (c *packetCapture) record(h packet.Header, payload []byte, src, dst net.Addr) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.file == nil {
		return
	}

	dir := "?"
	names := c.serverNames
	if c.remote != nil {
		if src.String() == c.remote.String() {
			dir = "S2C"
			names = c.serverNames
		} else {
			dir = "C2S"
			names = c.clientNames
		}
	}

	name := fmt.Sprintf("id_%d", h.PacketID)
	if n, ok := names[h.PacketID]; ok {
		name = n
	}
	if dir == "C2S" || dir == "S2C" {
		c.count(dir, name)
	}

	entry := map[string]any{
		"ms":    time.Since(c.start).Milliseconds(),
		"dir":   dir,
		"id":    h.PacketID,
		"name":  name,
		"bytes": len(payload),
		"hex":   hex.EncodeToString(payload[:min(len(payload), hexPreviewBytes)]),
	}
	if h.PacketID == packet.IDPlayerAuthInput && (dir == "C2S" || dir == "?") {
		if decoded := decodePlayerAuthInput(payload); decoded != nil {
			entry["pai"] = decoded
		}
	}
	c.writeLocked(entry)
}

func (c *packetCapture) count(dir, name string) {
	if dir == "C2S" {
		c.writtenCount[name]++
	} else {
		c.readCount[name]++
	}
}

func (c *packetCapture) write(entry map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeLocked(entry)
}

func (c *packetCapture) writeLocked(entry map[string]any) {
	if c.file == nil {
		return
	}
	_ = json.NewEncoder(c.file).Encode(entry)
}

// poolNames builds an ID → short type name map from a gophertunnel packet pool.
func poolNames(pool packet.Pool) map[uint32]string {
	names := make(map[uint32]string, len(pool))
	for id, constructor := range pool {
		pk := constructor()
		names[id] = stringsTrimPrefixStar(fmt.Sprintf("%T", pk))
	}
	return names
}

func stringsTrimPrefixStar(s string) string {
	if len(s) > 0 && s[0] == '*' {
		return s[1:]
	}
	return s
}

// decodePlayerAuthInput unmarshals a raw PlayerAuthInput payload into the
// fields that matter for the connection-drop diff: tick, position, movement
// vectors, and the input flag set. Decode failures (panics inside the reader)
// are swallowed and reported as a nil result.
func decodePlayerAuthInput(payload []byte) (result map[string]any) {
	var pk packet.PlayerAuthInput
	r := protocol.NewReader(bytes.NewReader(payload), 0, false)
	defer func() {
		if recover() != nil {
			result = nil
		}
	}()
	pk.Marshal(r)
	flags := []string{}
	for i := 0; i < int(packet.InputFlagCount); i++ {
		if pk.InputData.Load(i) {
			if i < len(inputFlagNames) {
				flags = append(flags, inputFlagNames[i])
			} else {
				flags = append(flags, fmt.Sprintf("bit%d", i))
			}
		}
	}
	return map[string]any{
		"tick":      pk.Tick,
		"pos":       []float32{pk.Position.X(), pk.Position.Y(), pk.Position.Z()},
		"moveVec":   []float32{pk.MoveVector.X(), pk.MoveVector.Y()},
		"delta":     []float32{pk.Delta.X(), pk.Delta.Y(), pk.Delta.Z()},
		"yaw":       pk.Yaw,
		"headYaw":   pk.HeadYaw,
		"pitch":     pk.Pitch,
		"inputMode": pk.InputMode,
		"playMode":  pk.PlayMode,
		"interact":  pk.InteractionModel,
		"flags":     flags,
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
