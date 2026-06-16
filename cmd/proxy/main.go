// Command proxy is a MITM packet recorder for diagnosing the Venity 30-second
// disconnect. It listens locally as a Bedrock server, forwards the connection to
// the real remote (play.venity.net), and records EVERY packet in both directions
// to a JSONL file. Connect a REAL Minecraft client to 127.0.0.1:19132 and play
// for ~40s; the resulting capture shows exactly what the official client sends in
// the first 30 seconds that our bot does not.
//
// Usage:
//
//	go run ./cmd/proxy            # remote defaults to play.venity.net:19132
//	go run ./cmd/proxy -remote play.venity.net:19132 -local 0.0.0.0:19132
//
// Auth: reuses the same configs/token.json the bot uses. If absent, an
// interactive Microsoft login is started (device-code flow in the terminal).
//
// Derived from gophertunnel's MIT-licensed proxy example.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/auth"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"golang.org/x/oauth2"
)

func main() {
	remote := flag.String("remote", "play.venity.net:19132", "remote Bedrock server address host:port")
	local := flag.String("local", "0.0.0.0:19132", "local listen address the real Minecraft client connects to")
	tokenPath := flag.String("token", "configs/token.json", "path to the saved Microsoft Live token (shared with the bot)")
	outPath := flag.String("out", "logs/proxy-capture.jsonl", "path to write the packet capture")
	flag.Parse()

	src, err := tokenSource(*tokenPath)
	if err != nil {
		fmt.Printf("auth: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(filepath.Dir(*outPath), 0755); err != nil {
		fmt.Printf("mkdir out: %v\n", err)
		os.Exit(1)
	}
	out, err := os.Create(*outPath)
	if err != nil {
		fmt.Printf("create out: %v\n", err)
		os.Exit(1)
	}
	defer out.Close()
	rec := &recorder{enc: json.NewEncoder(out)}

	status, err := minecraft.NewForeignStatusProvider(*remote)
	if err != nil {
		fmt.Printf("status provider for %s: %v\n", *remote, err)
		os.Exit(1)
	}

	// AuthenticationDisabled lets our own bot connect to the proxy locally without
	// a second Xbox auth round-trip. The proxy still authenticates to the REMOTE
	// server using its own TokenSource, so Venity only ever sees one authenticated
	// session (the proxy's). A real Minecraft client can still connect too.
	listener, err := minecraft.ListenConfig{
		StatusProvider:         status,
		AuthenticationDisabled: true,
	}.Listen("raknet", *local)
	if err != nil {
		fmt.Printf("listen %s: %v\n", *local, err)
		os.Exit(1)
	}
	defer listener.Close()

	fmt.Printf("MITM proxy listening on %s -> %s\n", *local, *remote)
	fmt.Printf("Capture file: %s\n", *outPath)
	fmt.Println("Now open Minecraft and connect to this machine (e.g. 127.0.0.1:19132).")
	fmt.Println("Stay connected for at least 40 seconds, then disconnect. Ctrl+C to stop the proxy.")

	for {
		c, err := listener.Accept()
		if err != nil {
			fmt.Printf("accept: %v\n", err)
			return
		}
		go handleConn(c.(*minecraft.Conn), listener, *remote, src, rec)
	}
}

func handleConn(conn *minecraft.Conn, listener *minecraft.Listener, remote string, src oauth2.TokenSource, rec *recorder) {
	fmt.Printf("client connected: %s (identity=%s)\n", conn.RemoteAddr(), conn.IdentityData().DisplayName)

	serverConn, err := minecraft.Dialer{
		TokenSource: src,
		ClientData:  conn.ClientData(),
	}.Dial("raknet", remote)
	if err != nil {
		fmt.Printf("dial remote: %v\n", err)
		_ = listener.Disconnect(conn, "proxy failed to reach remote")
		return
	}

	var g sync.WaitGroup
	g.Add(2)
	go func() {
		if err := conn.StartGame(serverConn.GameData()); err != nil {
			fmt.Printf("client StartGame: %v\n", err)
		}
		g.Done()
	}()
	go func() {
		if err := serverConn.DoSpawn(); err != nil {
			fmt.Printf("server DoSpawn: %v\n", err)
		}
		g.Done()
	}()
	g.Wait()

	start := time.Now()
	rec.event("spawn", "both spawned; recording started")

	defer func() {
		_ = serverConn.Close()
		_ = listener.Disconnect(conn, "connection lost")
		rec.event("closed", "session closed")
		fmt.Println("session closed; capture flushed")
	}()

	// Client -> Server
	go func() {
		for {
			pk, err := conn.ReadPacket()
			if err != nil {
				return
			}
			rec.packet("C2S", start, pk)
			if err := serverConn.WritePacket(pk); err != nil {
				var disc minecraft.DisconnectError
				if errors.As(err, &disc) {
					_ = listener.Disconnect(conn, disc.Error())
				}
				return
			}
		}
	}()

	// Server -> Client
	for {
		pk, err := serverConn.ReadPacket()
		if err != nil {
			var disc minecraft.DisconnectError
			if errors.As(err, &disc) {
				rec.event("server_disconnect", disc.Error())
				_ = listener.Disconnect(conn, disc.Error())
			} else {
				rec.event("server_read_err", err.Error())
			}
			return
		}
		rec.packet("S2C", start, pk)
		if err := conn.WritePacket(pk); err != nil {
			return
		}
	}
}

type recorder struct {
	mu  sync.Mutex
	enc *json.Encoder
}

// summarize keeps the capture small and readable: full struct only for the
// packets that matter for the handshake/movement investigation, type-name + a
// few key fields for everything else.
func (r *recorder) packet(dir string, start time.Time, pk packet.Packet) {
	r.mu.Lock()
	defer r.mu.Unlock()
	typeName := reflect.TypeOf(pk).String()
	entry := map[string]any{
		"dir":  dir,
		"ms":   time.Since(start).Milliseconds(),
		"type": typeName,
		"id":   pk.ID(),
	}
	switch p := pk.(type) {
	case *packet.PlayerAuthInput:
		entry["full"] = map[string]any{
			"tick":      p.Tick,
			"position":  vec3(p.Position),
			"moveVec":   vec2(p.MoveVector),
			"delta":     vec3(p.Delta),
			"yaw":       p.Yaw,
			"headYaw":   p.HeadYaw,
			"pitch":     p.Pitch,
			"inputMode": p.InputMode,
			"playMode":  p.PlayMode,
			"flags":     decodeInputFlags(p.InputData),
		}
	case *packet.ClientMovementPredictionSync:
		entry["full"] = map[string]any{
			"entityUniqueID": p.EntityUniqueID,
			"bbWidth":        p.BoundingBoxWidth,
			"bbHeight":       p.BoundingBoxHeight,
			"movementSpeed":  p.MovementSpeed,
		}
	case *packet.SetLocalPlayerAsInitialised:
		entry["full"] = map[string]any{"entityRuntimeID": p.EntityRuntimeID}
	case *packet.RequestChunkRadius:
		entry["full"] = map[string]any{"radius": p.ChunkRadius, "max": p.MaxChunkRadius}
	case *packet.ServerBoundLoadingScreen:
		entry["full"] = map[string]any{"type": p.Type}
	case *packet.Interact:
		entry["full"] = map[string]any{"action": p.ActionType, "target": p.TargetEntityRuntimeID}
	}
	_ = r.enc.Encode(entry)
}

func (r *recorder) event(kind, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.enc.Encode(map[string]any{"event": kind, "msg": msg, "wall": time.Now().Format(time.RFC3339Nano)})
}

// inputFlagNames maps PlayerAuthInput InputData bit indices to readable names,
// in the exact iota order defined by gophertunnel v1.56.2.
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
	"SneakCurrentRaw",
}

func decodeInputFlags(bs protocol.Bitset) []string {
	var set []string
	n := bs.Len()
	for i := 0; i < n; i++ {
		if bs.Load(i) {
			if i < len(inputFlagNames) {
				set = append(set, inputFlagNames[i])
			} else {
				set = append(set, fmt.Sprintf("bit%d", i))
			}
		}
	}
	return set
}

func vec3(v [3]float32) map[string]float32 {
	return map[string]float32{"x": v[0], "y": v[1], "z": v[2]}
}
func vec2(v [2]float32) map[string]float32 {
	return map[string]float32{"x": v[0], "y": v[1]}
}

func tokenSource(path string) (oauth2.TokenSource, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		var tok oauth2.Token
		if json.Unmarshal(data, &tok) == nil && tok.AccessToken != "" {
			fmt.Printf("loaded Microsoft Live token from %s\n", path)
			return auth.RefreshTokenSource(&tok), nil
		}
	}
	fmt.Println("no saved token found; starting interactive Microsoft login...")
	tok, err := auth.RequestLiveToken()
	if err != nil {
		return nil, fmt.Errorf("microsoft oauth login: %w", err)
	}
	if encoded, err := json.Marshal(tok); err == nil {
		_ = os.MkdirAll(filepath.Dir(path), 0755)
		_ = os.WriteFile(path, encoded, 0600)
		fmt.Printf("saved token to %s\n", path)
	}
	return auth.RefreshTokenSource(tok), nil
}
