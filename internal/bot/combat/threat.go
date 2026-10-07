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
	// guardMode is the in-game guardian toggle, off by default. With it off a
	// distant hostile only produces an AI alert and the reflex engages at 4
	// blocks; with it on the detector hunts for the mob that most endangers the
	// protected player and engages it out to guardEngageRadius — the difference
	// between "didiemin" and "bunuh aja ya".
	guardMode      bool
	guardProtector string
	// lastRetaliate gates the player-hit self-defence engage to one per window.
	lastRetaliate time.Time
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

	if td.guardScan(visible, botPos) {
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

// isHostileEntity decides whether an actor is one of the mobs the detector
// reacts to.
//
// The lookup normalises before comparing. The actor's Type is always
// namespaced — "minecraft:zombie" — and an unnamed mob's Name is a copy of
// that same Type (handleAddActor falls back to it), so the raw strings never
// matched the bare keys in hostileMobs and the whole threat path was dead
// code for every mob without a nametag: no reflex engage, no alerts, which is
// the "mob jahat diemin terus" the guard feature had to fight through.
func isHostileEntity(ent *entity.Info) bool {
	return hostileMobs[entity.NormalizeName(ent.Name)] || hostileMobs[entity.NormalizeName(ent.Type)]
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

// damageRetaliateCooldown keeps one painful second from re-triggering the
// self-defence engage over and over while the health attribute catches up.
const damageRetaliateCooldown = 5 * time.Second

// nearestTrackedPlayerReader is the optional view of the bot that can look up
// tracked players directly. Players are not actors (handleAddPlayer never
// touches b.Actors), so the reflex needs this read and cannot fall back to
// GetEntities — a bot without it just never retaliates, which is the safe
// default.
type nearestTrackedPlayerReader interface {
	NearestTrackedPlayer(origin mgl32.Vec3, maxDist float32) (name string, id uint64, pos mgl32.Vec3, ok bool)
}

// OnDamaged is the self-defence reflex for player hits. A mob that walks up
// is the scan's problem; a player who just swung is a duel that already
// started, so the bot engages the nearest tracked player instead of standing
// there being farmed.
//
// Fair-play shape: only players the bot can actually see, only inside melee
// conversation range, one engage per cooldown window. Damage with no player
// nearby (falls, mobs, poison) is left to the existing paths — hitting back
// at nobody is how a bot looks like it is cheating.
func (td *ThreatDetector) OnDamaged(botPos mgl32.Vec3) {
	td.mu.Lock()
	if time.Since(td.lastRetaliate) < damageRetaliateCooldown {
		td.mu.Unlock()
		return
	}
	td.lastRetaliate = time.Now()
	td.mu.Unlock()

	reader, ok := td.bot.(nearestTrackedPlayerReader)
	if !ok {
		return
	}
	attacker, _, _, found := reader.NearestTrackedPlayer(botPos, 4.5)
	if !found {
		return
	}

	td.logger.Info("ThreatDetector: retaliating against player hit", "player", attacker)
	td.combat.EngagePlayer(attacker)
	td.bot.InjectAIEvent("[SYSTEM EVENT: You were just hit by " + attacker + " and are now fighting back. Tell the player naturally in chat, briefly.]")
}

// guardEngageRadius is how far from the protected player (or the bot) a
// hostile may sit and still be engaged on sight in guard mode. Beyond it the
// behaviour falls back to the stock alert, which is the off-mode behaviour.
const guardEngageRadius = 12.0

// SetGuardMode turns the in-game guardian behaviour on or off. The protector is
// the player who asked, and the hostile picked is the one closest to them.
func (td *ThreatDetector) SetGuardMode(enabled bool, protector string) {
	td.mu.Lock()
	defer td.mu.Unlock()
	td.guardMode = enabled
	td.guardProtector = protector
}

// GuardMode reports the current guardian toggle and who it protects.
func (td *ThreatDetector) GuardMode() (bool, string) {
	td.mu.Lock()
	defer td.mu.Unlock()
	return td.guardMode, td.guardProtector
}

// guardScan is the guard-mode half of the scan. It engages the hostile with the
// smallest distance to either the protected player or the bot, whichever is
// nearer, inside guardEngageRadius — so a zombie drifting toward the player is
// attacked while it is still their problem, not only when it reaches the bot.
//
// It reports whether it engaged, and the caller then skips the stock alert
// path: a fight that already started needs no "decide whether to fight" event.
// When the protector is not currently tracked (logged off, out of range) the
// anchor degrades to the bot itself, which is exactly the off-mode reflex
// widened to the guard radius.
func (td *ThreatDetector) guardScan(visible []*entity.Info, botPos mgl32.Vec3) bool {
	enabled, protector := td.GuardMode()
	if !enabled {
		return false
	}

	anchor := botPos
	anchorName := "bot"
	if _, pos, ok := td.bot.FindPlayer(protector); ok {
		anchor = pos
		anchorName = protector
	}

	var best *entity.Info
	bestAnchorDist := float32(math.MaxFloat32)
	for _, ent := range visible {
		if !isHostileEntity(ent) {
			continue
		}
		toAnchor := td.distance(anchor, ent.Position)
		if toAnchor < bestAnchorDist {
			bestAnchorDist = toAnchor
			best = ent
		}
	}
	if best == nil || bestAnchorDist > guardEngageRadius {
		return false
	}

	td.logger.Info("ThreatDetector: guard engage",
		"name", best.Name, "id", best.ID,
		"dist_to_protector", bestAnchorDist, "protector", anchorName)
	td.combat.EngageTarget(best.ID)
	return true
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
