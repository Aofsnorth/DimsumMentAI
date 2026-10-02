package action

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/rand"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
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

// runMovementPattern walks the requested pattern until its duration is up. It
// stops the bot when it finishes, which is what the caller reports.
func runMovementPattern(b *bot.Bot, label, param, user string) {
	duration := time.Duration(DurationTicks(param, 5*time.Second)/20) * time.Second
	if duration > 30*time.Second {
		duration = 30 * time.Second
	}
	deadline := time.Now().Add(duration)

	for time.Now().Before(deadline) {
		pos := b.GetCoords()
		switch label {
		case "chase":
			if _, p, ok := b.FindPlayer(user); ok {
				b.WalkTo(p)
			}
		case "runaway", "flee":
			runAwayFromPlayer(b, user, 2*time.Second)
		default:
			angle := rand.Float64() * math.Pi * 2
			dist := float32(2 + rand.Intn(4))
			target := pos.Add(mgl32.Vec3{
				float32(math.Cos(angle)) * dist,
				0,
				float32(math.Sin(angle)) * dist,
			})
			b.WalkTo(target)
		}
		time.Sleep(1500 * time.Millisecond)
	}
	b.Stop()
}

func runAwayFromPlayer(b *bot.Bot, user string, duration time.Duration) {
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
	time.Sleep(duration)
}

func handleLookOrIdleAction(b *bot.Bot, label, param, user string) {
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

// digDownAction digs a shaft (or handles the "dig out" variant, which is
// really an emote) and then reports, so a plan step waiting on it is released
// when the digging stops rather than timing out.
func digDownAction(b *bot.Bot, label, param, user string) {
	depth := ParseCount(param, 3)
	if label == "gotohell" {
		depth = 50
	}
	if label == "digout" {
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
	b.Gatherer.DigDown(context.Background(), depth)
	ReportStatus(b, user, event.ActionStatus{Action: label, Count: depth, Success: true})
}

// towerAction scaffolds a tower and then reports, for the same reason.
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
	b.Gatherer.TowerUp(context.Background(), height)
	ReportStatus(b, user, event.ActionStatus{Action: "buildtower", Count: height, Success: true})
}
