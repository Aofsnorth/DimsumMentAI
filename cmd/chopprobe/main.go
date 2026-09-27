// Command chopprobe replays the tree-chopping packet sequence against a live
// world, one step at a time with timestamps, to identify which packet makes a
// host close the connection without a disconnect reason.
//
// It exists because the real bot was kicked ~300ms after starting a chop on a
// client-hosted LAN world ("host closed the world (no disconnect reason sent)").
// The standalone PlayerAction StartBreak path was confirmed as the kill packet
// (kick 4ms after that one packet, while a heartbeat-only run survives). Modern
// servers set ServerAuthoritativeBlockBreaking in the Start packet, which moves
// block breaking into PlayerAuthInput.BlockActions; this probe validates that
// path against the same host before the fix is built into the bot.
//
// The heartbeat mirrors internal/bot/movement/packet.go's
// buildPlayerAuthInputPacket field-for-field (eye position, Touch input mode,
// BlockBreakingDelayEnabled + VerticalCollision flags, tick counter) so the
// probe is the same client the host already tolerates.
//
// Usage:
//
//	go run ./cmd/chopprobe -config configs/bot.yaml -pos -120,64,178
//	go run ./cmd/chopprobe -config configs/bot.yaml -pos -120,64,178 -sabd
//	go run ./cmd/chopprobe -config configs/bot.yaml -pos -120,64,178 -skipchop
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"bedrock-ai/internal/config"
	"bedrock-ai/internal/connection"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// stepLog records each outbound stage with its send time, so a disconnect can
// be attributed to the packet that immediately preceded it.
type step struct {
	name string
	at   time.Time
}

var (
	stepsMu sync.Mutex
	steps   []step
)

func markStep(name string) {
	stepsMu.Lock()
	defer stepsMu.Unlock()
	steps = append(steps, step{name: name, at: time.Now()})
	fmt.Printf("STEP %-14s %s\n", name, time.Now().Format("15:04:05.000"))
}

func dumpSteps(kickedAt time.Time) {
	stepsMu.Lock()
	defer stepsMu.Unlock()
	fmt.Println("--- step timeline (ms before kick) ---")
	for _, s := range steps {
		fmt.Printf("  %-14s -%dms\n", s.name, kickedAt.Sub(s.at).Milliseconds())
	}
}

// miningPhase drives the SABD block-action sequence on the heartbeat goroutine:
// 0 idle, 1 start (send StartBreak), 2 continuing (ContinueDestroy per tick),
// 3 finish (ContinueDestroy + PredictDestroy on the same tick), 4 abort.
var miningPhase atomic.Int32

func main() {
	configPath := flag.String("config", "configs/bot.yaml", "path to config file")
	posFlag := flag.String("pos", "-120,64,178", "block to break, x,y,z")
	delay := flag.Int("delay", 6, "seconds to settle before the chop sequence")
	hold := flag.Int("hold", 10, "seconds to stay connected after the sequence")
	skipChop := flag.Bool("skipchop", false, "only send the heartbeat, no chop packets (control run)")
	sabd := flag.Bool("sabd", false, "break via PlayerAuthInput.BlockActions (modern server-auth path) instead of standalone PlayerAction")
	breakMs := flag.Int("breakms", 3500, "ms to keep mining before finishing the break")
	flag.Parse()

	var target protocol.BlockPos
	if n, err := fmt.Sscanf(*posFlag, "%d,%d,%d", &target[0], &target[1], &target[2]); err != nil || n != 3 {
		fmt.Fprintf(os.Stderr, "bad -pos %q\n", *posFlag)
		os.Exit(1)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("target=%v host=%s chop=%v sabd=%v\n", target, cfg.Server.Address(), !*skipChop, *sabd)

	dialer := connection.NewDialer(
		cfg.Server,
		login.IdentityData{Identity: uuid.New().String(), DisplayName: cfg.Bot.Name},
		probeClientData(cfg),
	)

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
	fmt.Printf("server flags: ServerAuthoritativeBlockBreaking=%v (movement mode the host negotiated)\n",
		gd.PlayerMovementSettings.ServerAuthoritativeBlockBreaking)

	spawnedAt := time.Now()
	feet := gd.PlayerPosition
	fmt.Printf("spawned at %.2f %.2f %.2f (target is %.1f blocks away horizontally)\n",
		feet.X(), feet.Y(), feet.Z(),
		math.Sqrt(float64((feet.X()-float32(target.X()))*(feet.X()-float32(target.X()))+(feet.Z()-float32(target.Z()))*(feet.Z()-float32(target.Z())))))

	aimYaw, aimPitch := aimAngles(feet.Add(mgl32.Vec3{0, 1.62, 0}), blockCenter(target))

	// Heartbeat: same shape the real bot sends while standing still, plus the
	// SABD block-action sequence when the mining phase says so.
	go func() {
		ticker := time.NewTicker(time.Second / 20)
		defer ticker.Stop()
		var tick uint64
		for range ticker.C {
			tick++
			inputData := protocol.NewInputFlags(packet.InputFlagCount)
			inputData.Set(packet.InputFlagBlockBreakingDelayEnabled)
			inputData.Set(packet.InputFlagVerticalCollision)

			var blockActions []protocol.PlayerBlockAction
			switch miningPhase.Load() {
			case 1:
				blockActions = append(blockActions, protocol.PlayerBlockAction{
					Action: protocol.PlayerActionStartBreak, BlockPos: target, Face: 1,
				})
				miningPhase.Store(2)
				markStep("SABD-StartBreak")
			case 2:
				blockActions = append(blockActions, protocol.PlayerBlockAction{
					Action: protocol.PlayerActionContinueDestroyBlock, BlockPos: target, Face: 1,
				})
			case 3:
				// The real client pairs ContinueDestroy with PredictDestroy on
				// the final tick (see Geyser's BlockBreakHandler).
				blockActions = append(blockActions,
					protocol.PlayerBlockAction{Action: protocol.PlayerActionContinueDestroyBlock, BlockPos: target, Face: 1},
					protocol.PlayerBlockAction{Action: protocol.PlayerActionPredictDestroyBlock, BlockPos: target, Face: 1},
				)
				miningPhase.Store(0)
				markStep("SABD-PredictDestroy")
			case 4:
				blockActions = append(blockActions, protocol.PlayerBlockAction{
					Action: protocol.PlayerActionAbortBreak, BlockPos: target, Face: 1,
				})
				miningPhase.Store(0)
				markStep("SABD-AbortBreak")
			}
			if len(blockActions) > 0 {
				inputData.Set(packet.InputFlagPerformBlockActions)
			}

			_ = conn.WritePacket(&packet.PlayerAuthInput{
				Position:           feet.Add(mgl32.Vec3{0, 1.62, 0}),
				Pitch:              aimPitch,
				Yaw:                aimYaw,
				HeadYaw:            aimYaw,
				InteractPitch:      aimPitch,
				InteractYaw:        aimYaw,
				InputData:          inputData,
				InputMode:          packet.InputModeTouch,
				PlayMode:           packet.PlayModeNormal,
				InteractionModel:   packet.InteractionModelTouch,
				Tick:               tick,
				AnalogueMoveVector: mgl32.Vec2{},
				RawMoveVector:      mgl32.Vec2{},
				BlockActions:       protocol.Option[[]protocol.PlayerBlockAction](blockActions),
			})
		}
	}()

	if !*skipChop {
		go func() {
			time.Sleep(time.Duration(*delay) * time.Second)

			if *sabd {
				// Server-authitative path: phases drive the heartbeat above.
				miningPhase.Store(1)
				time.Sleep(time.Duration(*breakMs) * time.Millisecond)
				miningPhase.Store(3)
				markStep("SABD-SequenceDone")
				return
			}

			// Legacy path, byte-for-byte what the real bot sends today.
			markStep("StartBreak")
			_ = conn.WritePacket(&packet.PlayerAction{
				EntityRuntimeID: gd.EntityRuntimeID,
				ActionType:      protocol.PlayerActionStartBreak,
				BlockPosition:   target,
				BlockFace:       1,
			})
			_ = conn.Flush()

			time.Sleep(100 * time.Millisecond)
			for i := 0; i < *breakMs/90; i++ {
				markStep(fmt.Sprintf("Swing%d", i+1))
				_ = conn.WritePacket(&packet.Animate{
					ActionType:      packet.AnimateActionSwingArm,
					EntityRuntimeID: gd.EntityRuntimeID,
					SwingSource:     packet.AnimateSwingSourceMine,
				})
				_ = conn.Flush()
				time.Sleep(90 * time.Millisecond)
			}

			markStep("CrackBreak")
			_ = conn.WritePacket(&packet.PlayerAction{
				EntityRuntimeID: gd.EntityRuntimeID,
				ActionType:      protocol.PlayerActionCrackBreak,
				BlockPosition:   target,
				BlockFace:       1,
			})
			_ = conn.Flush()

			time.Sleep(50 * time.Millisecond)
			markStep("StopBreak")
			_ = conn.WritePacket(&packet.PlayerAction{
				EntityRuntimeID: gd.EntityRuntimeID,
				ActionType:      protocol.PlayerActionStopBreak,
				BlockPosition:   target,
				BlockFace:       1,
			})
			_ = conn.Flush()
			markStep("SequenceDone")
		}()
	}

	deadline := spawnedAt.Add(time.Duration(*delay+*hold+4) * time.Second)
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(deadline); err != nil {
			break
		}
		pk, err := conn.ReadPacket()
		if err != nil {
			if strings.Contains(err.Error(), "i/o timeout") || strings.Contains(err.Error(), "context deadline exceeded") {
				fmt.Printf("RESULT=SURVIVED seconds=%d\n", int(time.Since(spawnedAt).Seconds()))
				dumpSteps(time.Now())
				return
			}
			kickAt := time.Now()
			fmt.Printf("RESULT=DISCONNECTED alive=%.1fs err=%v\n", kickAt.Sub(spawnedAt).Seconds(), err)
			dumpSteps(kickAt)
			os.Exit(2)
		}
		switch p := pk.(type) {
		case *packet.Disconnect:
			fmt.Printf("SERVER DISCONNECT: %q\n", p.Message)
			os.Exit(3)
		case *packet.UpdateBlock:
			fmt.Printf("  <- UpdateBlock %v -> rid %d\n", p.Position, p.NewBlockRuntimeID)
		case *packet.LevelEvent:
			if p.EventType == 2001 {
				fmt.Println("  <- LevelEvent 2001 (block break particles)")
			}
		case *packet.Text:
			msg := p.Message
			if len(msg) > 120 {
				msg = msg[:120] + "..."
			}
			fmt.Printf("  chat: %q\n", msg)
		}
	}
	fmt.Printf("RESULT=SURVIVED seconds=%d (deadline)\n", int(time.Since(spawnedAt).Seconds()))
	dumpSteps(time.Now())
}

func blockCenter(p protocol.BlockPos) mgl32.Vec3 {
	return mgl32.Vec3{float32(p.X()) + 0.5, float32(p.Y()) + 0.5, float32(p.Z()) + 0.5}
}

// aimAngles is internal/bot/interact/interactor.go's version: Bedrock yaw 0
// faces +Z, pitch positive is down.
func aimAngles(from, to mgl32.Vec3) (yaw, pitch float32) {
	d := to.Sub(from)
	distH := math.Sqrt(float64(d.X()*d.X() + d.Z()*d.Z()))
	if distH < 0.001 {
		distH = 0.001
	}
	yaw = float32(math.Atan2(float64(-d.X()), float64(d.Z())) * 180 / math.Pi)
	if yaw < 0 {
		yaw += 360
	}
	pitch = float32(-math.Atan2(float64(d.Y()), distH) * 180 / math.Pi)
	return yaw, pitch
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
