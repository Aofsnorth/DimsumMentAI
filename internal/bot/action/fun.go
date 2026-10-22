package action

import (
	"fmt"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/rand"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func DurationTicks(param string, fallback time.Duration) int {
	seconds := int(fallback / time.Second)
	if param != "" {
		_, _ = fmt.Sscanf(strings.Split(param, ",")[0], "%d", &seconds)
	}
	if seconds < 1 {
		seconds = 1
	}
	if seconds > 60 {
		seconds = 60
	}
	return seconds * 20
}

func ParseCount(param string, fallback int) int {
	if param == "" {
		return fallback
	}
	_, _ = fmt.Sscanf(strings.Split(param, ",")[0], "%d", &fallback)
	if fallback < 1 {
		return 1
	}
	return fallback
}

// runEmoteAnimation performs multi-tick or composite emote animations (wave,
// dab, multi-jump, dance combos) after halting any active follow/walk state so
// look and crouch emotes are not suppressed by pathfinding.
func runEmoteAnimation(b *bot.Bot, label, emoteName, param string, ticks int, lookOffset mgl32.Vec3) {
	b.HaltWalk()
	if lookOffset != (mgl32.Vec3{}) {
		b.LookAt(b.GetCoords().Add(lookOffset))
	}

	switch label {
	case "wave", "dab":
		b.TriggerEmoteFor("nod", ticks)
		swingArmBursts(b, 4, 180*time.Millisecond)
		return
	case "jumpinplace":
		count := ParseCount(param, 5)
		if count > 25 {
			count = 25
		}
		b.RequestJump()
		b.TriggerEmoteFor("jump", 14)
		runRepeatedJumps(b, count-1, 600*time.Millisecond)
		return
	case "jumpforever":
		b.RequestJump()
		b.TriggerEmoteFor("jump", ticks)
		steps := ticks / 12
		if steps > 1 {
			runRepeatedJumps(b, steps-1, 600*time.Millisecond)
		}
		return
	case "dance", "floss", "naenae", "robot", "breakdance", "throwparty", "explode", "jumpspincombo":
		b.TriggerEmoteFor("spin", ticks)
		runDanceCombo(b, ticks)
		return
	default:
		if emoteName == "jump" {
			b.RequestJump()
		}
		b.TriggerEmoteFor(emoteName, ticks)
	}
}

func swingArmBursts(b *bot.Bot, swings int, interval time.Duration) {
	ctx := b.ActivityContext()
	go func() {
		for i := 0; i < swings; i++ {
			if ctx != nil && ctx.Err() != nil {
				return
			}
			_ = b.WritePacket(&packet.Animate{
				ActionType:      packet.AnimateActionSwingArm,
				EntityRuntimeID: b.GetEntityRuntimeID(),
			})
			time.Sleep(interval)
		}
	}()
}

func runRepeatedJumps(b *bot.Bot, remaining int, interval time.Duration) {
	if remaining <= 0 {
		return
	}
	ctx := b.ActivityContext()
	go func() {
		for i := 0; i < remaining; i++ {
			time.Sleep(interval)
			if ctx != nil && ctx.Err() != nil {
				return
			}
			b.RequestJump()
			b.TriggerEmoteFor("jump", 14)
		}
	}()
}

func runDanceCombo(b *bot.Bot, ticks int) {
	ctx := b.ActivityContext()
	duration := time.Duration(ticks*50) * time.Millisecond
	go func() {
		deadline := time.Now().Add(duration)
		step := 0
		for time.Now().Before(deadline) {
			if ctx != nil && ctx.Err() != nil {
				return
			}
			if step%2 == 0 {
				b.RequestJump()
			}
			_ = b.WritePacket(&packet.Animate{
				ActionType:      packet.AnimateActionSwingArm,
				EntityRuntimeID: b.GetEntityRuntimeID(),
			})
			step++
			time.Sleep(450 * time.Millisecond)
		}
	}()
}

// runMovementPattern walks the requested pattern until its duration is up or
// the activity context is cancelled.
func runMovementPattern(b *bot.Bot, label, param, user string) {
	duration := time.Duration(DurationTicks(param, 5*time.Second)/20) * time.Second
	if duration > 30*time.Second {
		duration = 30 * time.Second
	}
	ctx := b.ActivityContext()
	deadline := time.Now().Add(duration)
	origin := b.GetCoords()
	baseYawRad := float64(b.GetYaw()+90) * math.Pi / 180.0
	step := 0

	if label == "bunnyhop" || label == "panic" || label == "jumpforward" {
		b.SetSprintHint(true)
		defer b.ClearSprintHint()
	}

	for time.Now().Before(deadline) {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		stepMovementPattern(b, label, user, origin, baseYawRad, step)
		step++
		if !sleepPatternTick(ctx, label) {
			break
		}
	}
	b.Stop()
}

func stepMovementPattern(b *bot.Bot, label, user string, origin mgl32.Vec3, baseYawRad float64, step int) {
	switch label {
	case "chase":
		if _, p, ok := b.FindPlayer(user); ok {
			b.WalkTo(p)
		}
	case "runaway", "flee":
		stepRunAwayFromPlayer(b, user)
	case "walkbackforth", "swimbackforth":
		dir := float32(1)
		if step%2 == 1 {
			dir = -1
		}
		b.WalkTo(origin.Add(mgl32.Vec3{float32(math.Cos(baseYawRad)) * 4 * dir, 0, float32(math.Sin(baseYawRad)) * 4 * dir}))
	case "walkcircle", "spiral":
		angle := float64(step) * (math.Pi / 3)
		radius := float32(3.5)
		if label == "spiral" {
			radius = float32(2 + (step % 6))
		}
		b.WalkTo(origin.Add(mgl32.Vec3{float32(math.Cos(angle)) * radius, 0, float32(math.Sin(angle)) * radius}))
	case "walksquare":
		corners := [4]mgl32.Vec3{{4, 0, 0}, {4, 0, 4}, {0, 0, 4}, {0, 0, 0}}
		b.WalkTo(origin.Add(corners[step%4]))
	case "moonwalk":
		pos := b.GetCoords()
		b.WalkTo(pos.Add(mgl32.Vec3{-float32(math.Cos(baseYawRad)) * 3, 0, -float32(math.Sin(baseYawRad)) * 3}))
	case "crabwalk", "zigzag":
		side := float32(1)
		if step%2 == 1 {
			side = -1
		}
		perpAngle := baseYawRad + (math.Pi/2)*float64(side)
		fwd := float32(0)
		if label == "zigzag" {
			fwd = 2.5
		}
		pos := b.GetCoords()
		b.WalkTo(pos.Add(mgl32.Vec3{
			float32(math.Cos(baseYawRad))*fwd + float32(math.Cos(perpAngle))*3,
			0,
			float32(math.Sin(baseYawRad))*fwd + float32(math.Sin(perpAngle))*3,
		}))
	case "jumpforward", "bunnyhop":
		b.RequestJump()
		pos := b.GetCoords()
		b.WalkTo(pos.Add(mgl32.Vec3{float32(math.Cos(baseYawRad)) * 5, 0, float32(math.Sin(baseYawRad)) * 5}))
	default:
		if label == "panic" {
			b.RequestJump()
		}
		pos := b.GetCoords()
		angle := rand.Float64() * math.Pi * 2
		dist := float32(2 + rand.Intn(4))
		b.WalkTo(pos.Add(mgl32.Vec3{float32(math.Cos(angle)) * dist, 0, float32(math.Sin(angle)) * dist}))
	}
}

func sleepPatternTick(ctx interface{ Done() <-chan struct{} }, label string) bool {
	wait := 1200 * time.Millisecond
	if label == "panic" || label == "bunnyhop" || label == "jumpforward" || label == "walkcircle" {
		wait = 650 * time.Millisecond
	}
	if ctx == nil {
		time.Sleep(wait)
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(wait):
		return true
	}
}

func stepRunAwayFromPlayer(b *bot.Bot, user string) {
	_, p, ok := b.FindPlayer(user)
	if !ok {
		return
	}
	pos := b.GetCoords()
	dir := pos.Sub(p)
	if dir.Len() < 0.1 {
		dir = mgl32.Vec3{1, 0, 0}
	}
	dir = dir.Normalize().Mul(8)
	b.WalkTo(pos.Add(dir))
}

func runAwayFromPlayer(b *bot.Bot, user string, duration time.Duration) {
	stepRunAwayFromPlayer(b, user)
	time.Sleep(duration)
}

func handleLookOrIdleAction(b *bot.Bot, label, param, user string) {
	b.HaltWalk()
	switch label {
	case "stare":
		seconds := time.Duration(DurationTicks(param, 5*time.Second)/20) * time.Second
		if !b.LookAtPlayer(user, seconds) {
			b.TriggerEmoteFor("lookaround", int(seconds/time.Second)*20)
		}
	case "lookcrazy":
		b.TriggerEmoteFor("lookaround", DurationTicks(param, 5*time.Second))
	case "freeze":
		b.Stop()
	case "vibrate":
		b.TriggerEmoteFor("wiggle", DurationTicks(param, 3*time.Second))
	}
}

func digDownAction(b *bot.Bot, label, param, user string) {
	depth := ParseCount(param, 3)
	if label == "gotohell" {
		depth = 50
	}
	if label == "digout" {
		b.HaltWalk()
		b.RequestJump()
		b.TriggerEmoteFor("jump", 60)
		ReportStatus(b, user, event.ActionStatus{Action: label, Success: true})
		return
	}
	if b.Gatherer == nil {
		ReportStatus(b, user, event.ActionStatus{
			Action:  label,
			Success: false,
			Error:   "pengumpul blok belum siap",
		})
		return
	}
	b.Gatherer.DigDown(b.ActivityContext(), depth)
	ReportStatus(b, user, event.ActionStatus{Action: label, Count: depth, Success: true})
}

func towerAction(b *bot.Bot, param, user string) {
	height := ParseCount(param, 6)
	if height > 50 {
		height = 50
	}
	if b.Gatherer == nil {
		ReportStatus(b, user, event.ActionStatus{
			Action:  "buildtower",
			Success: false,
			Error:   "pengumpul blok belum siap",
		})
		return
	}
	b.Gatherer.TowerUp(b.ActivityContext(), height)
	ReportStatus(b, user, event.ActionStatus{Action: "buildtower", Count: height, Success: true})
}
