package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot/building"
	"bedrock-ai/internal/bot/combat"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/gathering"
	"bedrock-ai/internal/bot/inventory"
	"bedrock-ai/internal/bot/pathfinder"
	"bedrock-ai/internal/config"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/handler"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

type Bot struct {
	logger     *slog.Logger
	conn       *minecraft.Conn
	dialer     DialerFunc
	registry   *handler.Registry
	bus        *event.Bus
	name       string
	protoSkin  protocol.Skin
	playerUUID uuid.UUID

	// AI and configuration
	AiClient  *ai.NvidiaClient
	Throttler *ai.MessageThrottler
	AiCfg     config.AIConfig

	// Player Tracking
	playerEntityIDs map[string]uint64
	playerUsernames map[uint64]string
	playerPositions map[uint64]mgl32.Vec3
	playerUUIDs     map[uuid.UUID]string

	// Actor Tracking
	actors              map[uint64]*entity.Info
	uniqueIDToRuntimeID map[int64]uint64

	// Subsystems
	combatMgr    *combat.CombatManager
	threatDet    *combat.ThreatDetector
	gatherer     *gathering.ResourceGatherer
	inventoryMgr *inventory.InventoryManager
	builderAgent *building.BuilderAgent

	// Movement & Steering
	movementState    string // "idle", "walk_to", "follow"
	targetPos        mgl32.Vec3
	targetPlayerName string

	// Look angles
	yaw   float32
	pitch float32

	// A* Pathfinding
	worldModel            *pathfinder.LocalWorldModel
	currentPath           []pathfinder.Node
	pathIndex             int
	ticksStuck            int
	lastTickPos           mgl32.Vec3
	lastPathRecalcTime    time.Time
	consecutiveStuckCount int

	// Health & Hunger tracking
	health int
	hunger int

	// Inventory tracking
	inventoryMap map[uint32]protocol.ItemStack
	itemNames    map[int32]string
	recipes      map[string]uint32
	heldSlot     uint32

	// Emotes / Animations state
	emoteState string
	emoteTicks int

	// Internal bot messages tracked to prevent loops
	recentBotMessages map[string]time.Time

	mu  sync.Mutex
	pos mgl32.Vec3
}

type DialerFunc func() (*minecraft.Conn, error)

func newBot(opts ...Option) (*Bot, error) {
	b := &Bot{
		playerEntityIDs:     make(map[string]uint64),
		playerUsernames:     make(map[uint64]string),
		playerPositions:     make(map[uint64]mgl32.Vec3),
		playerUUIDs:         make(map[uuid.UUID]string),
		recentBotMessages:   make(map[string]time.Time),
		movementState:       "idle",
		inventoryMap:        make(map[uint32]protocol.ItemStack),
		itemNames:           make(map[int32]string),
		recipes:             make(map[string]uint32),
		health:              20,
		hunger:              20,
		worldModel:          pathfinder.NewLocalWorldModel(),
		actors:              make(map[uint64]*entity.Info),
		uniqueIDToRuntimeID: make(map[int64]uint64),
	}

	for _, opt := range opts {
		opt(b)
	}

	if err := b.validate(); err != nil {
		return nil, fmt.Errorf("validate bot options: %w", err)
	}

	return b, nil
}

func (b *Bot) validate() error {
	if b.logger == nil {
		return fmt.Errorf("logger is required")
	}
	if b.dialer == nil {
		return fmt.Errorf("dialer is required")
	}
	if b.registry == nil {
		return fmt.Errorf("registry is required")
	}
	if b.bus == nil {
		return fmt.Errorf("event bus is required")
	}
	return nil
}

func (b *Bot) Run(ctx context.Context) error {
	conn, err := b.dialer()
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	b.conn = conn
	defer b.conn.Close()

	if parsedUUID, err := uuid.Parse(conn.IdentityData().Identity); err == nil {
		b.playerUUID = parsedUUID
	}

	b.logger.Info("connected to server",
		slog.String("address", conn.RemoteAddr().String()),
	)

	if err := b.conn.DoSpawn(); err != nil {
		return fmt.Errorf("spawn: %w", err)
	}

	gd := b.conn.GameData()
	b.mu.Lock()
	b.pos = gd.PlayerPosition.Sub(mgl32.Vec3{0, 1.62, 0})
	b.yaw = gd.Yaw
	b.pitch = gd.Pitch
	// Initialize item names map from StartGame packet
	for _, entry := range gd.Items {
		b.itemNames[int32(entry.RuntimeID)] = entry.Name
	}
	b.mu.Unlock()

	// Validate spawn position - if in void (y > 320 or y < -64), set to safe height
	spawnY := gd.PlayerPosition.Y() - 1.62
	if spawnY > 320 || spawnY < -64 {
		b.logger.Warn("Bot spawned in void, setting to safe height",
			slog.Float64("y", float64(spawnY)),
		)
		b.mu.Lock()
		b.pos = mgl32.Vec3{gd.PlayerPosition.X(), 100, gd.PlayerPosition.Z()}
		b.mu.Unlock()
	} else {
		b.mu.Lock()
		b.pos = gd.PlayerPosition.Sub(mgl32.Vec3{0, 1.62, 0})
		b.mu.Unlock()
	}

	b.mu.Lock()
	actualPos := b.pos
	b.mu.Unlock()
	b.logger.Info("spawned in world",
		slog.String("name", b.name),
		slog.Float64("x", float64(actualPos.X())),
		slog.Float64("y", float64(actualPos.Y())),
		slog.Float64("z", float64(actualPos.Z())),
	)

	// Initialize subsystems
	b.combatMgr = combat.NewCombatManager(b, b.logger)
	b.threatDet = combat.NewThreatDetector(b, b.combatMgr, b.logger)
	b.gatherer = gathering.NewResourceGatherer(b, b.logger)
	b.inventoryMgr = inventory.NewInventoryManager(b, b.logger)
	b.builderAgent = building.NewBuilderAgent(b, b.logger, b.AiClient)

	b.bus.Publish(event.SpawnEvent{GameData: gd})

	// Tell the server we finished loading
	b.sendLoadingScreenDone()

	// Register chat listener
	b.InitChatListener(ctx)

	// Start sending PlayerAuthInput so the server registers
	// the bot's physical position and broadcasts it to other players.
	go b.sendInputLoop(ctx, gd)

	// Start combat and threat detector loops
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				b.combatMgr.Tick(ctx)
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(1200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				b.threatDet.Scan(ctx)
			}
		}
	}()

	return b.packetLoop(ctx)
}

func (b *Bot) sendLoadingScreenDone() {
	// Type 1: loading screen started (real client sends this first)
	_ = b.conn.WritePacket(&packet.ServerBoundLoadingScreen{
		Type: packet.LoadingScreenTypeStart,
	})
	// Type 2: loading screen finished
	_ = b.conn.WritePacket(&packet.ServerBoundLoadingScreen{
		Type: packet.LoadingScreenTypeEnd,
	})
	b.logger.Info("sent loading screen packets")
}

func (b *Bot) sendPlayerSkin() {
	if len(b.protoSkin.SkinData) == 0 {
		return
	}
	b.protoSkin.OverrideAppearance = true
	b.protoSkin.PrimaryUser = true
	b.protoSkin.Trusted = true

	b.mu.Lock()
	targetUUID := b.playerUUID
	b.mu.Unlock()

	_ = b.conn.WritePacket(&packet.PlayerSkin{
		UUID: targetUUID,
		Skin: b.protoSkin,
	})
	b.logger.Info("sent PlayerSkin packet",
		slog.String("uuid", targetUUID.String()),
		slog.Int("skin_data_len", len(b.protoSkin.SkinData)),
		slog.String("arm_size", b.protoSkin.ArmSize),
	)
}

func (b *Bot) sendInputLoop(ctx context.Context, gd minecraft.GameData) {
	ticker := time.NewTicker(time.Second / 20) // 20 ticks/sec
	defer ticker.Stop()

	tick := uint64(0)

	// Natural idle look-around state variables
	b.mu.Lock()
	initYaw := b.yaw
	initPitch := b.pitch
	b.mu.Unlock()

	var idleLookTargetYaw float32 = initYaw
	var idleLookTargetPitch float32 = initPitch
	var idleLookTicksRemaining int = 0
	var idleLookWaitTicks int = 30 + rand.Intn(50)
	idleLookState := "waiting"

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.mu.Lock()
			currentPos := b.pos
			b.mu.Unlock()

			b.mu.Lock()
			mState := b.movementState
			tPlayer := b.targetPlayerName
			tPos := b.targetPos
			yaw := b.yaw
			pitch := b.pitch
			b.mu.Unlock()

			// Update target position dynamically if following a player
			if mState == "follow" && tPlayer != "" {
				if _, pos, ok := b.FindPlayer(tPlayer); ok {
					b.mu.Lock()
					dx := pos.X() - b.targetPos.X()
					dz := pos.Z() - b.targetPos.Z()
					if dx*dx+dz*dz > 1.0 {
						b.targetPos = pos
						b.lastPathRecalcTime = time.Now()
						b.mu.Unlock()
						b.RecalculatePath()
						b.mu.Lock()
					} else {
						b.targetPos = pos
					}
					tPos = b.targetPos

					timeSinceRecalc := time.Since(b.lastPathRecalcTime)
					hasPath := len(b.currentPath) > 0 && b.pathIndex < len(b.currentPath)
					if (!hasPath || b.ticksStuck > 10) && timeSinceRecalc > 800*time.Millisecond {
						b.lastPathRecalcTime = time.Now()
						b.mu.Unlock()
						b.logger.Debug("Periodic path recalculation triggered", "hasPath", hasPath, "ticksStuck", b.ticksStuck)
						b.RecalculatePath()
						b.mu.Lock()
					}
					b.mu.Unlock()
				}
			}

			// Get current target coordinate along path or targetPos directly if no path
			b.mu.Lock()
			hasPath := len(b.currentPath) > 0 && b.pathIndex < len(b.currentPath)
			var nextTarget mgl32.Vec3
			if hasPath {
				node := b.currentPath[b.pathIndex]
				nextTarget = mgl32.Vec3{float32(node.X) + 0.5, float32(node.Y), float32(node.Z) + 0.5} // target center of block
			} else {
				nextTarget = tPos
			}
			b.mu.Unlock()

			// If path has only 1 node or no path, use direct movement without pathfinding
			if !hasPath || (len(b.currentPath) == 1) {
				nextTarget = tPos // Direct movement to target
				b.mu.Lock()
				b.currentPath = nil // Clear invalid path
				b.mu.Unlock()
			}

			dx := nextTarget.X() - currentPos.X()
			dy := nextTarget.Y() - currentPos.Y()
			dz := nextTarget.Z() - currentPos.Z()
			dist := float32(math.Sqrt(float64(dx*dx + dz*dz)))

			shouldJump := false
			prevPos := currentPos
			var moveVec mgl32.Vec2
			var moveDelta mgl32.Vec3

			// Active steering
			if mState != "idle" {
				// If we are close to the current path node, advance to next node
				if hasPath && dist < 0.8 {
					b.mu.Lock()
					b.pathIndex++
					b.ticksStuck = 0
					b.lastTickPos = currentPos
					if b.pathIndex >= len(b.currentPath) {
						b.currentPath = nil
						if b.movementState == "walk_to" {
							b.movementState = "idle"
							b.logger.Info("bot arrived at target destination", slog.Float64("x", float64(tPos.X())), slog.Float64("z", float64(tPos.Z())))
						}
					}
					b.mu.Unlock()
				} else if !hasPath && dist < 1.5 {
					if mState == "walk_to" {
						b.mu.Lock()
						b.movementState = "idle"
						b.mu.Unlock()
						b.logger.Info("bot arrived at target destination", slog.Float64("x", float64(tPos.X())), slog.Float64("z", float64(tPos.Z())))
					}
				}

				// Stuck/collision detection
				if mState != "idle" {
					b.mu.Lock()
					moveDeltaX := currentPos.X() - b.lastTickPos.X()
					moveDeltaZ := currentPos.Z() - b.lastTickPos.Z()
					if moveDeltaX*moveDeltaX+moveDeltaZ*moveDeltaZ < 0.001 {
						b.ticksStuck++
					} else {
						b.ticksStuck = 0
						b.consecutiveStuckCount = 0
						b.lastTickPos = currentPos
					}

					// If stuck for 20 ticks (1 second), assume target block is solid and recalculate
					if b.ticksStuck >= 20 {
						b.ticksStuck = 0
						b.consecutiveStuckCount++
						b.logger.Debug("Stuck detected, recalculating path", "consecutive_count", b.consecutiveStuckCount, "hasPath", hasPath)

						if hasPath && b.pathIndex < len(b.currentPath) {
							node := b.currentPath[b.pathIndex]
							b.worldModel.SetSolid(node.X, node.Y, node.Z, true)
							b.mu.Unlock()
							b.RecalculatePath()
							b.mu.Lock()
						} else {
							b.mu.Unlock()
							b.RecalculatePath()
							b.mu.Lock()
						}

						// Fallback: if stuck 3+ times consecutively, try direct movement without pathfinding
						if b.consecutiveStuckCount >= 3 {
							b.logger.Warn("Multiple stuck detections, attempting direct movement fallback", "consecutive_count", b.consecutiveStuckCount)
							b.currentPath = nil // Clear path to force direct movement
							b.consecutiveStuckCount = 0
						}
					}
					b.mu.Unlock()
				}

				if dy > 0.5 {
					shouldJump = true
				}

				b.mu.Lock()
				stuckTicks := b.ticksStuck
				b.mu.Unlock()
				if stuckTicks > 5 && stuckTicks < 15 {
					shouldJump = true
				}

				if currentPos.Y() > 320 {
					shouldJump = false
				}

				if dist > 0.05 {
					const speed float32 = 0.15
					var stepX, stepY, stepZ float32
					if dist > 0.01 {
						stepX = (dx / dist) * speed
						stepZ = (dz / dist) * speed
					}

					const maxStepY float32 = 0.3
					stepY = dy
					if stepY > maxStepY {
						stepY = maxStepY
					} else if stepY < -maxStepY {
						stepY = -maxStepY
					}

					if shouldJump && stepY < 0.1 {
						stepY = 0.3
					}

					if currentPos.Y() > 320 && dy < -1 {
						stepY = -2.0
					}

					currentPos = mgl32.Vec3{
						currentPos.X() + stepX,
						currentPos.Y() + stepY,
						currentPos.Z() + stepZ,
					}
					b.mu.Lock()
					b.pos = currentPos
					b.mu.Unlock()

					moveDelta = currentPos.Sub(prevPos)

					yawWorldRad := float64(yaw+90) * math.Pi / 180
					forwardX := float32(math.Cos(yawWorldRad))
					forwardZ := float32(math.Sin(yawWorldRad))

					if dist > 0.01 {
						moveDirX := dx / dist
						moveDirZ := dz / dist
						moveForward := moveDirX*forwardX + moveDirZ*forwardZ
						moveStrafe := moveDirX*(-forwardZ) + moveDirZ*forwardX
						moveVec = mgl32.Vec2{moveStrafe, moveForward}
					}
				}
			}

			// STATE-BASED LOOK CONTROLLER
			var targetYaw float32 = yaw
			var targetPitch float32 = pitch

			if mState == "follow" && tPlayer != "" {
				// Follow mode: primarily look in direction of travel, occasionally glance at player
				// This makes movement look more natural while still tracking the player
				var targetHeadPos mgl32.Vec3
				targetFound := false
				if _, pos, ok := b.FindPlayer(tPlayer); ok {
					targetHeadPos = pos.Add(mgl32.Vec3{0, 1.62, 0}) // look at player head
					targetFound = true
				}

				if dist > 0.1 {
					// Primary: look in direction of travel for natural movement
					yawRad := math.Atan2(float64(dz), float64(dx))
					targetYaw = float32(yawRad*180/math.Pi) - 90
					
					// Calculate pitch based on terrain elevation ahead
					dyH := nextTarget.Y() - (currentPos.Y() + 1.62)
					distH := float32(math.Sqrt(float64(dx*dx + dz*dz)))
					pitchRad := -math.Atan2(float64(dyH), float64(distH))
					targetPitch = float32(pitchRad * 180 / math.Pi)
					
					// Clamp pitch to reasonable range for natural look
					if targetPitch > 20 {
						targetPitch = 20
					} else if targetPitch < -20 {
						targetPitch = -20
					}
				} else if targetFound && dist <= 0.1 {
					// Only look at player when very close or stopped
					dxH := targetHeadPos.X() - currentPos.X()
					dyH := targetHeadPos.Y() - (currentPos.Y() + 1.62)
					dzH := targetHeadPos.Z() - currentPos.Z()
					distH := float32(math.Sqrt(float64(dxH*dxH + dzH*dzH)))
					if distH > 0.01 {
						yawRad := math.Atan2(float64(dzH), float64(dxH))
						targetYaw = float32(yawRad*180/math.Pi) - 90
						pitchRad := -math.Atan2(float64(dyH), float64(distH))
						targetPitch = float32(pitchRad * 180 / math.Pi)
					}
				}
			} else if mState == "walk_to" {
				// Walk mode: look smoothly towards direction of travel
				if dist > 0.1 {
					yawRad := math.Atan2(float64(dz), float64(dx))
					targetYaw = float32(yawRad*180/math.Pi) - 90
					dyH := nextTarget.Y() - (currentPos.Y() + 1.62)
					distH := float32(math.Sqrt(float64(dx*dx + dz*dz)))
					pitchRad := -math.Atan2(float64(dyH), float64(distH))
					targetPitch = float32(pitchRad * 180 / math.Pi)
				}
			} else {
				// Idle mode: perform organic step-and-pause look-around (completely independent, no player-stare)
				if idleLookState == "moving" {
					targetYaw = idleLookTargetYaw
					targetPitch = idleLookTargetPitch
					idleLookTicksRemaining--
					if idleLookTicksRemaining <= 0 {
						idleLookState = "waiting"
						idleLookWaitTicks = 40 + rand.Intn(80) // 2 to 6 seconds pause
					}
				} else { // waiting
					targetYaw = idleLookTargetYaw
					targetPitch = idleLookTargetPitch
					idleLookWaitTicks--
					if idleLookWaitTicks <= 0 {
						idleLookState = "moving"
						idleLookTicksRemaining = 20 + rand.Intn(20) // 1 to 2 seconds transition

						// Choose a yaw offset up to +/- 60 degrees from current yaw
						yawOffset := (rand.Float32() - 0.5) * 120.0
						idleLookTargetYaw = yaw + yawOffset

						// Pitch target between -15 and +15
						idleLookTargetPitch = (rand.Float32() - 0.5) * 30.0
					}
				}
			}

			// Apply smooth interpolation to eliminate head tremors
			yaw = interpolateAngle(yaw, targetYaw, 15.0)      // max 15 degrees per tick
			pitch = interpolatePitch(pitch, targetPitch, 8.0) // max 8 degrees per tick

			inputData := protocol.NewBitset(packet.PlayerAuthInputBitsetSize)
			inputData.Set(packet.InputFlagVerticalCollision)
			if shouldJump {
				inputData.Set(packet.InputFlagJumping)
			}
			if moveVec.Y() > 0.1 {
				inputData.Set(packet.InputFlagUp)
			} else if moveVec.Y() < -0.1 {
				inputData.Set(packet.InputFlagDown)
			}
			if moveVec.X() > 0.1 {
				inputData.Set(packet.InputFlagRight)
			} else if moveVec.X() < -0.1 {
				inputData.Set(packet.InputFlagLeft)
			}
			if moveVec.Y() > 0.5 {
				inputData.Set(packet.InputFlagSprinting)
			}

			// Custom physical emote updates
			b.mu.Lock()
			if b.emoteTicks > 0 {
				b.emoteTicks--
				switch b.emoteState {
				case "jump":
					inputData.Set(packet.InputFlagJumping)
					if b.emoteTicks > 10 {
						currentPos = mgl32.Vec3{currentPos.X(), currentPos.Y() + 0.15, currentPos.Z()}
					} else {
						currentPos = mgl32.Vec3{currentPos.X(), currentPos.Y() - 0.15, currentPos.Z()}
					}
					b.pos = currentPos
				case "sneak":
					inputData.Set(packet.InputFlagSneaking)
				case "spin":
					yaw = interpolateAngle(yaw, yaw+18, 18)
				case "wiggle":
					if b.emoteTicks%4 < 2 {
						yaw = interpolateAngle(yaw, yaw+15, 15)
					} else {
						yaw = interpolateAngle(yaw, yaw-15, 15)
					}
				case "lookaround":
					if b.emoteTicks%5 == 0 {
						yaw = interpolateAngle(yaw, yaw+float32((tick%50)-25), 25)
						pitch = interpolatePitch(pitch, pitch+float32((tick%30)-15), 15)
					}
				case "nod":
					if b.emoteTicks%8 < 4 {
						pitch = interpolatePitch(pitch, 30, 10)
					} else {
						pitch = interpolatePitch(pitch, -10, 10)
					}
				case "shake":
					if b.emoteTicks%8 < 4 {
						yaw = interpolateAngle(yaw, yaw+20, 20)
					} else {
						yaw = interpolateAngle(yaw, yaw-20, 20)
					}
				}
				if b.emoteTicks == 0 {
					b.emoteState = ""
				}
			}

			// Save look angles back
			b.yaw = yaw
			b.pitch = pitch
			b.mu.Unlock()

			pk := &packet.PlayerAuthInput{
				Position:           currentPos.Add(mgl32.Vec3{0, 1.62, 0}),
				Pitch:              pitch,
				Yaw:                yaw,
				HeadYaw:            yaw,
				MoveVector:         moveVec,
				InputData:          inputData,
				InputMode:          packet.InputModeTouch,
				PlayMode:           packet.PlayModeNormal,
				InteractionModel:   packet.InteractionModelTouch,
				Tick:               tick,
				Delta:              moveDelta,
				AnalogueMoveVector: moveVec,
				RawMoveVector:      moveVec,
			}
			if err := b.conn.WritePacket(pk); err != nil {
				return
			}
			tick++
		}
	}
}

// interpolateAngle smoothly moves current angle towards target by at most maxStep, handling 360 wrap-around
func interpolateAngle(current, target, maxStep float32) float32 {
	diff := target - current
	for diff < -180 {
		diff += 360
	}
	for diff > 180 {
		diff -= 360
	}
	if diff > maxStep {
		diff = maxStep
	} else if diff < -maxStep {
		diff = -maxStep
	}
	res := current + diff
	for res < 0 {
		res += 360
	}
	for res >= 360 {
		res -= 360
	}
	return res
}

// interpolatePitch smoothly moves current pitch towards target by at most maxStep
func interpolatePitch(current, target, maxStep float32) float32 {
	diff := target - current
	if diff > maxStep {
		diff = maxStep
	} else if diff < -maxStep {
		diff = -maxStep
	}
	res := current + diff
	if res > 90 {
		res = 90
	} else if res < -90 {
		res = -90
	}
	return res
}

func (b *Bot) packetLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			b.logger.Info("shutting down", slog.String("reason", ctx.Err().Error()))
			return nil
		default:
		}

		pk, err := b.conn.ReadPacket()
		if err != nil {
			var disc minecraft.DisconnectError
			if errors.As(err, &disc) {
				b.logger.Info("disconnected by server",
					slog.String("reason", disc.Error()),
				)
				b.bus.Publish(event.SpawnEvent{}) // Publish empty event or trigger disconnect
				return nil
			}
			return fmt.Errorf("read packet: %w", err)
		}

		switch p := pk.(type) {
		case *packet.AddPlayer:
			b.mu.Lock()
			b.playerEntityIDs[p.Username] = p.EntityRuntimeID
			b.playerUsernames[p.EntityRuntimeID] = p.Username
			b.playerPositions[p.EntityRuntimeID] = p.Position
			b.playerUUIDs[p.UUID] = p.Username
			b.mu.Unlock()
			b.logger.Info("tracked player spawned", slog.String("username", p.Username), slog.Uint64("runtime_id", p.EntityRuntimeID))

		case *packet.MovePlayer:
			b.mu.Lock()
			isSelf := p.EntityRuntimeID == b.conn.GameData().EntityRuntimeID
			b.logger.Debug("MovePlayer packet received",
				slog.Uint64("runtime_id", p.EntityRuntimeID),
				slog.Uint64("self_runtime_id", b.conn.GameData().EntityRuntimeID),
				slog.Bool("is_self", isSelf),
				slog.Float64("x", float64(p.Position.X())),
				slog.Float64("y", float64(p.Position.Y())),
				slog.Float64("z", float64(p.Position.Z())),
			)
			if isSelf {
				newPos := p.Position.Sub(mgl32.Vec3{0, 1.62, 0})
				if newPos.Y() <= 320 && newPos.Y() >= -64 {
					correctionDx := newPos.X() - b.pos.X()
					correctionDz := newPos.Z() - b.pos.Z()
					correctionDist := math.Sqrt(float64(correctionDx*correctionDx + correctionDz*correctionDz))

					if b.movementState == "idle" || correctionDist > 2.0 {
						b.pos = newPos
					}
				} else {
					b.logger.Warn("Server sent void position, ignoring", "y", newPos.Y())
				}
			} else {
				b.playerPositions[p.EntityRuntimeID] = p.Position
			}
			b.mu.Unlock()

		case *packet.CorrectPlayerMovePrediction:
			b.mu.Lock()
			correctedPos := p.Position.Sub(mgl32.Vec3{0, 1.62, 0})
			if correctedPos.Y() <= 320 && correctedPos.Y() >= -64 {
				b.pos = correctedPos
			}
			b.mu.Unlock()

		case *packet.Respawn:
			b.mu.Lock()
			b.pos = p.Position.Sub(mgl32.Vec3{0, 1.62, 0})
			b.logger.Info("bot respawned/teleported by server",
				slog.Float64("x", float64(b.pos.X())),
				slog.Float64("y", float64(b.pos.Y())),
				slog.Float64("z", float64(b.pos.Z())),
			)
			b.mu.Unlock()

		case *packet.PlayerList:
			if p.ActionType == packet.PlayerListActionAdd {
				for _, entry := range p.Entries {
					b.mu.Lock()
					b.playerUUIDs[entry.UUID] = entry.Username
					if entry.Username == b.name {
						b.playerUUID = entry.UUID
						b.logger.Info("server PlayerList entry for bot",
							slog.String("username", entry.Username),
							slog.String("uuid", entry.UUID.String()),
							slog.Int64("entity_unique_id", entry.EntityUniqueID),
						)
					}
					b.mu.Unlock()
				}
			} else if p.ActionType == packet.PlayerListActionRemove {
				for _, entry := range p.Entries {
					b.mu.Lock()
					if username, ok := b.playerUUIDs[entry.UUID]; ok {
						if id, hasID := b.playerEntityIDs[username]; hasID {
							delete(b.playerUsernames, id)
							delete(b.playerPositions, id)
						}
						delete(b.playerEntityIDs, username)
						delete(b.playerUUIDs, entry.UUID)
						b.logger.Info("tracked player disconnected", slog.String("username", username))
					}
					b.mu.Unlock()
				}
			}

		case *packet.AddActor:
			b.mu.Lock()
			b.actors[p.EntityRuntimeID] = &entity.Info{
				ID:       p.EntityRuntimeID,
				Type:     p.EntityType,
				Name:     p.EntityType,
				Position: p.Position,
				Health:   20, // default
			}
			b.uniqueIDToRuntimeID[p.EntityUniqueID] = p.EntityRuntimeID
			b.mu.Unlock()
			b.logger.Info("tracked actor spawned", slog.String("type", p.EntityType), slog.Uint64("runtime_id", p.EntityRuntimeID))

		case *packet.CraftingData:
			b.mu.Lock()
			b.recipes = make(map[string]uint32)
			for _, r := range p.Recipes {
				switch recipe := r.(type) {
				case *protocol.ShapelessRecipe:
					if len(recipe.Output) > 0 {
						outItem := recipe.Output[0]
						name := b.itemNames[outItem.NetworkID]
						if name != "" {
							b.recipes[strings.ToLower(name)] = recipe.RecipeNetworkID
							cleanName := strings.TrimPrefix(name, "minecraft:")
							b.recipes[strings.ToLower(cleanName)] = recipe.RecipeNetworkID
						}
					}
				case *protocol.ShapedRecipe:
					if len(recipe.Output) > 0 {
						outItem := recipe.Output[0]
						name := b.itemNames[outItem.NetworkID]
						if name != "" {
							b.recipes[strings.ToLower(name)] = recipe.RecipeNetworkID
							cleanName := strings.TrimPrefix(name, "minecraft:")
							b.recipes[strings.ToLower(cleanName)] = recipe.RecipeNetworkID
						}
					}
				}
			}
			b.mu.Unlock()
			b.logger.Info("Crafting recipes cached", "count", len(b.recipes))

		case *packet.MoveActorDelta:
			b.mu.Lock()
			if act, ok := b.actors[p.EntityRuntimeID]; ok {
				act.Position = p.Position
			}
			b.mu.Unlock()

		case *packet.RemoveActor:
			b.mu.Lock()
			if runtimeID, ok := b.uniqueIDToRuntimeID[p.EntityUniqueID]; ok {
				delete(b.actors, runtimeID)
				delete(b.uniqueIDToRuntimeID, p.EntityUniqueID)
			}
			id := uint64(p.EntityUniqueID)
			if username, ok := b.playerUsernames[id]; ok {
				delete(b.playerEntityIDs, username)
				delete(b.playerUsernames, id)
				delete(b.playerPositions, id)
				b.logger.Info("tracked player left view distance", slog.String("username", username))
			}
			b.mu.Unlock()

		case *packet.InventoryContent:
			if p.WindowID == 0 {
				b.mu.Lock()
				b.inventoryMap = make(map[uint32]protocol.ItemStack)
				for slot, item := range p.Content {
					if item.Stack.Count > 0 && item.Stack.NetworkID != 0 {
						b.inventoryMap[uint32(slot)] = item.Stack
					}
				}
				b.mu.Unlock()
			}

		case *packet.InventorySlot:
			if p.WindowID == 0 {
				b.mu.Lock()
				if p.NewItem.Stack.Count > 0 && p.NewItem.Stack.NetworkID != 0 {
					b.inventoryMap[p.Slot] = p.NewItem.Stack
				} else {
					delete(b.inventoryMap, p.Slot)
				}
				b.mu.Unlock()
			}

		case *packet.MobEquipment:
			if p.EntityRuntimeID == b.conn.GameData().EntityRuntimeID {
				b.mu.Lock()
				b.heldSlot = uint32(p.HotBarSlot)
				b.mu.Unlock()
			}

		case *packet.UpdateAttributes:
			if p.EntityRuntimeID == b.conn.GameData().EntityRuntimeID {
				b.mu.Lock()
				prevHealth := b.health
				for _, attr := range p.Attributes {
					if attr.Name == "minecraft:health" {
						b.health = int(attr.Value)
					} else if attr.Name == "minecraft:player.hunger" {
						b.hunger = int(attr.Value)
					}
				}

				// Self-learning hazard detection: if health decreases, mark the block below as hazard
				if b.health < prevHealth && b.health > 0 {
					feetX := int32(math.Floor(float64(b.pos.X())))
					feetY := int32(math.Floor(float64(b.pos.Y())))
					feetZ := int32(math.Floor(float64(b.pos.Z())))

					b.worldModel.SetHazard(feetX, feetY-1, feetZ, true)
					b.logger.Warn("bot took damage! marking block below feet as hazard", "x", feetX, "y", feetY-1, "z", feetZ)

					b.mu.Unlock()
					b.RecalculatePath()
					b.mu.Lock()
				}
				b.mu.Unlock()
			}

		case *packet.UpdateBlock:
			// Treat block as solid if runtime ID is not 0 (air/water/empty)
			isSolid := p.NewBlockRuntimeID != 0
			b.worldModel.SetSolid(p.Position.X(), p.Position.Y(), p.Position.Z(), isSolid)
			b.logger.Debug("Block updated", "x", p.Position.X(), "y", p.Position.Y(), "z", p.Position.Z(), "isSolid", isSolid, "runtimeID", p.NewBlockRuntimeID)
		}

		if handleErr := b.registry.Handle(ctx, pk); handleErr != nil {
			b.logger.Error("handle packet",
				slog.String("error", handleErr.Error()),
			)
		}
	}
}

// FindPlayer returns the runtime ID and current position of a player by username (case-insensitive)
func (b *Bot) FindPlayer(username string) (uint64, mgl32.Vec3, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for name, id := range b.playerEntityIDs {
		if strings.EqualFold(name, username) {
			if pos, ok := b.playerPositions[id]; ok {
				return id, pos, true
			}
		}
	}
	return 0, mgl32.Vec3{}, false
}

// RecalculatePath computes the shortest path to targetPos using A* search.
func (b *Bot) RecalculatePath() {
	b.mu.Lock()
	defer b.mu.Unlock()

	start := pathfinder.Node{
		X: int32(math.Floor(float64(b.pos.X()))),
		Y: int32(math.Floor(float64(b.pos.Y()))),
		Z: int32(math.Floor(float64(b.pos.Z()))),
	}
	targetY := b.targetPos.Y()
	if b.movementState == "follow" {
		targetY -= 1.62
	}
	target := pathfinder.Node{
		X: int32(math.Floor(float64(b.targetPos.X()))),
		Y: int32(math.Floor(float64(targetY))),
		Z: int32(math.Floor(float64(b.targetPos.Z()))),
	}

	b.logger.Debug("recalculating path using A*",
		"start_x", start.X, "start_y", start.Y, "start_z", start.Z,
		"target_x", target.X, "target_y", target.Y, "target_z", target.Z,
		"movement_state", b.movementState,
	)

	path := pathfinder.FindPath(start, target, b.worldModel)
	if len(path) > 0 {
		b.currentPath = path
		b.pathIndex = 0
		b.ticksStuck = 0
		b.lastTickPos = b.pos
		b.lastPathRecalcTime = time.Now()
		b.consecutiveStuckCount = 0
		b.logger.Info("A* pathfinding completed", "nodes", len(path), "movement_state", b.movementState)
	} else {
		b.currentPath = nil
		b.lastPathRecalcTime = time.Now()
		b.logger.Warn("A* pathfinding failed to resolve walkable path to destination",
			"start", start, "target", target, "movement_state", b.movementState)
	}
}

func (b *Bot) Close() error {
	if b.conn != nil {
		return b.conn.Close()
	}
	return nil
}

func (b *Bot) GetEntities() map[uint64]*entity.Info {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.actors
}

func (b *Bot) GetHeldItemSlot() uint32 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.heldSlot
}

func (b *Bot) GetInventorySlots() map[uint32]protocol.ItemStack {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inventoryMap
}

func (b *Bot) GetItemNames() map[int32]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.itemNames
}

func (b *Bot) SendChat(msg string) {
	b.SendSafeChat(msg)
}

func (b *Bot) GetEntityRuntimeID() uint64 {
	return b.conn.GameData().EntityRuntimeID
}

func (b *Bot) GetLocalWorldModel() entity.WorldModel {
	return b.worldModel
}

func (b *Bot) NavigateTo(pos mgl32.Vec3) {
	b.WalkTo(pos)
}

func (b *Bot) StopMovement() {
	b.Stop()
}

func (b *Bot) NavigateToBlock(x, y, z int32, tolerance float32) bool {
	target := mgl32.Vec3{float32(x) + 0.5, float32(y), float32(z) + 0.5}
	b.WalkTo(target)

	// Wait up to 5 seconds for bot to reach coordinate
	for i := 0; i < 25; i++ {
		time.Sleep(200 * time.Millisecond)
		b.mu.Lock()
		curPos := b.pos
		mState := b.movementState
		b.mu.Unlock()

		dx := curPos.X() - target.X()
		dy := curPos.Y() - target.Y()
		dz := curPos.Z() - target.Z()
		dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
		if dist <= tolerance {
			return true
		}
		if mState == "idle" {
			break
		}
	}
	return false
}

func (b *Bot) WritePacket(pk packet.Packet) error {
	return b.conn.WritePacket(pk)
}

func (b *Bot) EquipItem(slot uint32) error {
	b.mu.Lock()
	b.heldSlot = slot
	item := b.inventoryMap[slot]
	b.mu.Unlock()

	pk := &packet.MobEquipment{
		EntityRuntimeID: b.conn.GameData().EntityRuntimeID,
		NewItem:         protocol.ItemInstance{Stack: item},
		InventorySlot:   byte(slot),
		HotBarSlot:      byte(slot),
		WindowID:        0,
	}
	return b.conn.WritePacket(pk)
}

func (b *Bot) UnequipItem() error {
	pk := &packet.MobEquipment{
		EntityRuntimeID: b.conn.GameData().EntityRuntimeID,
		NewItem:         protocol.ItemInstance{},
		InventorySlot:   0,
		HotBarSlot:      0,
		WindowID:        0,
	}
	return b.conn.WritePacket(pk)
}

func (b *Bot) LookAt(pos mgl32.Vec3) {
	b.mu.Lock()
	defer b.mu.Unlock()

	dx := pos.X() - b.pos.X()
	dy := pos.Y() - b.pos.Y()
	dz := pos.Z() - b.pos.Z()

	distH := math.Sqrt(float64(dx*dx + dz*dz))
	if distH < 0.001 {
		distH = 0.001
	}

	yawRad := math.Atan2(float64(dz), float64(dx))
	yawDeg := yawRad * 180 / math.Pi
	b.yaw = float32(yawDeg) - 90
	if b.yaw < 0 {
		b.yaw += 360
	}

	pitchRad := -math.Atan2(float64(dy), distH)
	b.pitch = float32(pitchRad * 180 / math.Pi)
}

func (b *Bot) DropItem(name string, count int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	var targetSlot uint32
	var foundItem protocol.ItemStack
	found := false

	// Find the item by name
	for slot, item := range b.inventoryMap {
		if item.Count <= 0 {
			continue
		}
		itemName := b.itemNames[item.NetworkID]
		if strings.Contains(strings.ToLower(itemName), strings.ToLower(name)) {
			targetSlot = slot
			foundItem = item
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("item %s not found in inventory", name)
	}

	if count <= 0 || count > int(foundItem.Count) {
		count = int(foundItem.Count)
	}

	// Create dropped item transaction
	dropItem := foundItem
	dropItem.Count = uint16(count)

	tx := &packet.InventoryTransaction{
		Actions: []protocol.InventoryAction{
			{
				SourceType:    protocol.InventoryActionSourceContainer,
				WindowID:      0,
				InventorySlot: targetSlot,
				OldItem:       protocol.ItemInstance{Stack: foundItem},
				NewItem: protocol.ItemInstance{
					Stack: protocol.ItemStack{
						ItemType:       foundItem.ItemType,
						BlockRuntimeID: foundItem.BlockRuntimeID,
						Count:          foundItem.Count - uint16(count),
						NBTData:        foundItem.NBTData,
						CanBePlacedOn:  foundItem.CanBePlacedOn,
						CanBreak:       foundItem.CanBreak,
						HasNetworkID:   foundItem.HasNetworkID,
					},
				},
			},
			{
				SourceType:    protocol.InventoryActionSourceWorld,
				SourceFlags:   1, // Drop item flag
				InventorySlot: 0,
				OldItem:       protocol.ItemInstance{},
				NewItem:       protocol.ItemInstance{Stack: dropItem},
			},
		},
		TransactionData: &protocol.NormalTransactionData{},
	}

	return b.conn.WritePacket(tx)
}

func (b *Bot) InjectAIEvent(msg string) {
	b.logger.Info("AI Event injected", "msg", msg)
	if b.AiClient == nil {
		return
	}

	// Query Nvidia Client asynchronously with the system message
	go func() {
		hp, hunger, botCoords := b.GetStatusDetails()
		heldItem := b.GetHeldItem()
		invSummary := b.GetInventorySummary()

		b.mu.Lock()
		mainPlayer := b.AiCfg.MainPlayer
		b.mu.Unlock()

		if mainPlayer == "" {
			return
		}

		playerCoordsStr := ""
		if pCoords, ok := b.GetPlayerCoords(mainPlayer); ok {
			playerCoordsStr = fmt.Sprintf("X:%.0f Y:%.0f Z:%.0f", pCoords.X(), pCoords.Y(), pCoords.Z())
		}

		botStatusText := fmt.Sprintf("HP: %d/20, Hunger: %d/20", hp, hunger)
		systemPrompt := b.AiClient.BuildSystemPrompt(
			b.name,
			botCoords+" ("+botStatusText+")",
			playerCoordsStr,
			heldItem,
			invSummary,
		)

		reply, err := b.AiClient.Ask(mainPlayer, systemPrompt, msg)
		if err != nil {
			b.logger.Error("Failed to ask Nvidia LLM for injected event", "error", err.Error())
			return
		}

		parsed := ai.Parse(reply)
		if parsed.CleanReply != "" {
			b.SendSafeChat(parsed.CleanReply)
		}

		for _, act := range parsed.Actions {
			b.ExecuteAction(act.Label, act.Param, mainPlayer)
		}
	}()
}

func (b *Bot) CraftItem(recipeNetID uint32, count int) error {
	req := protocol.ItemStackRequest{
		RequestID: 1, // standard ID
		Actions: []protocol.StackRequestAction{
			&protocol.AutoCraftRecipeStackRequestAction{
				RecipeNetworkID: recipeNetID,
				TimesCrafted:    byte(count),
				NumberOfCrafts:  byte(count),
			},
		},
	}
	pk := &packet.ItemStackRequest{
		Requests: []protocol.ItemStackRequest{req},
	}
	return b.conn.WritePacket(pk)
}

func (b *Bot) GetRecipes() map[string]uint32 {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Create a shallow copy to be thread-safe
	copyMap := make(map[string]uint32, len(b.recipes))
	for k, v := range b.recipes {
		copyMap[k] = v
	}
	return copyMap
}
