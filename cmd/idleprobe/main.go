// Command idleprobe measures whether a connection survives, and whether a
// server command actually runs.
//
// It exists because the bot's symptoms are ambiguous from the outside: a kick, a
// UDP timeout, a wrong command prefix, and a quiet shutdown all end up reading
// as roughly the same line in the log, so every fix was a guess. This tool
// removes the bot's behaviour from the question and reports two facts:
//
//   - how long the connection lives while nobody does anything
//   - what the server says in reply to a command
//
// The -move, -chunks and -radius flags add the rest of what the real bot does on
// a fresh join. The bot survives none of that while this probe does, so the
// flags exist to isolate which piece the server objects to rather than to keep
// guessing at it.
//
// Usage:
//
//	go run ./cmd/idleprobe -config configs/bot.yaml -seconds 40
//	go run ./cmd/idleprobe -config configs/bot.yaml -seconds 40 -move -chunks -radius
//	go run ./cmd/idleprobe -config configs/bot.yaml -seconds 30 -cmd "register pass pass"
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"bedrock-ai/internal/config"
	"bedrock-ai/internal/connection"

	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// heartbeatRate is the PlayerAuthInput rate for -move. It defaults to the real
// bot's 20Hz, because a probe at any other rate is testing a different client
// than the one that breaks.
var heartbeatRate = 20

// chunkRadius matches the value the bot requests on spawn.
const chunkRadius = 8

func main() {
	configPath := flag.String("config", "configs/bot.yaml", "path to config file")
	seconds := flag.Int("seconds", 40, "how long to hold the connection open")
	move := flag.Bool("move", false, "send PlayerAuthInput at the bot's tick rate")
	hz := flag.Int("hz", 20, "PlayerAuthInput rate when -move is set; 0 disables")
	chunks := flag.Bool("chunks", false, "flood SubChunkRequest like the bot does")
	radius := flag.Bool("radius", false, "send RequestChunkRadius like the bot does")
	cmd := flag.String("cmd", "", "run this server command a few seconds after joining")
	twice := flag.Bool("twice", false, "send the command twice like the bot retry does")
	flag.Parse()
	heartbeatRate = *hz
	doubleSend = *twice

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	dialer := connection.NewDialer(
		cfg.Server,
		login.IdentityData{Identity: uuid.New().String(), DisplayName: cfg.Bot.Name},
		// A realistic ClientData: the bot's mergeClientData reports Android and
		// touch, and a proxy can gate on what the client claims to be.
		probeClientData(cfg),
	)

	mode := "still"
	if *move {
		mode = fmt.Sprintf("moving@%dHz", heartbeatRate)
	}
	fmt.Printf("mode=%s chunks=%v radius=%v target=%s\n",
		mode, *chunks, *radius, cfg.Server.Address())

	conn, err := dialer.Dial()
	if err != nil {
		fmt.Fprintf(os.Stderr, "DIAL FAILED: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.DoSpawn(); err != nil {
		fmt.Fprintf(os.Stderr, "SPAWN FAILED: %v\n", err)
		os.Exit(1)
	}

	gd := conn.GameData()
	spawnedAt := time.Now()
	fmt.Printf("spawned at %.1f %.1f %.1f\n", gd.PlayerPosition.X(), gd.PlayerPosition.Y(), gd.PlayerPosition.Z())

	if *move {
		go heartbeat(conn, gd)
	}
	if *radius {
		requestRadius(conn)
	}
	if *chunks {
		go floodChunks(conn)
	}
	if *cmd != "" {
		go runCommand(conn, *cmd)
	}

	deadline := spawnedAt.Add(time.Duration(*seconds) * time.Second)
	lastPacket := time.Now()
	reads := 0

	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(deadline); err != nil {
			break
		}
		pk, err := conn.ReadPacket()
		if err != nil {
			// A deadline error here is the probe's own clock expiring, not the
			// server hanging up. Reporting that as a disconnect sent us chasing
			// a bug that was never there.
			if isOwnDeadline(err) {
				fmt.Printf("RESULT=SURVIVED seconds=%d packets=%d quiet=%.1fs\n",
					int(time.Since(spawnedAt).Seconds()), reads, time.Since(lastPacket).Seconds())
				return
			}
			fmt.Printf("RESULT=DISCONNECTED alive=%.1fs quiet=%.1fs packets=%d err=%v\n",
				time.Since(spawnedAt).Seconds(), time.Since(lastPacket).Seconds(), reads, err)
			os.Exit(2)
		}
		reads++
		lastPacket = time.Now()
		noteInbound(pk)
		report(pk)
	}
}

// isOwnDeadline reports whether the read failed because the probe's own read
// deadline elapsed, rather than because the connection broke.
func isOwnDeadline(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return contains(msg, "deadline exceeded") || contains(msg, "i/o timeout")
}

func contains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}

// probeClientData mirrors the fields the bot's mergeClientData sets.
func probeClientData(cfg *config.Config) login.ClientData {
	cd := login.ClientData{
		CurrentInputMode: 2,
		DefaultInputMode: 2,
		DeviceModel:      "SM-G973F",
		DeviceOS:         1,
		GameVersion:      protocol.CurrentVersion,
		LanguageCode:     "en_US",
		UIProfile:        0,
		DeviceID:         login.DeviceID(uuid.New().String()),
	}
	if cfg.Bot.Language == "Indonesian" {
		cd.LanguageCode = "id_ID"
	}
	return cd
}

// heartbeat writes a PlayerAuthInput at the bot's tick rate so the client is
// not silently frozen.
func heartbeat(conn *minecraft.Conn, gd minecraft.GameData) {
	if heartbeatRate <= 0 {
		return
	}
	ticker := time.NewTicker(time.Second / time.Duration(heartbeatRate))
	defer ticker.Stop()
	for range ticker.C {
		noteSent("PlayerAuthInput")
		_ = conn.WritePacket(&packet.PlayerAuthInput{
			Position: gd.PlayerPosition,
			Yaw:      gd.Yaw,
			Pitch:    gd.Pitch,
		})
	}
}

// requestRadius asks for chunks exactly as the bot does on spawn.
func requestRadius(conn *minecraft.Conn) {
	_ = conn.WritePacket(&packet.RequestChunkRadius{
		ChunkRadius:    chunkRadius,
		MaxChunkRadius: chunkRadius,
	})
	_ = conn.Flush()
	fmt.Printf("--> requested chunk radius %d\n", chunkRadius)
}

// floodChunks reproduces the bot's sub-chunk requester: a 5x5 block of columns
// every second. This is the one behaviour the bot does that a plain client
// never would, so it is the prime suspect when a server goes quiet on a bot but
// not on a real player.
func floodChunks(conn *minecraft.Conn) {
	requested := make(map[[2]int32]time.Time)
	const retry = 20 * time.Second
	targets := make([][2]int32, 0, 25)
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			targets = append(targets, [2]int32{int32(dx), int32(dz)})
		}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	total := 0
	for range ticker.C {
		now := time.Now()
		sent := 0
		for _, t := range targets {
			if last, ok := requested[t]; ok && now.Sub(last) < retry {
				continue
			}
			requested[t] = now
			_ = conn.WritePacket(&packet.SubChunkRequest{
				Dimension: 0,
				Position:  protocol.SubChunkPos{t[0], 0, t[1]},
				Offsets:   subChunkOffsets(),
			})
			sent++
		}
		total += sent
		_ = conn.Flush()
		if sent > 0 {
			fmt.Printf("--> subchunk tick: %d sent (total %d)\n", sent, total)
		}
	}
}

// subChunkOffsets mirrors the vertical window the bot asks for: a full overworld
// column, 24 sub-chunks from -64 to 319.
func subChunkOffsets() []protocol.SubChunkOffset {
	offsets := make([]protocol.SubChunkOffset, 0, 24)
	for y := int8(-4); y <= 19; y++ {
		offsets = append(offsets, protocol.SubChunkOffset{0, y, 0})
	}
	return offsets
}

// runCommand sends a server command shortly after joining. The question is not
// "did we write the packet" but "did the server answer", and only the reply
// distinguishes a working command from a silently ignored one.
func runCommand(conn *minecraft.Conn, line string) {
	// Wait for the world to finish arriving. A command sent during the spawn
	// burst is answered by a proxy that has not registered the player yet, which
	// looks identical to the command being rejected.
	time.Sleep(5 * time.Second)

	command := line
	if command[0] != '/' {
		command = "/" + command
	}
	fmt.Printf("--> sending command %q\n", command)

	_ = conn.WritePacket(&packet.CommandRequest{
		CommandLine: command,
		CommandOrigin: protocol.CommandOrigin{
			Origin: protocol.CommandOriginPlayer,
			UUID:   uuid.New(),
		},
		Version: protocol.CurrentVersion,
	})
	noteSent("CommandRequest")
	fmt.Println("--> command packet written")

	if doubleSend {
		time.Sleep(1200 * time.Millisecond)
		_ = conn.WritePacket(&packet.CommandRequest{
			CommandLine: command,
			CommandOrigin: protocol.CommandOrigin{
				Origin: protocol.CommandOriginPlayer,
				UUID:   uuid.New(),
			},
			Version: protocol.CurrentVersion,
		})
		_ = conn.Flush()
		fmt.Println("--> second command packet written")
	}
}

var doubleSend bool

// sentCounts records which outbound packet types this client used. A surviving
// probe and a dying bot differ in what they SEND, not what they receive, so the
// comparison that matters is on the write side.
var sentCounts = map[string]int{}
var sentMu sync.Mutex

func noteSent(name string) {
	sentMu.Lock()
	sentCounts[name]++
	sentMu.Unlock()
}

func noteInbound(pk packet.Packet) {
	name := strings.TrimPrefix(fmt.Sprintf("%T", pk), "*packet.")
	sentMu.Lock()
	sentCounts["<-"+name]++
	sentMu.Unlock()
}

func dumpSent() {
	sentMu.Lock()
	defer sentMu.Unlock()
	keys := make([]string, 0, len(sentCounts))
	for k := range sentCounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %-40s %d\n", k, sentCounts[k])
	}
}

func report(pk packet.Packet) {
	switch p := pk.(type) {
	case *packet.CommandOutput:
		for _, m := range p.OutputMessages {
			fmt.Printf("  COMMAND OUTPUT: %q\n", m.Message)
		}
	case *packet.Text:
		msg := p.Message
		if len(msg) > 140 {
			msg = msg[:140] + "..."
		}
		fmt.Printf("  chat: %q\n", msg)
	case *packet.Disconnect:
		fmt.Printf("  SERVER DISCONNECT: %q\n", p.Message)
		os.Exit(3)
	}
}
