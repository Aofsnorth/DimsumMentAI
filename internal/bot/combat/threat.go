// Package combat handles threat detection and combat logic.
package combat

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"bedrock-ai/internal/bot/entity"

	"github.com/go-gl/mathgl/mgl32"
)

var hostileMobs = map[string]bool{
	"zombie":          true,
	"skeleton":        true,
	"creeper":         true,
	"spider":          true,
	"cave_spider":     true,
	"enderman":        true,
	"witch":           true,
	"slime":           true,
	"magma_cube":      true,
	"phantom":         true,
	"drowned":         true,
	"husk":            true,
	"stray":           true,
	"pillager":        true,
	"vindicator":      true,
	"evoker":          true,
	"ravager":         true,
	"blaze":           true,
	"ghast":           true,
	"wither_skeleton": true,
	"piglin_brute":    true,
	"warden":          true,
	"vex":             true,
	"guardian":        true,
	"elder_guardian":  true,
	"silverfish":      true,
	"endermite":       true,
	"hoglin":          true,
	"zoglin":          true,
	"breeze":          true,
	"bogged":          true,
}

type ThreatDetector struct {
	bot            Bot
	combat         *CombatManager
	logger         *slog.Logger
	alertCooldowns map[uint64]time.Time
	mu             sync.Mutex
}

func NewThreatDetector(bot Bot, cm *CombatManager, logger *slog.Logger) *ThreatDetector {
	return &ThreatDetector{
		bot:            bot,
		combat:         cm,
		logger:         logger,
		alertCooldowns: make(map[uint64]time.Time),
	}
}

func (td *ThreatDetector) Scan(ctx context.Context) {
	if td.combat.InCombat() {
		return
	}

	botPos := td.bot.GetCoords()
	entities := td.bot.GetEntities()
	now := time.Now()

	td.cleanupCooldowns(now)

	visible := entity.VisibleMobs(td.bot.GetLocalWorldModel(), td.bot, botPos, entities, 16, nil)
	closestHostile, closestDist, newThreats := td.evaluateThreats(botPos, visible, now)
	if closestHostile == nil {
		return
	}

	td.handleThreat(closestHostile, closestDist, newThreats, botPos)
}

func (td *ThreatDetector) cleanupCooldowns(now time.Time) {
	td.mu.Lock()
	for id, t := range td.alertCooldowns {
		if now.Sub(t) > 30*time.Second {
			delete(td.alertCooldowns, id)
		}
	}
	td.mu.Unlock()
}

func (td *ThreatDetector) evaluateThreats(botPos mgl32.Vec3, entities []*entity.Info, now time.Time) (*entity.Info, float32, []*entity.Info) {
	var closestHostile *entity.Info
	closestDist := float32(math.MaxFloat32)
	var newThreats []*entity.Info

	for _, ent := range entities {
		if !isHostileEntity(ent) {
			continue
		}

		dist := td.distance(botPos, ent.Position)

		if dist < closestDist {
			closestDist = dist
			closestHostile = ent
		}

		td.mu.Lock()
		lastAlert, onCooldown := td.alertCooldowns[ent.ID]
		if !onCooldown || now.Sub(lastAlert) > 2*time.Minute {
			newThreats = append(newThreats, ent)
			td.alertCooldowns[ent.ID] = now
		}
		td.mu.Unlock()
	}

	return closestHostile, closestDist, newThreats
}

func isHostileEntity(ent *entity.Info) bool {
	nameLower := strings.ToLower(ent.Name)
	typeLower := strings.ToLower(ent.Type)
	return hostileMobs[nameLower] || hostileMobs[typeLower]
}

func (td *ThreatDetector) handleThreat(closestHostile *entity.Info, closestDist float32, newThreats []*entity.Info, botPos mgl32.Vec3) {
	// Reflex Self-Defense: auto-engage if hostile is very close (within 4 blocks)
	if closestDist <= 4.0 {
		td.logger.Info("ThreatDetector: self-defense reflex engage", "name", closestHostile.Name, "dist", closestDist)
		td.combat.EngageTarget(closestHostile.ID)
		return
	}

	// Alert AI and player of nearby threats if close (< 12 blocks) and not on cooldown
	if len(newThreats) > 0 && closestDist <= 12.0 {
		threatsStr := td.formatThreats(botPos, newThreats)
		td.logger.Info("ThreatDetector: alerting player/AI of threats", "threats", threatsStr)

		systemMsg := fmt.Sprintf("[SYSTEM EVENT: Hostile mobs detected nearby: %s. Decide: fight them or warn the player. Use \\attack\\ if you want to fight.]", threatsStr)
		td.bot.InjectAIEvent(systemMsg)
	}
}

func (td *ThreatDetector) formatThreats(botPos mgl32.Vec3, threats []*entity.Info) string {
	list := make([]string, 0, len(threats))
	for _, t := range threats {
		list = append(list, fmt.Sprintf("%s (dist: %.0f block)", t.Name, td.distance(botPos, t.Position)))
	}
	return strings.Join(list, ", ")
}

func (td *ThreatDetector) distance(a, b mgl32.Vec3) float32 {
	dx := a.X() - b.X()
	dy := a.Y() - b.Y()
	dz := a.Z() - b.Z()
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}
